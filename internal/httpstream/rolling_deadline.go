// Package httpstream provides helpers for HTTP handlers that stream large or
// long-lived response bodies (direct play, remux, downloads).
//
// The main API server sets an absolute WriteTimeout, which kills any response
// still being written when the deadline elapses — including perfectly healthy
// multi-gigabyte media streams. RollingDeadlineWriter replaces that contract
// for streaming responses only: the connection's write deadline is pushed
// forward on every successful write, so a response that keeps making progress
// lives indefinitely while a stalled one is still reaped within the window.
package httpstream

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	// DefaultStallWindow is how long a streaming response may go without
	// forward progress before its connection is reaped.
	DefaultStallWindow = 180 * time.Second

	// stallWindowEnv overrides DefaultStallWindow (integer seconds).
	stallWindowEnv = "SILO_STREAM_WRITE_STALL_TIMEOUT"

	// bumpStep rate-limits deadline updates so a busy stream issues one
	// SetWriteDeadline per step rather than one per 32 KB chunk.
	bumpStep = 15 * time.Second

	// readFromChunk bounds each ReadFrom slice so the deadline keeps rolling
	// during zero-copy (sendfile) transfers of large files.
	readFromChunk int64 = 64 << 20
)

// StreamOutcome classifies how a streaming response ended.
type StreamOutcome string

const (
	OutcomeCompleted   StreamOutcome = "completed"
	OutcomeStalledReap StreamOutcome = "stalled_reap"
	OutcomeClientGone  StreamOutcome = "client_gone"
)

// StallWindow returns the configured stall window for streaming responses.
func StallWindow() time.Duration {
	if v := os.Getenv(stallWindowEnv); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return DefaultStallWindow
}

// RollingDeadlineWriter wraps a streaming response and rolls the connection's
// write deadline forward as the body makes progress. Construct with
// NewRollingDeadlineWriter and use in place of the original ResponseWriter.
//
// If the underlying transport does not support per-response write deadlines
// (SetWriteDeadline errors), the wrapper degrades to a plain pass-through and
// the server-level WriteTimeout, if any, stays in effect.
type RollingDeadlineWriter struct {
	w        http.ResponseWriter
	rc       *http.ResponseController
	window   time.Duration
	step     time.Duration
	lastBump time.Time
	disabled bool
	latch    *CutLatch

	statusCode    int
	bytesWritten  int64
	firstWriteErr error
}

// CutLatch records that a stream has been terminally cut. Once latched, a
// RollingDeadlineWriter must never push the write deadline back out again: the
// cut is a deliberate hang-up, not a stall.
type CutLatch struct {
	cut atomic.Bool
}

func (l *CutLatch) Cut() {
	if l != nil {
		l.cut.Store(true)
	}
}

func (l *CutLatch) IsCut() bool {
	return l != nil && l.cut.Load()
}

type cutLatchContextKey struct{}

// WithCutLatch carries l on ctx so rolling writers constructed inside serving
// helpers can observe a cut made by a watcher around an inner writer.
func WithCutLatch(ctx context.Context, l *CutLatch) context.Context {
	return context.WithValue(ctx, cutLatchContextKey{}, l)
}

// CutLatchFrom returns the stream cut latch carried by ctx, if any.
func CutLatchFrom(ctx context.Context) *CutLatch {
	if ctx == nil {
		return nil
	}
	l, _ := ctx.Value(cutLatchContextKey{}).(*CutLatch)
	return l
}

// NewRollingDeadlineWriter wraps w with the configured stall window.
func NewRollingDeadlineWriter(w http.ResponseWriter) *RollingDeadlineWriter {
	return newRollingDeadlineWriterWithLatch(w, StallWindow(), bumpStep, nil)
}

// NewRollingDeadlineWriterCtx wraps w and observes a terminal cut latch carried
// by ctx. Callers without a revocable request can use NewRollingDeadlineWriter.
func NewRollingDeadlineWriterCtx(ctx context.Context, w http.ResponseWriter) *RollingDeadlineWriter {
	return newRollingDeadlineWriterWithLatch(w, StallWindow(), bumpStep, CutLatchFrom(ctx))
}

func newRollingDeadlineWriter(w http.ResponseWriter, window, step time.Duration) *RollingDeadlineWriter {
	return newRollingDeadlineWriterWithLatch(w, window, step, nil)
}

func newRollingDeadlineWriterWithLatch(w http.ResponseWriter, window, step time.Duration, latch *CutLatch) *RollingDeadlineWriter {
	s := &RollingDeadlineWriter{
		w:      w,
		rc:     http.NewResponseController(w),
		window: window,
		step:   step,
		latch:  latch,
	}
	s.bump()
	return s
}

func (s *RollingDeadlineWriter) bump() {
	if s.disabled || s.latch.IsCut() {
		return
	}
	now := time.Now()
	if !s.lastBump.IsZero() && now.Sub(s.lastBump) < s.step {
		return
	}
	if err := s.rc.SetWriteDeadline(now.Add(s.window)); err != nil {
		s.disabled = true
		return
	}
	// Close the check/set race with a concurrent cut: if the watcher latched
	// after the first check but before the future deadline landed, immediately
	// restore the terminal deadline instead of leaving the socket re-armed.
	if s.latch.IsCut() {
		_ = s.rc.SetWriteDeadline(time.Now())
		return
	}
	s.lastBump = now
}

func (s *RollingDeadlineWriter) Header() http.Header { return s.w.Header() }

func (s *RollingDeadlineWriter) WriteHeader(code int) {
	s.bump()
	if s.statusCode == 0 {
		s.statusCode = code
	}
	s.w.WriteHeader(code)
}

func (s *RollingDeadlineWriter) Write(p []byte) (int, error) {
	s.bump()
	if s.statusCode == 0 {
		s.statusCode = http.StatusOK
	}
	n, err := s.w.Write(p)
	s.recordWrite(int64(n), err)
	return n, err
}

// ReadFrom preserves the underlying ResponseWriter's io.ReaderFrom fast path
// (sendfile for *os.File bodies, as used by http.ServeContent) while still
// rolling the deadline between bounded slices.
func (s *RollingDeadlineWriter) ReadFrom(r io.Reader) (int64, error) {
	rf, ok := s.w.(io.ReaderFrom)
	if !ok {
		// writerOnly hides this method so io.Copy doesn't recurse into it.
		s.bump()
		return io.Copy(writerOnly{s}, r)
	}
	var total int64
	for {
		s.bump()
		if s.statusCode == 0 {
			s.statusCode = http.StatusOK
		}
		n, err := rf.ReadFrom(io.LimitReader(r, readFromChunk))
		total += n
		s.recordWrite(n, err)
		if err != nil {
			return total, err
		}
		if n < readFromChunk {
			return total, nil
		}
	}
}

func (s *RollingDeadlineWriter) Flush() {
	s.bump()
	_ = s.rc.Flush()
}

// StatusCode returns the response status observed by the wrapper.
func (s *RollingDeadlineWriter) StatusCode() int {
	return s.statusCode
}

// BytesWritten returns the number of response body bytes accepted by the
// underlying writer.
func (s *RollingDeadlineWriter) BytesWritten() int64 {
	return s.bytesWritten
}

// Outcome classifies the first write failure, or a canceled request when no
// write failure was surfaced by the transport.
func (s *RollingDeadlineWriter) Outcome(ctx context.Context) StreamOutcome {
	if isTimeoutError(s.firstWriteErr) {
		return OutcomeStalledReap
	}
	if s.firstWriteErr != nil || (ctx != nil && ctx.Err() != nil) {
		return OutcomeClientGone
	}
	return OutcomeCompleted
}

// Unwrap lets http.ResponseController traverse to the underlying writer.
func (s *RollingDeadlineWriter) Unwrap() http.ResponseWriter { return s.w }

func (s *RollingDeadlineWriter) recordWrite(n int64, err error) {
	s.bytesWritten += n
	if err != nil && s.firstWriteErr == nil {
		s.firstWriteErr = err
	}
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

type writerOnly struct{ io.Writer }
