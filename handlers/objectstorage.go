package handlers

import (
	"context"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ObjectStorage is the minimal object-store surface WharfHandlers needs; it
// exists so archive rebuild/eviction logic can run against an in-memory fake.
type ObjectStorage interface {
	Head(ctx context.Context, key string) (int64, error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	Delete(ctx context.Context, key string) error
	PresignGet(ctx context.Context, key string, expiry time.Duration) (string, error)
	PresignPut(ctx context.Context, key string, expiry time.Duration) (string, error)
}

type s3ObjectStorage struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucket        string
}

func newS3ObjectStorage(client *s3.Client, presignClient *s3.PresignClient, bucket string) *s3ObjectStorage {
	return &s3ObjectStorage{client: client, presignClient: presignClient, bucket: bucket}
}

func (s *s3ObjectStorage) Head(ctx context.Context, key string) (int64, error) {
	stat, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, err
	}
	if stat.ContentLength == nil {
		return 0, nil
	}
	return *stat.ContentLength, nil
}

func (s *s3ObjectStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	object, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	return object.Body, nil
}

func (s *s3ObjectStorage) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	return err
}

func (s *s3ObjectStorage) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

func (s *s3ObjectStorage) PresignGet(ctx context.Context, key string, expiry time.Duration) (string, error) {
	presignedURL, err := s.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, func(options *s3.PresignOptions) {
		options.Expires = expiry
	})
	if err != nil {
		return "", err
	}
	return presignedURL.URL, nil
}

func (s *s3ObjectStorage) PresignPut(ctx context.Context, key string, expiry time.Duration) (string, error) {
	presignedURL, err := s.presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, func(options *s3.PresignOptions) {
		options.Expires = expiry
	})
	if err != nil {
		return "", err
	}
	return presignedURL.URL, nil
}
