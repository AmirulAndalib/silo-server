// Package artworkstore stores logical artwork keys in local or S3 storage.
package artworkstore

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrNotFound   = errors.New("artworkstore: object not found")
	ErrInvalidKey = errors.New("artworkstore: invalid key")
)

const (
	BackendLocal = "local"
	BackendS3    = "s3"
)

type DirectURLer interface {
	DirectURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

type ObjectInfo struct {
	Key     string
	Size    int64
	ModTime time.Time
	ETag    string
}
type Store interface {
	// Put idempotently overwrites an object. Content matching is not required.
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	// Delete counts absent keys as deleted, matching S3 batch deletion.
	Delete(ctx context.Context, keys []string) (int, error)
	// DeletePrefix removes a directory subtree and counts removed regular files.
	DeletePrefix(ctx context.Context, prefix string) (int, error)
	// List returns lexical key order. Cursor is the last returned key, or empty
	// at the end. An empty prefix lists the store; a nonpositive limit lists all.
	List(ctx context.Context, prefix, cursor string, limit int) ([]ObjectInfo, string, error)
	// Probe checks storage access. Callers cache readiness probes for 30 seconds.
	Probe(ctx context.Context) error
}
