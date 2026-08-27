package config

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3ClientAPI is the mockable interface for S3 GetObject operations.
type S3ClientAPI interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// S3URI represents a parsed s3://bucket/key location.
type S3URI struct {
	Bucket string
	Key    string
}

// ParseS3URI parses an S3 URI of format s3://<bucket>/<key>.
func ParseS3URI(rawURI string) (*S3URI, error) {
	if !strings.HasPrefix(rawURI, "s3://") {
		return nil, fmt.Errorf("invalid S3 URI '%s': must begin with 's3://'", rawURI)
	}
	trimmed := strings.TrimPrefix(rawURI, "s3://")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid S3 URI '%s': expected format s3://<bucket>/<key>", rawURI)
	}
	return &S3URI{
		Bucket: parts[0],
		Key:    parts[1],
	}, nil
}
