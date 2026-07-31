package transfers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
)

const testPourID = "pour"

func TestRegistryLifecycleAndAccounting(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	r := NewWithOptions(Options{Now: func() time.Time { return now }})
	started := now.Add(-time.Minute)
	if err := r.Begin(Transfer{ID: "pour-1", UserID: 7, DownloadID: "download-1", StartedAt: started}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	got := r.Snapshot()
	if len(got) != 1 || got[0].LastServedAt != started || got[0].BytesServed != 0 {
		t.Fatalf("initial snapshot = %+v", got)
	}
	if err := r.AddServedBytes("pour-1", 25); err != nil {
		t.Fatalf("AddServedBytes: %v", err)
	}
	got = r.Snapshot()
	if got[0].BytesServed != 25 || got[0].LastServedAt != now {
		t.Fatalf("accounted snapshot = %+v", got)
	}

	r.End("pour-1")
	r.End("pour-1")
	if got := r.Snapshot(); len(got) != 0 {
		t.Fatalf("snapshot after End = %+v", got)
	}
}

func TestRegistryUnknownAndNonPositiveUpdatesAreNoOps(t *testing.T) {
	r := New()
	if err := r.AddServedBytes("missing", 20); err != nil {
		t.Fatalf("unknown AddServedBytes: %v", err)
	}
	if len(r.Snapshot()) != 0 {
		t.Fatal("unknown update created an entry")
	}
	if err := r.Begin(Transfer{ID: testPourID}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for _, n := range []int64{0, -1, -100} {
		if err := r.AddServedBytes(testPourID, n); err != nil {
			t.Fatalf("AddServedBytes(%d): %v", n, err)
		}
	}
	if got := r.Snapshot()[0].BytesServed; got != 0 {
		t.Fatalf("BytesServed = %d, want 0", got)
	}
}

func TestRegistryConcurrentPoursForSameDownloadDoNotCollide(t *testing.T) {
	r := New()
	for _, id := range []string{"range-a", "range-b"} {
		if err := r.Begin(Transfer{ID: id, DownloadID: "same-download"}); err != nil {
			t.Fatalf("Begin(%s): %v", id, err)
		}
	}
	_ = r.AddServedBytes("range-a", 10)
	_ = r.AddServedBytes("range-b", 20)
	got := r.Snapshot()
	if len(got) != 2 || got[0].ID == got[1].ID {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestRegistryConcurrentLifecycle(t *testing.T) {
	unlimited := 0
	r := NewWithOptions(Options{MaxEntries: 256, MaxPerUser: &unlimited})
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := time.Unix(0, int64(i)+1).String()
			if err := r.Begin(Transfer{ID: id}); err != nil {
				t.Errorf("Begin(%d): %v", i, err)
				return
			}
			for j := 0; j < 10; j++ {
				_ = r.AddServedBytes(id, 1)
				_ = r.Snapshot()
			}
			r.End(id)
		}(i)
	}
	wg.Wait()
	if got := r.Snapshot(); len(got) != 0 {
		t.Fatalf("leaked transfers = %d", len(got))
	}
}

func TestRegistryCapHoldsUnderRacingBegin(t *testing.T) {
	const cap = 7
	unlimited := 0
	r := NewWithOptions(Options{MaxEntries: cap, MaxPerUser: &unlimited})
	var wg sync.WaitGroup
	var accepted atomic.Int64
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := r.Begin(Transfer{ID: time.Unix(0, int64(i)+1).String()})
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrRegistryFull) {
				t.Errorf("Begin(%d): %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if got := accepted.Load(); got != cap {
		t.Fatalf("accepted = %d, want %d", got, cap)
	}
	if got := len(r.Snapshot()); got != cap {
		t.Fatalf("snapshot size = %d, want %d", got, cap)
	}
}

func TestRegistryPerUserLimitAndAnnotation(t *testing.T) {
	now := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	maxPerUser := 2
	logs := &countingLogHandler{}
	r := NewWithOptions(Options{
		MaxPerUser: &maxPerUser,
		Now:        func() time.Time { return now },
		Logger:     slog.New(logs),
	})
	for _, id := range []string{"one", "two"} {
		if err := r.Begin(Transfer{ID: id, UserID: 7, Route: "native_download"}); err != nil {
			t.Fatalf("Begin(%q): %v", id, err)
		}
	}
	if err := r.Begin(Transfer{ID: "over", UserID: 7, Route: "native_download"}); !errors.Is(err, ErrUserTransferLimit) {
		t.Fatalf("Begin at per-user cap = %v, want ErrUserTransferLimit", err)
	}
	if err := r.Begin(Transfer{ID: "other", UserID: 8}); err != nil {
		t.Fatalf("other user Begin: %v", err)
	}

	r.End("never-began")
	if err := r.Begin(Transfer{ID: "still-over", UserID: 7}); !errors.Is(err, ErrUserTransferLimit) {
		t.Fatalf("Begin after unknown End = %v, want ErrUserTransferLimit", err)
	}
	if got := logs.count.Load(); got != 1 {
		t.Fatalf("warnings before interval = %d, want 1", got)
	}
	now = now.Add(fullWarningInterval)
	if err := r.Begin(Transfer{ID: "warn-again", UserID: 7}); !errors.Is(err, ErrUserTransferLimit) {
		t.Fatalf("Begin after warning interval = %v, want ErrUserTransferLimit", err)
	}
	if got := logs.count.Load(); got != 2 {
		t.Fatalf("warnings after interval = %d, want 2", got)
	}

	r.End("one")
	if err := r.Begin(Transfer{ID: "replacement", UserID: 7}); err != nil {
		t.Fatalf("Begin after End: %v", err)
	}
	r.Annotate("replacement", " download ", 42)
	r.Annotate("unknown", "ignored", 99)
	var replacement Transfer
	for _, transfer := range r.Snapshot() {
		if transfer.ID == "replacement" {
			replacement = transfer
		}
	}
	if replacement.DownloadID != "download" || replacement.MediaFileID != 42 {
		t.Fatalf("annotated transfer = %+v", replacement)
	}
	if len(r.Snapshot()) != 3 {
		t.Fatalf("Annotate unknown changed registry: %+v", r.Snapshot())
	}

	r.End("two")
	r.End("replacement")
	r.End("other")
	if len(r.perUser) != 0 {
		t.Fatalf("perUser retained zero-count state: %+v", r.perUser)
	}

	unlimited := 0
	unlimitedRegistry := NewWithOptions(Options{MaxEntries: 30, MaxPerUser: &unlimited})
	for i := 0; i < 30; i++ {
		if err := unlimitedRegistry.Begin(Transfer{ID: strconv.Itoa(i), UserID: 1}); err != nil {
			t.Fatalf("explicit unlimited Begin(%d): %v", i, err)
		}
	}

	defaultRegistry := New()
	if defaultRegistry.MaxPerUser() != defaultMaxPerUser {
		t.Fatalf("unset MaxPerUser = %d, want %d", defaultRegistry.MaxPerUser(), defaultMaxPerUser)
	}
	for i := 0; i < defaultMaxPerUser; i++ {
		if err := defaultRegistry.Begin(Transfer{ID: strconv.Itoa(i), UserID: 1}); err != nil {
			t.Fatalf("default Begin(%d): %v", i, err)
		}
	}
	if err := defaultRegistry.Begin(Transfer{ID: "default-over", UserID: 1}); !errors.Is(err, ErrUserTransferLimit) {
		t.Fatalf("Begin at default cap = %v, want ErrUserTransferLimit", err)
	}
}

func TestParseMaxPerUser(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{raw: "", want: defaultMaxPerUser},
		{raw: "0", want: 0},
		{raw: "48", want: 48},
		{raw: "-1", wantErr: true},
		{raw: "10001", wantErr: true},
		{raw: "many", wantErr: true},
	} {
		got, err := ParseMaxPerUser(tc.raw)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("ParseMaxPerUser(%q) = %d, %v; want %d, error=%v", tc.raw, got, err, tc.want, tc.wantErr)
		}
	}
}

type countingLogHandler struct {
	count atomic.Int64
}

func (h *countingLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countingLogHandler) Handle(context.Context, slog.Record) error {
	h.count.Add(1)
	return nil
}
func (h *countingLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countingLogHandler) WithGroup(string) slog.Handler      { return h }

func TestRegistryClampsAndNormalizesRequestStrings(t *testing.T) {
	r := New()
	long := strings.Repeat("界", 400)
	if err := r.Begin(Transfer{
		ID:         " pour ",
		DownloadID: " " + long + " ",
		ProfileID:  long,
		Route:      long,
		ClientIP:   long,
		ClientName: long,
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	got := r.Snapshot()[0]
	if got.ID != testPourID {
		t.Fatalf("ID = %q", got.ID)
	}
	for name, tc := range map[string]struct {
		value string
		max   int
	}{
		"download": {got.DownloadID, maxDownloadIDLength},
		"profile":  {got.ProfileID, maxProfileIDLength},
		"route":    {got.Route, maxRouteLength},
		"ip":       {got.ClientIP, maxClientIPLength},
		"client":   {got.ClientName, maxClientNameLength},
	} {
		if n := len([]rune(tc.value)); n != tc.max {
			t.Errorf("%s length = %d, want %d", name, n, tc.max)
		}
	}
}

func TestMeterCloseFlushesSubMiBTailBeforeRegistryEnd(t *testing.T) {
	var accountingCalls atomic.Int64
	r := NewWithOptions(Options{Now: func() time.Time {
		accountingCalls.Add(1)
		return time.Now()
	}})
	if err := r.Begin(Transfer{ID: testPourID, StartedAt: time.Now()}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	func() {
		defer r.End(testPourID) // registered first, runs last
		metered := playback.NewSessionMeteredWriter(httptest.NewRecorder(), r, testPourID)
		defer func() { _ = metered.Close() }() // registered second, runs first
		if _, err := metered.Write([]byte("smaller than one MiB")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}()

	if got := accountingCalls.Load(); got != 1 {
		t.Fatalf("accounting calls = %d, want 1; tail was not flushed before End", got)
	}
}

func TestDirectPlayByteAccounting(t *testing.T) {
	payload := []byte("0123456789abcdef")
	path := filepath.Join(t.TempDir(), "sample.mp4")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tests := []struct {
		name       string
		method     string
		headers    map[string]string
		writer     func() http.ResponseWriter
		wantBytes  int64
		wantStatus int
	}{
		{name: "full body", method: http.MethodGet, writer: func() http.ResponseWriter { return httptest.NewRecorder() }, wantBytes: int64(len(payload)), wantStatus: http.StatusOK},
		{name: "range", method: http.MethodGet, headers: map[string]string{"Range": "bytes=2-5"}, writer: func() http.ResponseWriter { return httptest.NewRecorder() }, wantBytes: 4, wantStatus: http.StatusPartialContent},
		{name: "head", method: http.MethodHead, writer: func() http.ResponseWriter { return httptest.NewRecorder() }, wantStatus: http.StatusOK},
		{name: "conditional", method: http.MethodGet, headers: map[string]string{"If-Modified-Since": time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)}, writer: func() http.ResponseWriter { return httptest.NewRecorder() }, wantStatus: http.StatusNotModified},
		{name: "client disconnect", method: http.MethodGet, writer: func() http.ResponseWriter { return &disconnectWriter{limit: 5, header: make(http.Header)} }, wantBytes: 5, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New()
			if err := r.Begin(Transfer{ID: testPourID}); err != nil {
				t.Fatalf("Begin: %v", err)
			}
			base := tt.writer()
			metered := playback.NewSessionMeteredWriter(base, r, testPourID)
			req := httptest.NewRequest(tt.method, "/file", nil)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			if err := playback.ServeDirectPlay(metered, req, path); err != nil {
				t.Fatalf("ServeDirectPlay: %v", err)
			}
			if err := metered.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			got := r.Snapshot()[0]
			if got.BytesServed != tt.wantBytes {
				t.Fatalf("BytesServed = %d, want %d", got.BytesServed, tt.wantBytes)
			}
			switch w := base.(type) {
			case *httptest.ResponseRecorder:
				if w.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
				}
			case *disconnectWriter:
				if w.status != tt.wantStatus {
					t.Fatalf("status = %d, want %d", w.status, tt.wantStatus)
				}
			}
		})
	}
}

type disconnectWriter struct {
	header http.Header
	status int
	limit  int
}

func (w *disconnectWriter) Header() http.Header { return w.header }

func (w *disconnectWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *disconnectWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n := min(len(p), w.limit)
	w.limit -= n
	if n < len(p) || w.limit == 0 {
		return n, io.ErrClosedPipe
	}
	return n, nil
}
