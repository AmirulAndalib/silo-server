package streamtelemetry

import (
	"context"
	"sync"
)

type SnapshotStore interface {
	Publish(context.Context, Snapshot) error
	Load(context.Context) (Snapshot, error)
}

type LocalStore struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

func NewLocalStore() *LocalStore { return &LocalStore{} }

func (s *LocalStore) Publish(_ context.Context, snapshot Snapshot) error {
	s.mu.Lock()
	s.snapshot = cloneSnapshot(snapshot)
	s.mu.Unlock()
	return nil
}

func (s *LocalStore) Load(_ context.Context) (Snapshot, error) {
	s.mu.RLock()
	snapshot := cloneSnapshot(s.snapshot)
	s.mu.RUnlock()
	return snapshot, nil
}
