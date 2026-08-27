package repository

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func taskHistoryTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(adminPool.Close)

	schema := fmt.Sprintf("task_history_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	})
	if _, err := adminPool.Exec(ctx, `
		CREATE TABLE `+quotedSchema+`.task_executions (
			id BIGSERIAL PRIMARY KEY,
			task_key TEXT NOT NULL,
			started_at TIMESTAMPTZ NOT NULL,
			completed_at TIMESTAMPTZ NOT NULL,
			status TEXT NOT NULL,
			error_message TEXT,
			result_data JSONB,
			duration_ms BIGINT NOT NULL
		)`); err != nil {
		t.Fatalf("create task execution table: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		CREATE INDEX idx_task_executions_key_completed
		ON `+quotedSchema+`.task_executions (task_key, completed_at DESC)`); err != nil {
		t.Fatalf("create task execution index: %v", err)
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = map[string]string{}
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect isolated test schema: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func insertTaskExecution(t *testing.T, pool *pgxpool.Pool, taskKey string, completedAt time.Time) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO task_executions (
			task_key, started_at, completed_at, status, duration_ms
		) VALUES ($1, $2, $2, 'completed', 0)
		RETURNING id`, taskKey, completedAt).Scan(&id)
	if err != nil {
		t.Fatalf("insert task execution: %v", err)
	}
	return id
}

func remainingTaskExecutionIDs(t *testing.T, pool *pgxpool.Pool, taskKey string) []int64 {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT id FROM task_executions WHERE task_key = $1 ORDER BY id`, taskKey)
	if err != nil {
		t.Fatalf("query remaining task executions: %v", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan task execution ID: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate task execution IDs: %v", err)
	}
	return ids
}

func TestPruneTaskHistoryBatch(t *testing.T) {
	t.Run("keeps newest executions per task", func(t *testing.T) {
		pool := taskHistoryTestPool(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		now := time.Now().UTC().Truncate(time.Microsecond)
		var ids []int64
		for i := range 5 {
			ids = append(ids, insertTaskExecution(t, pool, "count-capped", now.Add(time.Duration(i)*time.Minute)))
		}

		deleted, err := pruneTaskHistoryBatch(ctx, pool, 2, now.Add(-24*time.Hour), 100)
		if err != nil {
			t.Fatalf("PruneBatch: %v", err)
		}
		if deleted != 3 {
			t.Fatalf("deleted = %d, want 3", deleted)
		}
		got := remainingTaskExecutionIDs(t, pool, "count-capped")
		want := ids[len(ids)-2:]
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("remaining IDs = %v, want %v", got, want)
		}
	})

	t.Run("age pruning always preserves newest execution", func(t *testing.T) {
		pool := taskHistoryTestPool(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		now := time.Now().UTC().Truncate(time.Microsecond)
		olderID := insertTaskExecution(t, pool, "age-capped", now.Add(-48*time.Hour))
		newestID := insertTaskExecution(t, pool, "age-capped", now.Add(-47*time.Hour))

		deleted, err := pruneTaskHistoryBatch(ctx, pool, 100, now.Add(-24*time.Hour), 100)
		if err != nil {
			t.Fatalf("PruneBatch: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("deleted = %d, want 1", deleted)
		}
		got := remainingTaskExecutionIDs(t, pool, "age-capped")
		if fmt.Sprint(got) != fmt.Sprint([]int64{newestID}) {
			t.Fatalf("remaining IDs = %v, want [%d]; older ID was %d", got, newestID, olderID)
		}
	})

	t.Run("uses ID to break completion time ties", func(t *testing.T) {
		pool := taskHistoryTestPool(t)
		repo := NewPgExecutionRepository(pool)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		now := time.Now().UTC().Truncate(time.Microsecond)
		firstID := insertTaskExecution(t, pool, "tied", now)
		newestID := insertTaskExecution(t, pool, "tied", now)

		deleted, err := pruneTaskHistoryBatch(ctx, pool, 1, now.Add(-24*time.Hour), 100)
		if err != nil {
			t.Fatalf("PruneBatch: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("deleted = %d, want 1", deleted)
		}
		got := remainingTaskExecutionIDs(t, pool, "tied")
		if fmt.Sprint(got) != fmt.Sprint([]int64{newestID}) {
			t.Fatalf("remaining IDs = %v, want [%d]; first ID was %d", got, newestID, firstID)
		}

		latest, err := repo.GetLatest(ctx, "tied")
		if err != nil {
			t.Fatalf("GetLatest: %v", err)
		}
		if latest == nil || latest.ID != newestID {
			t.Fatalf("GetLatest ID = %v, want %d", latest, newestID)
		}
	})

	t.Run("bounded batches converge and are idempotent", func(t *testing.T) {
		pool := taskHistoryTestPool(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		now := time.Now().UTC().Truncate(time.Microsecond)
		for i := range 7 {
			insertTaskExecution(t, pool, "batched", now.Add(time.Duration(i)*time.Minute))
		}

		var total int64
		for {
			deleted, err := pruneTaskHistoryBatch(ctx, pool, 1, now.Add(-24*time.Hour), 2)
			if err != nil {
				t.Fatalf("PruneBatch: %v", err)
			}
			total += deleted
			if deleted < 2 {
				break
			}
		}
		if total != 6 {
			t.Fatalf("total deleted = %d, want 6", total)
		}
		if got := len(remainingTaskExecutionIDs(t, pool, "batched")); got != 1 {
			t.Fatalf("remaining rows = %d, want 1", got)
		}
		deleted, err := pruneTaskHistoryBatch(ctx, pool, 1, now.Add(-24*time.Hour), 2)
		if err != nil {
			t.Fatalf("idempotent PruneBatch: %v", err)
		}
		if deleted != 0 {
			t.Fatalf("idempotent delete count = %d, want 0", deleted)
		}
	})
}

func TestPruneTaskHistoryBatchConcurrent(t *testing.T) {
	pool := taskHistoryTestPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := range 20 {
		insertTaskExecution(t, pool, "concurrent", now.Add(time.Duration(i)*time.Minute))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := pruneTaskHistoryBatch(ctx, pool, 1, now.Add(-24*time.Hour), 100)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent PruneBatch: %v", err)
		}
	}
	if got := len(remainingTaskExecutionIDs(t, pool, "concurrent")); got != 1 {
		t.Fatalf("remaining rows = %d, want 1", got)
	}
}

func TestPgExecutionRepositoryPrunePreservesEachTaskBoundary(t *testing.T) {
	pool := taskHistoryTestPool(t)
	repo := NewPgExecutionRepository(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)

	inserted := map[string][]int64{}
	for taskKey, count := range map[string]int{"task-a": 5, "task-b": 3, "task-c": 1} {
		for i := range count {
			inserted[taskKey] = append(inserted[taskKey], insertTaskExecution(
				t,
				pool,
				taskKey,
				now.Add(time.Duration(i)*time.Minute),
			))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := repo.Prune(ctx, 2, now.Add(-24*time.Hour), 2, 10)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if result.Deleted != 4 || result.LimitReached || result.Skipped {
		t.Fatalf("Prune result = %#v, want 4 deleted", result)
	}

	for taskKey, ids := range inserted {
		want := ids
		if len(want) > 2 {
			want = want[len(want)-2:]
		}
		got := remainingTaskExecutionIDs(t, pool, taskKey)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s remaining IDs = %v, want %v", taskKey, got, want)
		}
	}
}

func TestPgExecutionRepositoryPruneReportsBatchLimit(t *testing.T) {
	pool := taskHistoryTestPool(t)
	repo := NewPgExecutionRepository(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := range 7 {
		insertTaskExecution(t, pool, "limited", now.Add(time.Duration(i)*time.Minute))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := repo.Prune(ctx, 1, now.Add(-24*time.Hour), 2, 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if result.Deleted != 4 || !result.LimitReached || result.Skipped {
		t.Fatalf("Prune result = %#v, want 4 deleted with limit reached", result)
	}
	if got := len(remainingTaskExecutionIDs(t, pool, "limited")); got != 3 {
		t.Fatalf("remaining rows = %d, want 3", got)
	}

	result, err = repo.Prune(ctx, 1, now.Add(-24*time.Hour), 2, 10)
	if err != nil {
		t.Fatalf("finishing Prune: %v", err)
	}
	if result.Deleted != 2 || result.LimitReached || result.Skipped {
		t.Fatalf("finishing Prune result = %#v, want 2 deleted", result)
	}
}

func TestPgExecutionRepositoryPruneSkipsWhenAnotherNodeHoldsLock(t *testing.T) {
	pool := taskHistoryTestPool(t)
	repo := NewPgExecutionRepository(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	locker, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire lock connection: %v", err)
	}
	defer locker.Release()
	if _, err := locker.Exec(ctx, `SELECT pg_advisory_lock($1)`, taskHistoryCleanupAdvisoryLock); err != nil {
		t.Fatalf("hold cleanup lock: %v", err)
	}
	defer func() {
		_, _ = locker.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, taskHistoryCleanupAdvisoryLock)
	}()

	result, err := repo.Prune(ctx, 1, time.Now(), 10, 10)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if !result.Skipped || result.Deleted != 0 || result.LimitReached {
		t.Fatalf("Prune result = %#v, want skipped", result)
	}
}

func TestPgExecutionRepositoryPruneRejectsInvalidLimits(t *testing.T) {
	repo := &PgExecutionRepository{}
	if _, err := repo.Prune(context.Background(), 0, time.Now(), 1, 1); err == nil {
		t.Fatal("Prune accepted keepPerTask = 0")
	}
	if _, err := repo.Prune(context.Background(), 1, time.Now(), 0, 1); err == nil {
		t.Fatal("Prune accepted batchSize = 0")
	}
	if _, err := repo.Prune(context.Background(), 1, time.Now(), 1, 0); err == nil {
		t.Fatal("Prune accepted maxBatches = 0")
	}
}
