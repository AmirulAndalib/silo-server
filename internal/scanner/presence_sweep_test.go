package scanner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestStatVanishedReportsOnlyAbsentFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	present := filepath.Join(dir, "present.mkv")
	if err := os.WriteFile(present, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing present file: %v", err)
	}

	refs := []PresentFileRef{
		{ID: 1, Path: present},
		{ID: 2, Path: filepath.Join(dir, "gone.mkv")},
		{ID: 3, Path: filepath.Join(dir, "also-gone.mkv")},
	}

	vanished, err := statVanished(context.Background(), refs)
	if err != nil {
		t.Fatalf("statVanished: %v", err)
	}
	slices.Sort(vanished)
	if !slices.Equal(vanished, []int{2, 3}) {
		t.Fatalf("vanished = %v, want [2 3]", vanished)
	}
}

// A path whose parent directory is unreadable stats with EACCES, not ENOENT.
// The sweep must leave those files alone: unknown state is not removal.
func TestStatVanishedIgnoresNonNotExistErrors(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not deny access")
	}

	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatalf("creating locked dir: %v", err)
	}
	target := filepath.Join(locked, "movie.mkv")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing target: %v", err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	vanished, err := statVanished(context.Background(), []PresentFileRef{{ID: 1, Path: target}})
	if err != nil {
		t.Fatalf("statVanished: %v", err)
	}
	if len(vanished) != 0 {
		t.Fatalf("vanished = %v, want empty: a permission error must not count as removal", vanished)
	}
}

func TestStatVanishedHandlesEmptyInput(t *testing.T) {
	t.Parallel()

	vanished, err := statVanished(context.Background(), nil)
	if err != nil {
		t.Fatalf("statVanished: %v", err)
	}
	if len(vanished) != 0 {
		t.Fatalf("vanished = %v, want empty", vanished)
	}
}

func TestStatVanishedStopsOnCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	refs := make([]PresentFileRef, 0, 64)
	for i := range 64 {
		refs = append(refs, PresentFileRef{ID: i + 1, Path: filepath.Join(t.TempDir(), "gone.mkv")})
	}

	if _, err := statVanished(ctx, refs); err == nil {
		t.Fatal("statVanished returned nil error for a cancelled context")
	}
}

// The bulk-removal backstop is the difference between "a few files were
// deleted" and "the storage went away"; assert the exact boundary so a change
// to either constant is deliberate.
func TestPresenceSweepAbortThreshold(t *testing.T) {
	t.Parallel()

	aborts := func(checked, vanished int) bool {
		return vanished >= presenceSweepAbortFloor &&
			float64(vanished) >= float64(checked)*presenceSweepAbortFraction
	}

	cases := []struct {
		name              string
		checked, vanished int
		want              bool
	}{
		{"single deletion in a large library", 5000, 1, false},
		{"routine upgrade churn", 5000, 40, false},
		{"small library emptied stays under the floor", 10, 10, false},
		{"half a large library vanishing", 5000, 2500, true},
		{"just under the floor", 40, 19, false},
		{"at the floor and at the fraction", 40, 20, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := aborts(tc.checked, tc.vanished); got != tc.want {
				t.Fatalf("aborts(checked=%d, vanished=%d) = %v, want %v",
					tc.checked, tc.vanished, got, tc.want)
			}
		})
	}
}

func TestVerifyPresenceRequiresConfiguredScanner(t *testing.T) {
	t.Parallel()

	var s *Scanner
	if _, err := s.VerifyPresence(context.Background(), nil); err == nil {
		t.Fatal("VerifyPresence on a nil scanner returned nil error")
	}
}
