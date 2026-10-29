package database

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"houseflowApi/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type fakeTigrisObjectClient struct {
	putInput    *s3.PutObjectInput
	deleteInput *s3.DeleteObjectInput
	putErr      error
	deleteErr   error
}

func (f *fakeTigrisObjectClient) PutObject(
	_ context.Context,
	input *s3.PutObjectInput,
	_ ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	f.putInput = input
	return &s3.PutObjectOutput{}, f.putErr
}

func (f *fakeTigrisObjectClient) DeleteObject(
	_ context.Context,
	input *s3.DeleteObjectInput,
	_ ...func(*s3.Options),
) (*s3.DeleteObjectOutput, error) {
	f.deleteInput = input
	return &s3.DeleteObjectOutput{}, f.deleteErr
}

type fakeTigrisPresignClient struct {
	input   *s3.GetObjectInput
	expires time.Duration
	url     string
	err     error
}

func (f *fakeTigrisPresignClient) PresignGetObject(
	_ context.Context,
	input *s3.GetObjectInput,
	optionFunctions ...func(*s3.PresignOptions),
) (*v4.PresignedHTTPRequest, error) {
	f.input = input
	options := s3.PresignOptions{}
	for _, optionFunction := range optionFunctions {
		optionFunction(&options)
	}
	f.expires = options.Expires
	if f.err != nil {
		return nil, f.err
	}
	return &v4.PresignedHTTPRequest{URL: f.url, Method: "GET"}, nil
}

func TestTigrisContextPutsPrivateImageMetadata(t *testing.T) {
	client := &fakeTigrisObjectClient{}
	storage := newTigrisContext(client, &fakeTigrisPresignClient{}, "private-bucket")
	body := []byte("image-bytes")

	if err := storage.PutImage(
		context.Background(),
		"houses/house/chores/chore/review-rounds/1/photo.jpg",
		bytes.NewReader(body),
		int64(len(body)),
		"image/jpeg",
	); err != nil {
		t.Fatal(err)
	}
	if client.putInput == nil {
		t.Fatal("put object was not called")
	}
	if got := *client.putInput.Bucket; got != "private-bucket" {
		t.Fatalf("bucket = %q", got)
	}
	if got := *client.putInput.Key; got != "houses/house/chores/chore/review-rounds/1/photo.jpg" {
		t.Fatalf("key = %q", got)
	}
	if got := *client.putInput.ContentLength; got != int64(len(body)) {
		t.Fatalf("content length = %d", got)
	}
	if got := *client.putInput.ContentType; got != "image/jpeg" {
		t.Fatalf("content type = %q", got)
	}
	if client.putInput.ACL != "" {
		t.Fatalf("put unexpectedly sets a public ACL: %q", client.putInput.ACL)
	}
}

func TestTigrisContextDeletesObject(t *testing.T) {
	client := &fakeTigrisObjectClient{}
	storage := newTigrisContext(client, &fakeTigrisPresignClient{}, "private-bucket")

	if err := storage.DeleteObject(context.Background(), "private/object.jpg"); err != nil {
		t.Fatal(err)
	}
	if client.deleteInput == nil || *client.deleteInput.Bucket != "private-bucket" || *client.deleteInput.Key != "private/object.jpg" {
		t.Fatalf("unexpected delete input: %+v", client.deleteInput)
	}
}

func TestTigrisContextPresignsGetWithRequestedTTL(t *testing.T) {
	presigner := &fakeTigrisPresignClient{url: "https://private.example.test/signed"}
	storage := newTigrisContext(&fakeTigrisObjectClient{}, presigner, "private-bucket")

	url, err := storage.PresignGet(context.Background(), "private/object.jpg", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if url != presigner.url || presigner.expires != 5*time.Minute {
		t.Fatalf("url/expires = %q/%s", url, presigner.expires)
	}
	if presigner.input == nil || *presigner.input.Bucket != "private-bucket" || *presigner.input.Key != "private/object.jpg" {
		t.Fatalf("unexpected presign input: %+v", presigner.input)
	}
}

func TestNewTigrisContextUsesConfiguredVirtualHostedEndpoint(t *testing.T) {
	storage, err := NewTigrisContext(context.Background(), config.ConfigTigris{
		Endpoint:        "https://t3.storage.dev",
		Region:          "auto",
		Bucket:          "private-bucket",
		AccessKeyID:     "tid_test",
		SecretAccessKey: "tsec_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	signedURL, err := storage.PresignGet(context.Background(), "private/object.jpg", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "private-bucket.t3.storage.dev" || parsed.Path != "/private/object.jpg" {
		t.Fatalf("unexpected virtual-hosted Tigris URL: %s", signedURL)
	}
	if !strings.Contains(parsed.RawQuery, "X-Amz-Expires=300") || !strings.Contains(parsed.RawQuery, "X-Amz-Signature=") {
		t.Fatalf("URL is not signed with the requested expiry: %s", signedURL)
	}
}

func TestTigrisContextValidatesInputsAndWrapsProviderErrors(t *testing.T) {
	providerError := errors.New("provider unavailable")
	client := &fakeTigrisObjectClient{putErr: providerError, deleteErr: providerError}
	presigner := &fakeTigrisPresignClient{err: providerError}
	storage := newTigrisContext(client, presigner, "private-bucket")

	if err := storage.PutImage(context.Background(), "", bytes.NewReader(nil), 1, "image/jpeg"); err == nil {
		t.Fatal("empty object key must be rejected")
	}
	if err := storage.PutImage(context.Background(), "key", nil, 1, "image/jpeg"); err == nil {
		t.Fatal("nil image reader must be rejected")
	}
	if err := storage.PutImage(context.Background(), "key", bytes.NewReader(nil), 0, "image/jpeg"); err == nil {
		t.Fatal("non-positive image size must be rejected")
	}
	if err := storage.PutImage(context.Background(), "key", bytes.NewReader([]byte("x")), 1, ""); err == nil {
		t.Fatal("empty content type must be rejected")
	}
	if err := storage.PutImage(context.Background(), "key", bytes.NewReader([]byte("x")), 1, "image/jpeg"); !errors.Is(err, providerError) {
		t.Fatalf("put error = %v", err)
	}
	if err := storage.DeleteObject(context.Background(), "key"); !errors.Is(err, providerError) {
		t.Fatalf("delete error = %v", err)
	}
	if _, err := storage.PresignGet(context.Background(), "key", 0); err == nil {
		t.Fatal("non-positive presign ttl must be rejected")
	}
	if _, err := storage.PresignGet(context.Background(), "key", time.Minute); !errors.Is(err, providerError) {
		t.Fatalf("presign error = %v", err)
	}
}
