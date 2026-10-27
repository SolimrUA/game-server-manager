// Package status reads what a server reports about itself, and what its installer reported.
package status

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"personal-game-server-manager/lambda/internal/server"
)

// S3 holds the status objects, which the status agent on each server writes.
type S3 interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// Reader reads a server's reported status from the bucket both it and the installer write to.
type Reader struct {
	s3     S3
	bucket string
}

func NewReader(client S3, bucket string) *Reader {
	return &Reader{s3: client, bucket: bucket}
}

// Game reads a server's reported status. A status pushed before the current boot is stale, so it is
// reported as nothing rather than as the running game's.
func (r *Reader) Game(ctx context.Context, key string, launchedAt time.Time) (server.GameStatus, error) {
	if r.bucket == "" || key == "" {
		return nil, nil
	}

	var status server.GameStatus
	if err := r.read(ctx, "status/"+key+".json", &status); err != nil {
		return nil, err
	}

	pushedAt, err := time.Parse("2006-01-02T15:04:05Z", fmt.Sprint(status["updatedAt"]))
	if err == nil && !launchedAt.IsZero() && pushedAt.Before(launchedAt) {
		return nil, nil
	}
	return status, nil
}

// Install is what the installer reported through its presigned URL, so a server that failed to
// install says so instead of looking like one that is still starting.
func (r *Reader) Install(ctx context.Context, key string) (string, error) {
	if r.bucket == "" || key == "" {
		return "", nil
	}

	var install struct {
		Status string `json:"status"`
	}
	if err := r.read(ctx, "status/"+key+".install.json", &install); err != nil {
		return "", err
	}
	return install.Status, nil
}

// MarkInstalling replaces any install marker left by a previous server of this id, so an earlier
// outcome can't be read as this one's while it is still installing.
func (r *Reader) MarkInstalling(ctx context.Context, key string) error {
	body, err := json.Marshal(map[string]string{"status": "installing"})
	if err != nil {
		return err
	}
	_, err = r.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(r.bucket),
		Key:         aws.String("status/" + key + ".install.json"),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("marking %s as installing: %w", key, err)
	}
	return nil
}

func (r *Reader) read(ctx context.Context, key string, into any) error {
	out, err := r.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("reading %s: %w", key, err)
	}
	defer out.Body.Close()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		return fmt.Errorf("reading %s: %w", key, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("parsing %s: %w", key, err)
	}
	return nil
}
