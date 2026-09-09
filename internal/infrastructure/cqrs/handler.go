package cqrs

import "context"

// Handler is the common execution contract used by the in-process mediator.
type Handler[Request any, Result any] interface {
	Handle(ctx context.Context, request Request) (Result, error)
}

// CommandHandler handles a state-changing application use case.
type CommandHandler[Command any, Result any] interface {
	Handler[Command, Result]
}

// QueryHandler handles a side-effect-free application use case.
type QueryHandler[Query any, Result any] interface {
	Handler[Query, Result]
}

// NoResult is returned by commands that complete without a response payload.
type NoResult struct{}
