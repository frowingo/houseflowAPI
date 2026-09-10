package cqrs

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

var (
	ErrNilMediator = errors.New("mediator is nil")
	ErrNilHandler  = errors.New("mediator handler is nil")
	ErrNilRequest  = errors.New("mediator request is nil")
)

type DuplicateHandlerError struct {
	RequestType reflect.Type
}

func (e *DuplicateHandlerError) Error() string {
	return fmt.Sprintf("mediator handler already registered for %s", e.RequestType)
}

type HandlerNotFoundError struct {
	RequestType reflect.Type
}

func (e *HandlerNotFoundError) Error() string {
	return fmt.Sprintf("mediator handler not found for %s", e.RequestType)
}

type UnexpectedResultTypeError struct {
	RequestType  reflect.Type
	ExpectedType reflect.Type
	ActualType   reflect.Type
}

// Request is embedded by a command or query to bind it to its result type.
// The zero-sized marker lets Send reject an incorrect result type at compile time.
type Request[Result any] struct{}

func (Request[Result]) resultType() Result {
	var zero Result
	return zero
}

type TypedRequest[Result any] interface {
	resultType() Result
}

func (e *UnexpectedResultTypeError) Error() string {
	return fmt.Sprintf(
		"mediator handler for %s returned %s, expected %s",
		e.RequestType,
		typeName(e.ActualType),
		typeName(e.ExpectedType),
	)
}

// Sender dispatches a request to its single registered handler.
type Sender interface {
	Send(ctx context.Context, request any) (any, error)
}

type handlerFunc func(context.Context, any) (any, error)

// Mediator is a process-local, concurrency-safe request dispatcher.
type Mediator struct {
	mu       sync.RWMutex
	handlers map[reflect.Type]handlerFunc
}

func New() *Mediator {
	return &Mediator{handlers: make(map[reflect.Type]handlerFunc)}
}

// Register associates exactly one handler with a concrete request type.
func Register[Result any, Message TypedRequest[Result]](m *Mediator, handler Handler[Message, Result]) error {
	if m == nil {
		return ErrNilMediator
	}
	if isNil(handler) {
		return ErrNilHandler
	}

	requestType := typeOf[Message]()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.handlers[requestType]; exists {
		return &DuplicateHandlerError{RequestType: requestType}
	}

	m.handlers[requestType] = func(ctx context.Context, request any) (any, error) {
		typedRequest, ok := request.(Message)
		if !ok {
			return nil, &HandlerNotFoundError{RequestType: reflect.TypeOf(request)}
		}
		return handler.Handle(ctx, typedRequest)
	}
	return nil
}

// MustRegister registers a handler or panics so invalid startup wiring fails fast.
func MustRegister[Result any, Message TypedRequest[Result]](m *Mediator, handler Handler[Message, Result]) {
	if err := Register(m, handler); err != nil {
		panic(err)
	}
}

func (m *Mediator) Send(ctx context.Context, request any) (any, error) {
	if m == nil {
		return nil, ErrNilMediator
	}
	if request == nil {
		return nil, ErrNilRequest
	}

	requestType := reflect.TypeOf(request)
	m.mu.RLock()
	handler, exists := m.handlers[requestType]
	m.mu.RUnlock()
	if !exists {
		return nil, &HandlerNotFoundError{RequestType: requestType}
	}

	return handler(ctx, request)
}

// Send dispatches a request and verifies the handler result type at the boundary.
func Send[Result any, Message TypedRequest[Result]](ctx context.Context, sender Sender, request Message) (Result, error) {
	var zero Result
	if isNil(sender) {
		return zero, ErrNilMediator
	}

	result, err := sender.Send(ctx, request)
	if err != nil {
		return zero, err
	}
	typedResult, ok := result.(Result)
	if !ok {
		return zero, &UnexpectedResultTypeError{
			RequestType:  reflect.TypeOf(request),
			ExpectedType: typeOf[Result](),
			ActualType:   reflect.TypeOf(result),
		}
	}
	return typedResult, nil
}

func typeOf[T any]() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

func typeName(value reflect.Type) string {
	if value == nil {
		return "<nil>"
	}
	return value.String()
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
