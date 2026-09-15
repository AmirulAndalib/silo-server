package artworkstore

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Silo-Server/silo-server/internal/s3client"
)

type SettingsStore interface {
	Get(context.Context, string) (string, error)
	SetIfAbsent(context.Context, string, string) (bool, error)
}

type Options struct {
	Backend   string
	LocalPath string
	S3        *s3client.Client
	Settings  SettingsStore
}

const activeBackendKey = "artwork.storage_backend_active"

func Open(ctx context.Context, opts Options) (Store, string, error) {
	backend := strings.ToLower(strings.TrimSpace(opts.Backend))
	if backend == "" || backend == "auto" {
		if opts.S3 != nil {
			backend = BackendS3
		} else {
			backend = BackendLocal
		}
	}
	var store Store
	var err error
	switch backend {
	case BackendLocal:
		store, err = NewFilesystem(opts.LocalPath)
	case BackendS3:
		if opts.S3 == nil {
			return nil, "", fmt.Errorf("artwork storage backend s3 is configured but no S3 client is available")
		}
		store = NewS3(opts.S3)
	default:
		return nil, "", fmt.Errorf("unknown artwork storage backend %q", opts.Backend)
	}
	if err != nil {
		return nil, "", err
	}
	// Availability is checked by readiness through Probe, allowing outage recovery.
	if opts.Settings == nil {
		return store, backend, nil
	}
	active, err := opts.Settings.Get(ctx, activeBackendKey)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", activeBackendKey, err)
	}
	if active != "" && active != backend {
		return nil, "", fmt.Errorf("artwork storage backend is recorded as %q but configured as %q; copy the artwork tree between stores, then delete the %s row", active, backend, activeBackendKey)
	}
	recorded := &recordingStore{Store: store, settings: opts.Settings, backend: backend}
	if direct, ok := store.(DirectURLer); ok {
		return &recordingDirectStore{recordingStore: recorded, DirectURLer: direct}, backend, nil
	}
	return recorded, backend, nil
}

type recordingStore struct {
	Store
	settings SettingsStore
	backend  string
	mu       sync.Mutex
	recorded bool
}

type recordingDirectStore struct {
	*recordingStore
	DirectURLer
}

func (s *recordingDirectStore) ObjectAvailable(ctx context.Context, key string) (bool, error) {
	checker, ok := s.Store.(interface {
		ObjectAvailable(context.Context, string) (bool, error)
	})
	if !ok {
		return false, fmt.Errorf("artwork backend does not support external availability checks")
	}
	return checker.ObjectAvailable(ctx, key)
}

func (s *recordingStore) Put(ctx context.Context, key string, data []byte) error {
	if err := s.Store.Put(ctx, key, data); err != nil {
		return err
	}
	return s.recordBackend(ctx)
}

func (s *recordingStore) recordBackend(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recorded {
		return nil
	}
	inserted, err := s.settings.SetIfAbsent(ctx, activeBackendKey, s.backend)
	if err != nil {
		return fmt.Errorf("record artwork backend: %w", err)
	}
	if !inserted {
		active, err := s.settings.Get(ctx, activeBackendKey)
		if err != nil {
			return fmt.Errorf("verify recorded artwork backend: %w", err)
		}
		if active != s.backend {
			return fmt.Errorf("artwork backend changed concurrently: recorded %q, writing %q", active, s.backend)
		}
	}
	s.recorded = true
	return nil
}
