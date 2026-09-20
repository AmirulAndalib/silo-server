package handlers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type roomWriterSocket struct {
	started   chan struct{}
	release   chan struct{}
	closed    chan struct{}
	frames    chan string
	once      sync.Once
	closeOnce sync.Once
}

func newRoomWriterSocket(block bool) *roomWriterSocket {
	s := &roomWriterSocket{started: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{}), frames: make(chan string, 128)}
	if !block {
		close(s.release)
	}
	return s
}
func (*roomWriterSocket) SetWriteDeadline(time.Time) error { return nil }
func (s *roomWriterSocket) WriteMessage(_ int, payload []byte) error {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.closed:
		return errors.New("closed")
	case <-s.release:
	}
	s.frames <- string(payload)
	return nil
}
func (s *roomWriterSocket) WriteControl(_ int, _ []byte, _ time.Time) error { return nil }
func (s *roomWriterSocket) Close() error                                    { s.closeOnce.Do(func() { close(s.closed) }); return nil }

func TestWatchTogetherSlowWriterDoesNotBlockOtherViewers(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	slow, fast := newRoomWriterSocket(true), newRoomWriterSocket(false)
	a, b := newWatchTogetherRoomConn(slow), newWatchTogetherRoomConn(fast)
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	if err := a.WriteJSON(map[string]int{"sequence": 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-slow.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for i := range watchTogetherQueueSize {
		if err := a.WriteJSON(map[string]int{"sequence": i + 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.WriteJSON(map[string]int{"sequence": 99}); err == nil {
		t.Fatal("full queue accepted another frame")
	}
	select {
	case <-slow.closed:
	default:
		t.Fatal("slow connection was not closed")
	}
	for i := range 3 {
		if err := b.WriteJSON(map[string]int{"sequence": i}); err != nil {
			t.Fatal(err)
		}
	}
	for _, expected := range []string{`{"sequence":0}`, `{"sequence":1}`, `{"sequence":2}`} {
		select {
		case frame := <-fast.frames:
			if frame != expected {
				t.Fatalf("order: got %s want %s", frame, expected)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}
