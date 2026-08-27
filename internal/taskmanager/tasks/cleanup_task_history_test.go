package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/taskmanager"
)

type fakeTaskHistoryPruner struct {
	result     taskmanager.HistoryPruneResult
	err        error
	calls      int
	kept       int
	cutoff     time.Time
	batchSize  int
	maxBatches int
}

func (f *fakeTaskHistoryPruner) Prune(
	_ context.Context,
	keepPerTask int,
	cutoff time.Time,
	batchSize int,
	maxBatches int,
) (taskmanager.HistoryPruneResult, error) {
	f.calls++
	f.kept = keepPerTask
	f.cutoff = cutoff
	f.batchSize = batchSize
	f.maxBatches = maxBatches
	return f.result, f.err
}

type taskHistoryCleanupProgress struct {
	reports []string
	result  json.RawMessage
}

func (p *taskHistoryCleanupProgress) Report(_ float64, message string) {
	p.reports = append(p.reports, message)
}

func (p *taskHistoryCleanupProgress) SetResultData(data json.RawMessage) {
	p.result = append(p.result[:0], data...)
}

func TestTaskHistoryCleanupTask(t *testing.T) {
	pruner := &fakeTaskHistoryPruner{
		result: taskmanager.HistoryPruneResult{Deleted: 10017},
	}
	task := NewTaskHistoryCleanupTask(pruner)

	if task.Key() != "cleanup_task_history" {
		t.Fatalf("Key() = %q, want cleanup_task_history", task.Key())
	}
	if task.Category() != taskmanager.TaskCategorySystem {
		t.Fatalf("Category() = %q, want %q", task.Category(), taskmanager.TaskCategorySystem)
	}
	if task.IsHidden() {
		t.Fatal("IsHidden() = true, want false")
	}
	wantTriggers := []taskmanager.TriggerConfig{
		{Type: taskmanager.TriggerTypeStartup},
		{Type: taskmanager.TriggerTypeInterval, IntervalMs: int64((24 * time.Hour) / time.Millisecond)},
	}
	gotTriggers := task.DefaultTriggers()
	if len(gotTriggers) != len(wantTriggers) || gotTriggers[0] != wantTriggers[0] || gotTriggers[1] != wantTriggers[1] {
		t.Fatalf("DefaultTriggers() = %#v, want %#v", gotTriggers, wantTriggers)
	}

	before := time.Now().UTC().Add(-taskHistoryMaxAge)
	progress := &taskHistoryCleanupProgress{}
	if err := task.Execute(context.Background(), progress); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	after := time.Now().UTC().Add(-taskHistoryMaxAge)
	if pruner.calls != 1 {
		t.Fatalf("pruner calls = %d, want 1", pruner.calls)
	}
	if pruner.kept != taskHistoryKeepPerTask {
		t.Fatalf("keepPerTask = %d, want %d", pruner.kept, taskHistoryKeepPerTask)
	}
	if pruner.batchSize != taskHistoryCleanupBatchSize {
		t.Fatalf("batchSize = %d, want %d", pruner.batchSize, taskHistoryCleanupBatchSize)
	}
	if pruner.maxBatches != taskHistoryCleanupMaxBatches {
		t.Fatalf("maxBatches = %d, want %d", pruner.maxBatches, taskHistoryCleanupMaxBatches)
	}
	if pruner.cutoff.Before(before) || pruner.cutoff.After(after) {
		t.Fatalf("cutoff = %v, want between %v and %v", pruner.cutoff, before, after)
	}
	if got := progress.reports[len(progress.reports)-1]; got != "Pruned 10017 task executions" {
		t.Fatalf("last progress report = %q", got)
	}
	var result taskHistoryCleanupResult
	if err := json.Unmarshal(progress.result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Deleted != 10017 || result.LimitReached {
		t.Fatalf("result = %#v, want 10017 deleted without limit", result)
	}
}

func TestTaskHistoryCleanupTaskReturnsPruneError(t *testing.T) {
	wantErr := errors.New("delete failed")
	pruner := &fakeTaskHistoryPruner{
		result: taskmanager.HistoryPruneResult{Deleted: taskHistoryCleanupBatchSize},
		err:    wantErr,
	}
	progress := &taskHistoryCleanupProgress{}

	err := NewTaskHistoryCleanupTask(pruner).Execute(context.Background(), progress)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute error = %v, want %v", err, wantErr)
	}
	if got := progress.reports[len(progress.reports)-1]; !strings.Contains(got, "failed after deleting 10000 executions") {
		t.Fatalf("last progress report = %q", got)
	}
}

func TestTaskHistoryCleanupTaskCapsWorkPerRun(t *testing.T) {
	wantDeleted := int64(taskHistoryCleanupBatchSize * taskHistoryCleanupMaxBatches)
	pruner := &fakeTaskHistoryPruner{
		result: taskmanager.HistoryPruneResult{
			Deleted:      wantDeleted,
			LimitReached: true,
		},
	}
	progress := &taskHistoryCleanupProgress{}

	if err := NewTaskHistoryCleanupTask(pruner).Execute(context.Background(), progress); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if pruner.calls != 1 {
		t.Fatalf("pruner calls = %d, want 1", pruner.calls)
	}
	var result taskHistoryCleanupResult
	if err := json.Unmarshal(progress.result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Deleted != wantDeleted || !result.LimitReached {
		t.Fatalf("result = %#v, want %d deleted with limit reached", result, wantDeleted)
	}
}

func TestTaskHistoryCleanupTaskReportsSkippedLock(t *testing.T) {
	pruner := &fakeTaskHistoryPruner{
		result: taskmanager.HistoryPruneResult{Skipped: true},
	}
	progress := &taskHistoryCleanupProgress{}

	if err := NewTaskHistoryCleanupTask(pruner).Execute(context.Background(), progress); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := progress.reports[len(progress.reports)-1]; got != "Task history cleanup is already running on another node" {
		t.Fatalf("last progress report = %q", got)
	}
	var result taskHistoryCleanupResult
	if err := json.Unmarshal(progress.result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.Skipped {
		t.Fatalf("result = %#v, want skipped", result)
	}
}
