package playback

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/httpstream"
)

type byteRecorder struct{ total int64 }

func (r *byteRecorder) AddServedBytes(_ string, n int64) error {
	r.total += n
	return nil
}

type readerFromResponseWriter struct {
	header        http.Header
	body          bytes.Buffer
	readFromCalls int
	deadlineSet   bool
}

func (w *readerFromResponseWriter) Header() http.Header         { return w.header }
func (w *readerFromResponseWriter) WriteHeader(int)             {}
func (w *readerFromResponseWriter) Write(p []byte) (int, error) { return w.body.Write(p) }
func (w *readerFromResponseWriter) SetWriteDeadline(time.Time) error {
	w.deadlineSet = true
	return nil
}
func (w *readerFromResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	w.readFromCalls++
	return w.body.ReadFrom(src)
}

type readOnly struct{ io.Reader }

func TestSessionMeteredWriterPreservesReadFromAndUnwrapChain(t *testing.T) {
	base := &readerFromResponseWriter{header: make(http.Header)}
	production := httpstream.NewRollingDeadlineWriter(base)
	recorder := &byteRecorder{}
	metered := NewSessionMeteredWriter(production, recorder, "s1")

	payload := bytes.Repeat([]byte("x"), 2<<20)
	n, err := metered.ReadFrom(readOnly{bytes.NewReader(payload)})
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if n != int64(len(payload)) || base.readFromCalls == 0 {
		t.Fatalf("ReadFrom n=%d calls=%d, want %d and fast-path calls", n, base.readFromCalls, len(payload))
	}
	if err := metered.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if recorder.total != int64(len(payload)) {
		t.Fatalf("recorded bytes=%d, want %d", recorder.total, len(payload))
	}
	if metered.Unwrap() != production {
		t.Fatal("Unwrap did not expose the next production writer")
	}
	if err := http.NewResponseController(metered).SetWriteDeadline(time.Now()); err != nil {
		t.Fatalf("SetWriteDeadline through chain: %v", err)
	}
	if !base.deadlineSet {
		t.Fatal("response controller did not reach base writer")
	}
}

func TestSessionMeteredWriterFallbackDoesNotRecurseAndFlushesTail(t *testing.T) {
	base := httptest.NewRecorder()
	recorder := &byteRecorder{}
	metered := NewSessionMeteredWriter(base, recorder, "s1")
	payload := []byte("final partial chunk")

	n, err := metered.ReadFrom(readOnly{bytes.NewReader(payload)})
	if err != nil {
		t.Fatalf("ReadFrom fallback: %v", err)
	}
	if n != int64(len(payload)) {
		t.Fatalf("ReadFrom n=%d, want %d", n, len(payload))
	}
	if recorder.total != 0 {
		t.Fatalf("tail flushed before Close: %d", recorder.total)
	}
	defer func() { _ = metered.Close() }()
	if err := metered.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if recorder.total != int64(len(payload)) {
		t.Fatalf("tail bytes=%d, want %d", recorder.total, len(payload))
	}
}
