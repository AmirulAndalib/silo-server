package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/taskmanager"
)

const (
	taskHistoryKeepPerTask       = 1000
	taskHistoryMaxAge            = 30 * 24 * time.Hour
	taskHistoryCleanupBatchSize  = 10000
	taskHistoryCleanupMaxBatches = 100
)

// TaskHistoryPruner deletes a bounded amount of task execution history.
type TaskHistoryPruner interface {
	Prune(
		ctx context.Context,
		keepPerTask int,
		cutoff time.Time,
		batchSize int,
		maxBatches int,
	) (taskmanager.HistoryPruneResult, error)
}

// TaskHistoryCleanupTask prunes old task execution history.
type TaskHistoryCleanupTask struct {
	pruner TaskHistoryPruner
}

type taskHistoryCleanupResult struct {
	Deleted      int64 `json:"deleted"`
	LimitReached bool  `json:"limit_reached"`
	Skipped      bool  `json:"skipped"`
}

// NewTaskHistoryCleanupTask creates a scheduled task for task history retention.
func NewTaskHistoryCleanupTask(pruner TaskHistoryPruner) *TaskHistoryCleanupTask {
	return &TaskHistoryCleanupTask{pruner: pruner}
}

func (t *TaskHistoryCleanupTask) Key() string  { return "cleanup_task_history" }
func (t *TaskHistoryCleanupTask) Name() string { return "Cleanup Task History" }
func (t *TaskHistoryCleanupTask) Description() string {
	return "Prunes old background task execution history"
}
func (t *TaskHistoryCleanupTask) Category() taskmanager.TaskCategory {
	return taskmanager.TaskCategorySystem
}
func (t *TaskHistoryCleanupTask) IsHidden() bool { return false }

func (t *TaskHistoryCleanupTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return []taskmanager.TriggerConfig{
		{Type: taskmanager.TriggerTypeStartup},
		{Type: taskmanager.TriggerTypeInterval, IntervalMs: int64((24 * time.Hour) / time.Millisecond)},
	}
}

func (t *TaskHistoryCleanupTask) Execute(ctx context.Context, progress taskmanager.ProgressReporter) error {
	progress.Report(0, "Pruning task execution history")
	cutoff := time.Now().UTC().Add(-taskHistoryMaxAge)
	result, err := t.pruner.Prune(
		ctx,
		taskHistoryKeepPerTask,
		cutoff,
		taskHistoryCleanupBatchSize,
		taskHistoryCleanupMaxBatches,
	)
	resultData := taskHistoryCleanupResult{
		Deleted:      result.Deleted,
		LimitReached: result.LimitReached,
		Skipped:      result.Skipped,
	}
	setTaskHistoryCleanupResult(progress, resultData)
	if err != nil {
		slog.WarnContext(ctx, "task history cleanup failed", "component", "taskmanager", "deleted", result.Deleted, "error", err)
		progress.Report(100, fmt.Sprintf("Task history cleanup failed after deleting %d executions", result.Deleted))
		return err
	}
	if result.Skipped {
		progress.Report(100, "Task history cleanup is already running on another node")
		return nil
	}
	if result.LimitReached {
		slog.WarnContext(ctx, "task history cleanup reached per-run limit", "component", "taskmanager", "deleted", result.Deleted)
		progress.Report(100, fmt.Sprintf(
			"Pruned %d task executions; remaining history will be pruned on the next run",
			result.Deleted,
		))
		return nil
	}
	if result.Deleted > 0 {
		slog.InfoContext(ctx, "task history cleanup completed", "component", "taskmanager", "deleted", result.Deleted)
	}
	progress.Report(100, fmt.Sprintf("Pruned %d task executions", result.Deleted))
	return nil
}

func setTaskHistoryCleanupResult(progress taskmanager.ProgressReporter, result taskHistoryCleanupResult) {
	if progress == nil {
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		return
	}
	progress.SetResultData(data)
}
