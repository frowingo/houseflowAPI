package tests

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"houseflowApi/internal/infrastructure/cqrs"
)

type mediatorTestRequest struct {
	cqrs.Request[string]
	Value string
}

type mediatorUnknownRequest struct {
	cqrs.Request[string]
}

type mediatorIntRequest struct {
	cqrs.Request[int]
}

type mediatorContextKey struct{}

type mediatorTestHandler struct {
	receivedContextValue string
}

type mediatorWrongResultSender struct{}

func (mediatorWrongResultSender) Send(context.Context, any) (any, error) {
	return "not an integer", nil
}

type mediatorCountingHandler struct {
	calls atomic.Int64
}

func (h *mediatorCountingHandler) Handle(_ context.Context, request mediatorTestRequest) (string, error) {
	h.calls.Add(1)
	return request.Value, nil
}

func (h *mediatorTestHandler) Handle(ctx context.Context, request mediatorTestRequest) (string, error) {
	value, _ := ctx.Value(mediatorContextKey{}).(string)
	h.receivedContextValue = value
	return request.Value, nil
}

func TestMediatorDispatchesToRegisteredHandlerWithContext(t *testing.T) {
	sender := cqrs.New()
	handler := &mediatorTestHandler{}
	if err := cqrs.Register[string, mediatorTestRequest](sender, handler); err != nil {
		t.Fatal(err)
	}

	ctx := context.WithValue(context.Background(), mediatorContextKey{}, "request-context")
	result, err := cqrs.Send[string](ctx, sender, mediatorTestRequest{Value: "handled"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "handled" {
		t.Fatalf("result = %q, want %q", result, "handled")
	}
	if handler.receivedContextValue != "request-context" {
		t.Fatalf("context value = %q, want %q", handler.receivedContextValue, "request-context")
	}
}

func TestMediatorRejectsDuplicateHandlerRegistration(t *testing.T) {
	sender := cqrs.New()
	if err := cqrs.Register[string, mediatorTestRequest](sender, &mediatorTestHandler{}); err != nil {
		t.Fatal(err)
	}

	err := cqrs.Register[string, mediatorTestRequest](sender, &mediatorTestHandler{})
	var duplicateError *cqrs.DuplicateHandlerError
	if !errors.As(err, &duplicateError) {
		t.Fatalf("error = %v, want DuplicateHandlerError", err)
	}
}

func TestMediatorReturnsErrorForMissingHandler(t *testing.T) {
	sender := cqrs.New()

	_, err := cqrs.Send[string](context.Background(), sender, mediatorUnknownRequest{})
	var notFoundError *cqrs.HandlerNotFoundError
	if !errors.As(err, &notFoundError) {
		t.Fatalf("error = %v, want HandlerNotFoundError", err)
	}
}

func TestMediatorRejectsUnexpectedResultType(t *testing.T) {
	_, err := cqrs.Send[int](context.Background(), mediatorWrongResultSender{}, mediatorIntRequest{})
	var resultTypeError *cqrs.UnexpectedResultTypeError
	if !errors.As(err, &resultTypeError) {
		t.Fatalf("error = %v, want UnexpectedResultTypeError", err)
	}
}

func TestMediatorMustRegisterPanicsOnInvalidWiring(t *testing.T) {
	sender := cqrs.New()
	cqrs.MustRegister[string, mediatorTestRequest](sender, &mediatorTestHandler{})

	defer func() {
		if recover() == nil {
			t.Fatal("MustRegister did not panic for duplicate handler")
		}
	}()
	cqrs.MustRegister[string, mediatorTestRequest](sender, &mediatorTestHandler{})
}

func TestMediatorDispatchesConcurrentRequestsSafely(t *testing.T) {
	sender := cqrs.New()
	handler := &mediatorCountingHandler{}
	cqrs.MustRegister[string, mediatorTestRequest](sender, handler)

	const requestCount = 64
	errorsChannel := make(chan error, requestCount)
	var workers sync.WaitGroup
	for range requestCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := cqrs.Send[string](context.Background(), sender, mediatorTestRequest{Value: "handled"})
			if err == nil && result != "handled" {
				err = errors.New("unexpected mediator result")
			}
			errorsChannel <- err
		}()
	}
	workers.Wait()
	close(errorsChannel)

	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls := handler.calls.Load(); calls != requestCount {
		t.Fatalf("handler calls = %d, want %d", calls, requestCount)
	}
}
