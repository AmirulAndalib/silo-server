package playback

import (
	"io"
	"net/http"
)

const meteredWriterFlushBytes = 1 << 20

// ServedBytesRecorder receives server-observed byte counts.
type ServedBytesRecorder interface {
	AddServedBytes(sessionID string, n int64) error
}

// SessionMeteredWriter preserves optional ResponseWriter capabilities while
// attributing bytes to a playback session in coarse chunks.
type SessionMeteredWriter struct {
	http.ResponseWriter
	recorder  ServedBytesRecorder
	sessionID string
	pending   int64
}

func NewSessionMeteredWriter(w http.ResponseWriter, recorder ServedBytesRecorder, sessionID string) *SessionMeteredWriter {
	return &SessionMeteredWriter{ResponseWriter: w, recorder: recorder, sessionID: sessionID}
}

func (w *SessionMeteredWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.account(int64(n))
	return n, err
}

func (w *SessionMeteredWriter) ReadFrom(src io.Reader) (int64, error) {
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(src)
		w.account(n)
		return n, err
	}
	return io.Copy(meteredWriteOnly{w}, src)
}

type meteredWriteOnly struct{ io.Writer }

func (w *SessionMeteredWriter) account(n int64) {
	if n <= 0 {
		return
	}
	w.pending += n
	if w.pending >= meteredWriterFlushBytes {
		w.flush()
	}
}

func (w *SessionMeteredWriter) flush() {
	if w.pending <= 0 {
		return
	}
	if w.recorder != nil {
		_ = w.recorder.AddServedBytes(w.sessionID, w.pending)
	}
	w.pending = 0
}

// Close flushes the final partial chunk. Callers must defer it.
func (w *SessionMeteredWriter) Close() error {
	w.flush()
	return nil
}

func (w *SessionMeteredWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying connection.
func (w *SessionMeteredWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
