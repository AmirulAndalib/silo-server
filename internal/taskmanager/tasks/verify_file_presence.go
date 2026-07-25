package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/taskmanager"
)

// defaultVerifyFilePresenceIntervalMs is how often the sweep runs. Fifteen
// minutes is the ceiling on how long a deleted file can keep advertising
// itself as playable when no autoscan source covers the library.
const defaultVerifyFilePresenceIntervalMs int64 = 15 * 60 * 1000

// PresenceVerifier stats a library's cataloged files and marks the vanished
// ones missing. Implemented by *scanner.Scanner.
type PresenceVerifier interface {
	VerifyPresence(ctx context.Context, folder *models.MediaFolder) (scanner.PresenceSweepResult, error)
}

// VerifyFilePresenceTask closes the window between a file being deleted on
// disk and the catalog noticing. Catalog reads hide files with missing_since
// set, so marking is all it takes to stop offering a dead file for playback;
// without this the first thing to notice a deletion is a failed play.
type VerifyFilePresenceTask struct {
	folderRepo ScanFolderRepository
	verifier   PresenceVerifier
	intervalMs int64
}

type verifyFilePresenceResult struct {
	Checked            int `json:"checked"`
	Marked             int `json:"marked"`
	MembershipsRemoved int `json:"memberships_removed"`
	ItemsDeleted       int `json:"items_deleted"`
	FoldersAborted     int `json:"folders_aborted"`
	Errors             int `json:"errors"`
}

// NewVerifyFilePresenceTask builds the sweep task. intervalMs is in
// MILLISECONDS; a non-positive value falls back to the default cadence.
func NewVerifyFilePresenceTask(folderRepo ScanFolderRepository, verifier PresenceVerifier, intervalMs int64) *VerifyFilePresenceTask {
	if intervalMs <= 0 {
		intervalMs = defaultVerifyFilePresenceIntervalMs
	}
	return &VerifyFilePresenceTask{folderRepo: folderRepo, verifier: verifier, intervalMs: intervalMs}
}

func (t *VerifyFilePresenceTask) Key() string  { return "verify_file_presence" }
func (t *VerifyFilePresenceTask) Name() string { return "Verify Media File Presence" }
func (t *VerifyFilePresenceTask) Description() string {
	return "Checks that cataloged media files still exist on disk and hides the ones that were removed, without waiting for a full library scan"
}
func (t *VerifyFilePresenceTask) Category() taskmanager.TaskCategory {
	return taskmanager.TaskCategoryLibrary
}
func (t *VerifyFilePresenceTask) IsHidden() bool { return false }

func (t *VerifyFilePresenceTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return []taskmanager.TriggerConfig{
		{Type: taskmanager.TriggerTypeInterval, IntervalMs: t.intervalMs},
	}
}

func (t *VerifyFilePresenceTask) Execute(ctx context.Context, progress taskmanager.ProgressReporter) error {
	if t == nil || t.verifier == nil || t.folderRepo == nil {
		progress.Report(100, "File presence verification is not configured")
		return nil
	}

	folders, err := t.folderRepo.GetEnabled(ctx)
	if err != nil {
		return fmt.Errorf("listing enabled folders: %w", err)
	}
	if len(folders) == 0 {
		progress.Report(100, "No libraries to verify")
		return nil
	}

	var summary verifyFilePresenceResult
	// Sequential across folders on purpose: VerifyPresence already fans out
	// internally, and libraries commonly share one underlying mount.
	for i, folder := range folders {
		if folder == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		res, verifyErr := t.verifier.VerifyPresence(ctx, folder)
		if verifyErr != nil {
			slog.ErrorContext(ctx, "presence sweep: failed to verify library", "component", "taskmanager",
				"folder_id", folder.ID, "name", folder.Name, "error", verifyErr)
			summary.Errors++
		} else {
			summary.Checked += res.Checked
			summary.Marked += res.Marked
			summary.MembershipsRemoved += res.MembershipsRemoved
			summary.ItemsDeleted += res.ItemsDeleted
			if res.Aborted {
				summary.FoldersAborted++
			}
		}

		progress.Report(float64(i+1)/float64(len(folders))*100,
			fmt.Sprintf("Verified %s (%d/%d libraries)", folder.Name, i+1, len(folders)))
	}

	if data, err := json.Marshal(summary); err == nil {
		progress.SetResultData(data)
	}
	progress.Report(100, fmt.Sprintf("Checked %d files, hid %d removed", summary.Checked, summary.Marked))
	return nil
}
