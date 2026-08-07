package adminjob

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanbatch"
	"github.com/Silo-Server/silo-server/internal/scanqueue"
)

const (
	itemRefreshScanTrigger     = scanqueue.TriggerAdminItemRefresh
	libraryRefreshScanTrigger  = scanqueue.TriggerAdminLibraryRefresh
	directScanFinishTimeout    = 10 * time.Second
	directScanHeartbeatEvery   = 10 * time.Second
	directScanHeartbeatTimeout = 5 * time.Second
)

type directScanRunRepository interface {
	Create(ctx context.Context, input scanqueue.CreateInput) (*models.ScanRun, bool, error)
	Start(ctx context.Context, id string) (*models.ScanRun, error)
	TouchHeartbeat(ctx context.Context, id string) error
	Complete(ctx context.Context, id string, result *events.ScanRunResult) (*models.ScanRun, error)
	Fail(ctx context.Context, id string, errorMessage string) (*models.ScanRun, error)
	MarkCancelled(ctx context.Context, id string) (*models.ScanRun, bool, error)
}

func startDirectScanHeartbeat(repo directScanRunRepository, runID string, interval time.Duration) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				touchCtx, touchCancel := context.WithTimeout(ctx, directScanHeartbeatTimeout)
				err := repo.TouchHeartbeat(touchCtx, runID)
				touchCancel()
				if err != nil && !errors.Is(err, context.Canceled) {
					slog.Warn("direct scan heartbeat failed", "scan_id", runID, "error", err)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func beginDirectSubtreeScan(
	ctx context.Context,
	repo directScanRunRepository,
	libraryID int,
	path string,
	trigger string,
) (context.Context, *models.ScanRun, error) {
	if repo == nil {
		return nil, nil, fmt.Errorf("scan run repository is not configured")
	}
	run, created, err := repo.Create(ctx, scanqueue.CreateInput{
		LibraryID: libraryID,
		Mode:      scanqueue.ModeSubtree,
		Path:      path,
		Trigger:   trigger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create scan run: %w", err)
	}
	if !created {
		return nil, nil, fmt.Errorf("scan scope already has active run %s", run.ID)
	}
	runID := run.ID
	run, err = repo.Start(ctx, runID)
	if err != nil {
		startErr := fmt.Errorf("start scan run: %w", err)
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), directScanFinishTimeout)
		defer cancel()
		if _, _, cleanupErr := repo.MarkCancelled(cleanupCtx, runID); cleanupErr != nil {
			return nil, nil, errors.Join(startErr, fmt.Errorf("cancel scan run after start failure: %w", cleanupErr))
		}
		return nil, nil, startErr
	}
	return scanbatch.WithRunID(ctx, run.ID), run, nil
}

func completeDirectScan(ctx context.Context, repo directScanRunRepository, run *models.ScanRun, result *libraryingest.Result) error {
	if run == nil {
		return fmt.Errorf("scan run is missing")
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), directScanFinishTimeout)
	defer cancel()
	if _, err := repo.Complete(finishCtx, run.ID, scanRunResultFromIngest(result)); err != nil {
		return fmt.Errorf("complete scan run: %w", err)
	}
	return nil
}

func failDirectScan(ctx context.Context, repo directScanRunRepository, run *models.ScanRun, scanErr error) error {
	if run == nil || scanErr == nil {
		return scanErr
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), directScanFinishTimeout)
	defer cancel()
	if _, err := repo.Fail(finishCtx, run.ID, scanErr.Error()); err != nil {
		return errors.Join(scanErr, fmt.Errorf("fail scan run: %w", err))
	}
	return scanErr
}

func scanRunResultFromIngest(result *libraryingest.Result) *events.ScanRunResult {
	if result == nil {
		return nil
	}
	out := &events.ScanRunResult{
		MatchedFiles:           result.MatchedFiles,
		RetriedItems:           result.RetriedItems,
		StillUnmatchedWarnings: result.StillUnmatchedWarnings,
	}
	if result.Skipped {
		out.Skipped = 1
	}
	if result.ScanResult != nil {
		out.New = result.ScanResult.New
		out.Updated = result.ScanResult.Updated
		out.Unchanged = result.ScanResult.Unchanged
		out.Missing = result.ScanResult.Missing
		out.MissingSkippedProtected = result.ScanResult.MissingSkippedProtected
		out.FilesDeleted = result.ScanResult.FilesDeleted
		out.MembershipsRemoved = result.ScanResult.MembershipsRemoved
		out.ItemsDeleted = result.ScanResult.ItemsDeleted
		out.Errors = result.ScanResult.Errors
	}
	return out
}
