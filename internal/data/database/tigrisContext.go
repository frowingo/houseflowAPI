package database

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	storageAbstract "houseflowApi/internal/application/storage/abstract"
	"houseflowApi/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type tigrisObjectClient interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

type tigrisPresignClient interface {
	PresignGetObject(context.Context, *s3.GetObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

type TigrisContext struct {
	client    tigrisObjectClient
	presigner tigrisPresignClient
	bucket    string
}

func NewTigrisContext(ctx context.Context, tigrisConfig config.ConfigTigris) (*TigrisContext, error) {
	if !tigrisConfig.Configured() {
		return nil, errors.New("tigris config is required")
	}
	if err := config.ValidateTigris(tigrisConfig); err != nil {
		return nil, err
	}

	awsConfig, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(tigrisConfig.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			tigrisConfig.AccessKeyID,
			tigrisConfig.SecretAccessKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("load tigris S3 config: %w", err)
	}

	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(strings.TrimRight(tigrisConfig.Endpoint, "/"))
		options.UsePathStyle = false
	})
	return newTigrisContext(client, s3.NewPresignClient(client), tigrisConfig.Bucket), nil
}

func newTigrisContext(client tigrisObjectClient, presigner tigrisPresignClient, bucket string) *TigrisContext {
	return &TigrisContext{client: client, presigner: presigner, bucket: bucket}
}

func (t *TigrisContext) PutImage(
	ctx context.Context,
	objectKey string,
	reader io.Reader,
	size int64,
	contentType string,
) error {
	if err := validateObjectKey(objectKey); err != nil {
		return err
	}
	if reader == nil {
		return errors.New("image reader is required")
	}
	if size <= 0 {
		return errors.New("image size must be greater than zero")
	}
	if strings.TrimSpace(contentType) == "" {
		return errors.New("image content type is required")
	}

	_, err := t.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(t.bucket),
		Key:           aws.String(objectKey),
		Body:          reader,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("put tigris object: %w", err)
	}
	return nil
}

func (t *TigrisContext) DeleteObject(ctx context.Context, objectKey string) error {
	if err := validateObjectKey(objectKey); err != nil {
		return err
	}
	_, err := t.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(t.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return fmt.Errorf("delete tigris object: %w", err)
	}
	return nil
}

func (t *TigrisContext) PresignGet(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	if err := validateObjectKey(objectKey); err != nil {
		return "", err
	}
	if ttl <= 0 {
		return "", errors.New("presigned URL ttl must be greater than zero")
	}

	request, err := t.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(t.bucket),
		Key:    aws.String(objectKey),
	}, func(options *s3.PresignOptions) {
		options.Expires = ttl
	})
	if err != nil {
		return "", fmt.Errorf("presign tigris object: %w", err)
	}
	if request == nil || request.URL == "" {
		return "", errors.New("presign tigris object returned an empty URL")
	}
	return request.URL, nil
}

func validateObjectKey(objectKey string) error {
	if strings.TrimSpace(objectKey) == "" {
		return errors.New("object key is required")
	}
	return nil
}

var _ storageAbstract.ObjectStorage = (*TigrisContext)(nil)
