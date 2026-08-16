package proxy

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

type egressReaderFromSpy struct{ bytes.Buffer }

func (w *egressReaderFromSpy) Header() http.Header { return make(http.Header) }
func (w *egressReaderFromSpy) WriteHeader(int)     {}
func (w *egressReaderFromSpy) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(&w.Buffer, r)
}

func TestMeteredResponseWriterReadFromCountsBytes(t *testing.T) {
	spy := &egressReaderFromSpy{}
	meter := newEgressMeter()
	w := &meteredResponseWriter{ResponseWriter: spy, meter: meter}
	n, err := w.ReadFrom(bytes.NewReader(make([]byte, 8<<20)))
	if err != nil || n != 8<<20 || len(spy.Bytes()) != 8<<20 {
		t.Fatalf("ReadFrom = n=%d err=%v body=%d", n, err, len(spy.Bytes()))
	}
	if got := meter.RateKbps(); got <= 0 {
		t.Fatalf("meter rate = %d, want > 0", got)
	}
}
