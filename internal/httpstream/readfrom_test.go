package httpstream

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type readerFromResponseWriter struct {
	bytes.Buffer
	called int
	header http.Header
}

func (w *readerFromResponseWriter) Header() http.Header { return w.header }
func (w *readerFromResponseWriter) WriteHeader(int)     {}
func (w *readerFromResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	w.called++
	return io.Copy(&w.Buffer, r)
}

func TestCopyChunkedUsesReaderFromPerSlice(t *testing.T) {
	w := &readerFromResponseWriter{header: make(http.Header)}
	rf, ok := ReaderFromOf(w)
	if !ok {
		t.Fatal("ReaderFromOf did not report direct implementation")
	}
	var recorded int64
	n, err := CopyChunked(rf, bytes.NewReader(make([]byte, 10)), 4, func(n int64, _ error) { recorded += n })
	if err != nil || n != 10 || recorded != 10 || w.called != 3 {
		t.Fatalf("CopyChunked = n=%d err=%v recorded=%d calls=%d", n, err, recorded, w.called)
	}
}

func TestWriterOnlyHidesReaderFrom(t *testing.T) {
	w := &readerFromResponseWriter{header: make(http.Header)}
	if _, ok := WriterOnly(w).(io.ReaderFrom); ok {
		t.Fatal("WriterOnly exposed io.ReaderFrom")
	}
}

func TestCompressExceptBypassPreservesReaderFromAndKeptRouteCompresses(t *testing.T) {
	handlerSawReaderFrom := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, handlerSawReaderFrom = w.(io.ReaderFrom)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("x"), 2048))
	})
	handler := CompressExcept(gzip.BestSpeed, func(r *http.Request) bool { return r.URL.Path == "/media" })(next)

	mediaWriter := &readerFromResponseWriter{header: make(http.Header)}
	mediaReq := httptest.NewRequest(http.MethodGet, "/media", nil)
	mediaReq.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(mediaWriter, mediaReq)
	if !handlerSawReaderFrom || mediaWriter.Header().Get("Content-Encoding") != "" || mediaWriter.Len() != 2048 {
		t.Fatalf("bypass: saw ReaderFrom=%v encoding=%q bytes=%d", handlerSawReaderFrom, mediaWriter.Header().Get("Content-Encoding"), mediaWriter.Len())
	}

	recorder := httptest.NewRecorder()
	jsonReq := httptest.NewRequest(http.MethodGet, "/json", nil)
	jsonReq.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(recorder, jsonReq)
	if recorder.Header().Get("Content-Encoding") != "gzip" || recorder.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("kept route headers: encoding=%q vary=%q", recorder.Header().Get("Content-Encoding"), recorder.Header().Get("Vary"))
	}
}
