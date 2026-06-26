package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// browseNames runs HandleBrowse against the given path and returns the names of
// the entries reported in the response. It fails the test on any non-200 status.
func browseNames(t *testing.T, path string) []string {
	t.Helper()

	h := NewFilesystemHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/filesystem/browse?path="+path, nil)
	q := req.URL.Query()
	q.Set("path", path)
	req.URL.RawQuery = q.Encode()

	rec := httptest.NewRecorder()
	h.HandleBrowse(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleBrowse(%q) status = %d, want 200; body=%s", path, rec.Code, rec.Body.String())
	}

	var resp filesystemBrowseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}

	names := make([]string, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		names = append(names, e.Name)
	}
	return names
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestHandleBrowseFollowsSymlinkedDirectories is the regression test for #208:
// a folder whose children include a symlink pointing at a real directory must
// list that symlink in the picker, while symlinks to files and dangling
// symlinks must be excluded.
func TestHandleBrowseFollowsSymlinkedDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}

	root := t.TempDir()

	// Real subdirectory (control — already worked).
	if err := os.Mkdir(filepath.Join(root, "real-dir"), 0o755); err != nil {
		t.Fatalf("mkdir real-dir: %v", err)
	}

	// A regular file (must be excluded).
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Target directory living outside the browsed root, then a symlink to it —
	// this mirrors the issue's bind-mounted symlinks into a FUSE mount.
	target := filepath.Join(t.TempDir(), "Africa (2013)")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "symlink-dir")); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}

	// Symlink to a file (must be excluded — it is not a directory).
	fileTarget := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(fileTarget, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file target: %v", err)
	}
	if err := os.Symlink(fileTarget, filepath.Join(root, "symlink-file")); err != nil {
		t.Fatalf("symlink file: %v", err)
	}

	// Dangling symlink (must be excluded — os.Stat fails).
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), filepath.Join(root, "dangling")); err != nil {
		t.Fatalf("symlink dangling: %v", err)
	}

	names := browseNames(t, root)

	if !contains(names, "real-dir") {
		t.Errorf("real-dir missing from browse entries: %v", names)
	}
	if !contains(names, "symlink-dir") {
		t.Errorf("symlink-dir (symlink to a directory) missing from browse entries: %v", names)
	}
	if contains(names, "file.txt") {
		t.Errorf("file.txt should not appear in folder browse entries: %v", names)
	}
	if contains(names, "symlink-file") {
		t.Errorf("symlink-file (symlink to a file) should not appear in folder browse entries: %v", names)
	}
	if contains(names, "dangling") {
		t.Errorf("dangling symlink should not appear in folder browse entries: %v", names)
	}
}
