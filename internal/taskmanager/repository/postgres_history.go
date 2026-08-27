package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/taskmanager"
)

const taskHistoryCleanupAdvisoryLock int64 = 0x53494C4F48495354 // "SILOHIST"

type taskHistoryExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// PgExecutionRepository implements taskmanager.ExecutionRepository using PostgreSQL.
type PgExecutionRepository struct {
	pool *pgxpool.Pool
}

func NewPgExecutionRepository(pool *pgxpool.Pool) *PgExecutionRepository {
	return &PgExecutionRepository{pool: pool}
}

func (r *PgExecutionRepository) Insert(ctx context.Context, result taskmanager.ExecutionResult) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO task_executions (task_key, started_at, completed_at, status, error_message, result_data, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		result.TaskKey, result.StartedAt, result.CompletedAt,
		result.Status, result.ErrorMessage, result.ResultData, result.DurationMs,
	)
	if err != nil {
		return fmt.Errorf("inserting task execution: %w", err)
	}
	return nil
}

func (r *PgExecutionRepository) GetLatest(ctx context.Context, taskKey string) (*taskmanager.ExecutionResult, error) {
	var result taskmanager.ExecutionResult
	err := r.pool.QueryRow(ctx, `
		SELECT id, task_key, started_at, completed_at, status, COALESCE(error_message, ''), result_data, duration_ms
		FROM task_executions
		WHERE task_key = $1
		ORDER BY completed_at DESC, id DESC
		LIMIT 1`, taskKey,
	).Scan(
		&result.ID, &result.TaskKey, &result.StartedAt, &result.CompletedAt,
		&result.Status, &result.ErrorMessage, &result.ResultData, &result.DurationMs,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("getting latest task execution: %w", err)
	}
	return &result, nil
}

func (r *PgExecutionRepository) List(ctx context.Context, taskKey string, limit int) ([]taskmanager.ExecutionResult, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_key, started_at, completed_at, status, COALESCE(error_message, ''), result_data, duration_ms
		FROM task_executions
		WHERE task_key = $1
		ORDER BY completed_at DESC, id DESC
		LIMIT $2`, taskKey, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing task executions: %w", err)
	}
	defer rows.Close()

	var results []taskmanager.ExecutionResult
	for rows.Next() {
		var r taskmanager.ExecutionResult
		if err := rows.Scan(
			&r.ID, &r.TaskKey, &r.StartedAt, &r.CompletedAt,
			&r.Status, &r.ErrorMessage, &r.ResultData, &r.DurationMs,
		); err != nil {
			return nil, fmt.Errorf("scanning task execution: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// Prune deletes bounded batches of execution history outside the retention
// policy. A database advisory lock ensures only one Silo node prunes at a time.
// It always preserves the newest execution for every task, including tasks
// that are no longer registered.
func (r *PgExecutionRepository) Prune(
	ctx context.Context,
	keepPerTask int,
	cutoff time.Time,
	batchSize int,
	maxBatches int,
) (pruneResult taskmanager.HistoryPruneResult, returnErr error) {
	if keepPerTask < 1 {
		return pruneResult, fmt.Errorf("pruning task executions: keep per task must be positive")
	}
	if batchSize < 1 {
		return pruneResult, fmt.Errorf("pruning task executions: batch size must be positive")
	}
	if maxBatches < 1 {
		return pruneResult, fmt.Errorf("pruning task executions: max batches must be positive")
	}

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return pruneResult, fmt.Errorf("acquiring task history cleanup connection: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, taskHistoryCleanupAdvisoryLock).Scan(&acquired); err != nil {
		conn.Release()
		return pruneResult, fmt.Errorf("acquiring task history cleanup lock: %w", err)
	}
	if !acquired {
		conn.Release()
		pruneResult.Skipped = true
		return pruneResult, nil
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		unlockErr := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock($1)`, taskHistoryCleanupAdvisoryLock).Scan(&unlocked)
		if unlockErr == nil && unlocked {
			conn.Release()
			return
		}

		// A session-level advisory lock survives connection pooling. Destroy the
		// connection if it could not be unlocked so the lock cannot leak back
		// into the pool.
		rawConn := conn.Hijack()
		_ = rawConn.Close(context.Background())
		if returnErr == nil {
			if unlockErr != nil {
				returnErr = fmt.Errorf("releasing task history cleanup lock: %w", unlockErr)
			} else {
				returnErr = fmt.Errorf("releasing task history cleanup lock: lock was not held")
			}
		}
	}()

	for range maxBatches {
		if err := ctx.Err(); err != nil {
			return pruneResult, err
		}
		deleted, err := pruneTaskHistoryBatch(ctx, conn, keepPerTask, cutoff, batchSize)
		pruneResult.Deleted += deleted
		if err != nil {
			return pruneResult, err
		}
		if deleted < int64(batchSize) {
			return pruneResult, nil
		}
	}
	pruneResult.LimitReached = true
	return pruneResult, nil
}

func pruneTaskHistoryBatch(
	ctx context.Context,
	execer taskHistoryExecer,
	keepPerTask int,
	cutoff time.Time,
	batchSize int,
) (int64, error) {
	result, err := execer.Exec(ctx, `
		WITH ranked AS (
			SELECT
				id,
				completed_at,
				row_number() OVER (
					PARTITION BY task_key
					ORDER BY completed_at DESC, id DESC
				) AS recent_rank
			FROM task_executions
		),
		doomed AS (
			SELECT id
			FROM ranked
			WHERE recent_rank > $1
				OR (recent_rank > 1 AND completed_at < $2)
			ORDER BY id
			LIMIT $3
		)
		DELETE FROM task_executions AS execution
		USING doomed
		WHERE execution.id = doomed.id`,
		keepPerTask,
		cutoff,
		batchSize,
	)
	if err != nil {
		return 0, fmt.Errorf("pruning task executions: %w", err)
	}
	return result.RowsAffected(), nil
}
