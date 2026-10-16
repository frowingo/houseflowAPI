package appEvents

import (
	"context"
	"errors"
	"testing"
)

type commandStub struct{ err error }

func (stub commandStub) Handle(_ context.Context, value string) (string, error) {
	if stub.err != nil {
		return "", stub.err
	}
	return value + "-saved", nil
}

func TestNotificationRunsOnlyAfterSuccessfulCommand(t *testing.T) {
	called := 0
	notify := func(_ context.Context, command, result string) {
		called++
		if command != "change" || result != "change-saved" {
			t.Fatalf("notification received %q, %q", command, result)
		}
	}
	if _, err := NotifyAfter(commandStub{}, notify).Handle(context.Background(), "change"); err != nil {
		t.Fatal(err)
	}
	if _, err := NotifyAfter(commandStub{err: errors.New("rejected")}, notify).Handle(context.Background(), "change"); err == nil {
		t.Fatal("failed command unexpectedly succeeded")
	}
	if called != 1 {
		t.Fatalf("notification count = %d, want 1", called)
	}
}
