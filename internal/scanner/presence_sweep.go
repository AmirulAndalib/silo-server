package scanner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

// presenceSweepWorkers bounds the concurrent stat calls. A sweep is pure
// syscall work with no ffprobe or hashing, but library roots are frequently
// network mounts where a wide fan-out buys latency at the cost of hammering
// the server, so keep the fan-out modest.
const presenceSweepWorkers = 16

// presenceSweepAbortFraction is the share of a library's live files that may
// vanish in a single sweep before the sweep refuses to act on the result.
//
// The sweep runs unattended every few minutes and only ever observes the
// filesystem through stat(2), so it cannot distinguish "the operator deleted
// half the library" from "a mount dropped out but left a populated-looking
// directory behind". The root probes upstream catch the common shapes of that
// fault; this is the backstop for the ones they miss. Bulk removals are real,
// but they are also the case where being wrong is most expensive, so the sweep
// defers them to a full scan rather than guessing.
const presenceSweepAbortFraction = 0.5

// presenceSweepAbortFloor is the number of vanished files below which the
// fraction check does not apply. Without it a library with two files would
// abort on a single legitimate deletion.
const presenceSweepAbortFloor = 20

// PresenceSweepResult reports what one folder's sweep observed.
type PresenceSweepResult struct {
	Checked  int
	Vanished int
	Marked   int
	// Skipped counts live files under a protected (unreachable or
	// suspect-empty) root, which the sweep does not stat at all.
	Skipped int
	// Aborted is set when the vanished count tripped the bulk-removal
	// backstop and nothing was marked for this folder.
	Aborted bool
	// MembershipsRemoved and ItemsDeleted come from the reconcile pass that
	// follows a sweep that marked anything.
	MembershipsRemoved int
	ItemsDeleted       int
}

// VerifyPresence stats every file the catalog believes is on disk for one
// folder and marks the vanished ones missing, then reconciles library
// membership so titles left with no playable file drop out of the catalog.
//
// This exists because a deleted file is otherwise invisible to the server
// until the next full library scan: catalog reads all filter on
// missing_since, so the row keeps advertising a playable version, and the
// first thing to notice is a user pressing play. A sweep is cheap enough
// (one stat per live file, no probing) to run on a short interval, which
// closes that window from a scan interval down to the sweep interval.
//
// It deliberately does not empty trash. Deciding that a row is gone for good
// is the full scan's job, which has walked the tree and knows what is
// actually there; the sweep only knows that specific paths did not resolve.
func (s *Scanner) VerifyPresence(ctx context.Context, folder *models.MediaFolder) (PresenceSweepResult, error) {
	var result PresenceSweepResult
	if s == nil || s.fileRepo == nil || folder == nil {
		return result, fmt.Errorf("verify presence: scanner not configured")
	}

	// Protect files under roots that are unreachable or suspect-empty: those
	// files have not been removed, their storage is just not answering, and
	// stat would report every one of them as gone.
	configuredPaths, err := s.configuredFolderPaths(ctx, folder)
	if err != nil {
		return result, err
	}
	configuredRoots := cleanScanRoots(configuredPaths)
	protectedRoots := probeUnreachableRoots(ctx, folder.ID, configuredRoots)
	suspectRoots, err := s.suspectEmptyRoots(ctx, folder.ID, configuredRoots, protectedRoots)
	if err != nil {
		return result, err
	}
	protectedRoots = append(protectedRoots, suspectRoots...)

	refs, err := s.fileRepo.ListPresentByFolder(ctx, folder.ID)
	if err != nil {
		return result, err
	}
	if len(refs) == 0 {
		return result, nil
	}

	candidates := make([]PresentFileRef, 0, len(refs))
	for _, ref := range refs {
		if len(protectedRoots) > 0 && pathWithinAnyRoot(ref.Path, protectedRoots) {
			result.Skipped++
			continue
		}
		candidates = append(candidates, ref)
	}
	result.Checked = len(candidates)
	if len(candidates) == 0 {
		return result, nil
	}

	vanished, err := statVanished(ctx, candidates)
	if err != nil {
		return result, err
	}
	result.Vanished = len(vanished)
	if len(vanished) == 0 {
		return result, nil
	}

	if len(vanished) >= presenceSweepAbortFloor &&
		float64(len(vanished)) >= float64(result.Checked)*presenceSweepAbortFraction {
		result.Aborted = true
		slog.WarnContext(ctx, "scanner: presence sweep saw a bulk disappearance; deferring to a full scan",
			"component", "scanner",
			"folder_id", folder.ID,
			"checked", result.Checked,
			"vanished", result.Vanished,
		)
		return result, nil
	}

	marked, err := s.fileRepo.MarkMissingByIDs(ctx, vanished, time.Now().UTC())
	if err != nil {
		return result, err
	}
	result.Marked = marked
	if marked == 0 {
		return result, nil
	}

	removedMemberships, deletedItems, orphanedImageDirs, err := s.reconcileLibraryMemberships(ctx, folder.ID, protectedRoots)
	if err != nil {
		return result, fmt.Errorf("reconciling library membership for folder %d: %w", folder.ID, err)
	}
	result.MembershipsRemoved = removedMemberships
	result.ItemsDeleted = deletedItems

	if s.s3Client != nil && len(orphanedImageDirs) > 0 {
		bucket := s.s3Client.Bucket()
		for _, dir := range orphanedImageDirs {
			_, _ = s.s3Client.DeletePrefix(ctx, bucket, dir)
		}
	}

	slog.InfoContext(ctx, "scanner: presence sweep marked files missing", "component", "scanner",
		"folder_id", folder.ID,
		"checked", result.Checked,
		"marked", result.Marked,
		"memberships_removed", result.MembershipsRemoved,
		"items_deleted", result.ItemsDeleted,
	)
	return result, nil
}

// statVanished returns the ids of refs whose path no longer exists. Only
// os.ErrNotExist counts: a permission error, a timing-out mount, or any other
// fault means the file's state is unknown, and treating unknown as gone is
// what the sweep must never do.
func statVanished(ctx context.Context, refs []PresentFileRef) ([]int, error) {
	workers := presenceSweepWorkers
	if len(refs) < workers {
		workers = len(refs)
	}

	var (
		mu       sync.Mutex
		vanished []int
	)
	jobs := make(chan PresentFileRef)
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range jobs {
				if _, err := os.Stat(ref.Path); err == nil || !errors.Is(err, os.ErrNotExist) {
					continue
				}
				mu.Lock()
				vanished = append(vanished, ref.ID)
				mu.Unlock()
			}
		}()
	}

	var walkErr error
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			walkErr = err
			break
		}
		jobs <- ref
	}
	close(jobs)
	wg.Wait()
	if walkErr != nil {
		return nil, walkErr
	}
	return vanished, nil
}
