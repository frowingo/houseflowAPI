package abstract

import (
	"context"
	"io"
	"time"
)

// ObjectStorage defines the private object operations used by application
// workflows. Callers own object-key generation and authorization decisions.
type ObjectStorage interface {
	PutImage(ctx context.Context, objectKey string, reader io.Reader, size int64, contentType string) error
	DeleteObject(ctx context.Context, objectKey string) error
	PresignGet(ctx context.Context, objectKey string, ttl time.Duration) (string, error)
}
