package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/nodeconfig"
	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

const proxyTrackerTestSecret = "proxy-tracker-test-secret"

func newMountedProxyTestServer(t *testing.T, mediaPath string) (*Server, *deleteCaptureHook, string) {
	t.Helper()
	watcher := nodeconfig.NewWatcher(nil, nil, nil, nodeconfig.BootstrapOverrides{})
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = proxyTrackerTestSecret
	watcher.SetConfigForTest(cfg)
	tracker, redisState := newProxyTestTracker(t)
	server := NewServer(watcher, tracker)
	token, err := streamtoken.Sign(streamtoken.Claims{
		SessionID:     "session-1",
		MediaPath:     mediaPath,
		TranscodeNode: "http://transcode",
		UserID:        7,
		ProfileID:     "profile-1",
		MediaFileID:   42,
	}, proxyTrackerTestSecret, time.Minute)
	if err != nil {
		t.Fatalf("sign stream token: %v", err)
	}
	return server, redisState, token
}

type blockingResponseWriter struct {
	header  http.Header
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingResponseWriter) Header() http.Header { return w.header }
func (w *blockingResponseWriter) WriteHeader(int)     {}
func (w *blockingResponseWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func TestMountedDirectOverlappingRangesKeepSessionUntilFinalRequest(t *testing.T) {
	path := t.TempDir() + "/movie.bin"
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 64<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	server, redisState, token := newMountedProxyTestServer(t, path)
	firstWriter := &blockingResponseWriter{
		header:  make(http.Header),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		req := httptest.NewRequest(http.MethodGet, "/stream/direct/"+token, nil)
		req.Header.Set("Range", "bytes=0-32767")
		server.Handler().ServeHTTP(firstWriter, req)
	}()
	select {
	case <-firstWriter.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first mounted range request did not begin its pour")
	}

	second := httptest.NewRequest(http.MethodGet, "/stream/direct/"+token, nil)
	second.Header.Set("Range", "bytes=32768-65535")
	secondRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusPartialContent {
		t.Fatalf("second range status = %d, want 206", secondRec.Code)
	}
	snapshot := server.tracker.Snapshot()
	if len(snapshot) != 1 || snapshot[0].BytesServed == 0 {
		t.Fatalf("first request lost tracking after second completed: %+v", snapshot)
	}
	key := nodesessions.KeyPrefix + server.tracker.NodeHash() + ":session-1"
	if !redisState.has(key) {
		t.Fatal("Redis key was deleted while the first range was still pouring")
	}

	close(firstWriter.release)
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first range request did not complete")
	}
	if got := server.tracker.Snapshot(); len(got) != 0 {
		t.Fatalf("final request release left session behind: %+v", got)
	}
}

func TestMountedDirectReleasesLeaseOnEveryCompletionPath(t *testing.T) {
	path := t.TempDir() + "/movie.bin"
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, _, token := newMountedProxyTestServer(t, path)

	t.Run("head", func(t *testing.T) {
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/stream/direct/"+token, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		if got := server.tracker.Snapshot(); len(got) != 0 {
			t.Fatalf("HEAD leaked lease: %+v", got)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		missingServer, _, missingToken := newMountedProxyTestServer(t, path+".missing")
		rec := httptest.NewRecorder()
		missingServer.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream/direct/"+missingToken, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d", rec.Code)
		}
		if got := missingServer.tracker.Snapshot(); len(got) != 0 {
			t.Fatalf("missing-file completion leaked lease: %+v", got)
		}
	})

	t.Run("client cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodGet, "/stream/direct/"+token, nil).WithContext(ctx)
		cancel()
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		if got := server.tracker.Snapshot(); len(got) != 0 {
			t.Fatalf("canceled request leaked lease: %+v", got)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type limitedFailureWriter struct {
	header    http.Header
	remaining int
}

func (w *limitedFailureWriter) Header() http.Header { return w.header }
func (w *limitedFailureWriter) WriteHeader(int)     {}
func (w *limitedFailureWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, errors.New("client disconnected")
	}
	n := min(len(p), w.remaining)
	w.remaining -= n
	return n, errors.New("client disconnected")
}

func TestMountedTranscodeLivenessRequiresSuccessfulServedBytes(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		status       int
		body         string
		transportErr error
		writer       http.ResponseWriter
		wantBytes    int64
		wantServed   bool
	}{
		{name: "200 bytes", method: http.MethodGet, status: http.StatusOK, body: "manifest", wantBytes: 8, wantServed: true},
		{name: "206 bytes", method: http.MethodGet, status: http.StatusPartialContent, body: "segment", wantBytes: 7, wantServed: true},
		{name: "head zero bytes", method: http.MethodHead, status: http.StatusOK},
		{name: "404 error body", method: http.MethodGet, status: http.StatusNotFound, body: "not found"},
		{name: "upstream failure", method: http.MethodGet, transportErr: errors.New("dial failed")},
		{name: "client write failure", method: http.MethodGet, status: http.StatusOK, body: "abcdefgh", writer: &limitedFailureWriter{header: make(http.Header), remaining: 3}, wantBytes: 3, wantServed: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, _, token := newMountedProxyTestServer(t, "")
			server.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if tc.transportErr != nil {
					return nil, tc.transportErr
				}
				return &http.Response{
					StatusCode: tc.status,
					Header:     make(http.Header),
					Body:       io.NopCloser(bytes.NewBufferString(tc.body)),
				}, nil
			})}
			writer := tc.writer
			if writer == nil {
				writer = httptest.NewRecorder()
			}
			server.Handler().ServeHTTP(writer, httptest.NewRequest(tc.method, "/stream/transcode/"+token+"/master.m3u8", nil))

			got := server.tracker.Snapshot()
			if len(got) != 1 {
				t.Fatalf("snapshot = %+v, want visible ephemeral record", got)
			}
			if got[0].BytesServed != tc.wantBytes {
				t.Fatalf("BytesServed = %d, want %d", got[0].BytesServed, tc.wantBytes)
			}
			if (got[0].LastServedAt != "") != tc.wantServed {
				t.Fatalf("LastServedAt = %q, want served=%v", got[0].LastServedAt, tc.wantServed)
			}
		})
	}
}
