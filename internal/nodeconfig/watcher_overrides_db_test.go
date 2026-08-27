package nodeconfig

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newOverrideTestPool follows the repository-wide convention for tests that
// need a real database: skip unless one is configured, and skip again if it
// predates the migration under test rather than failing on a missing column.
func newOverrideTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)
	var columns int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'stream_nodes' AND column_name = 'hw_accel_override'`).Scan(&columns); err != nil || columns < 1 {
		t.Skip("test database has not applied the stream_nodes override migration")
	}
	return pool
}

func insertOverrideNode(t *testing.T, pool *pgxpool.Pool, url string, accel *string) int {
	t.Helper()
	ctx := context.Background()
	var id int
	if err := pool.QueryRow(ctx,
		`INSERT INTO stream_nodes (name, type, url, hw_accel_override)
		 VALUES ($1, 'transcode', $2, $3) RETURNING id`,
		fmt.Sprintf("override-%d", time.Now().UnixNano()), url, accel).Scan(&id); err != nil {
		t.Fatalf("insert node %q: %v", url, err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM stream_nodes WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup node %d: %v", id, err)
		}
	})
	return id
}

// stream_nodes.url is unique on the exact string, so a node registered twice —
// once with a trailing slash — is two legal rows that the lookup's rtrim
// tolerance collapses into one key. The winner has to be the same on every
// reload: without an explicit order the seq scan returns whichever row it
// reaches first, and the 30-second health sweep rewriting those rows would
// silently flip the node between two acceleration policies mid-deployment.
func TestQueryNodeHWOverridesPicksTheSameRowAcrossReloads(t *testing.T) {
	pool := newOverrideTestPool(t)
	ctx := context.Background()
	base := fmt.Sprintf("http://dup-node-%d:8082", time.Now().UnixNano())

	pinned := "none"
	firstID := insertOverrideNode(t, pool, base, &pinned)
	insertOverrideNode(t, pool, base+"/", nil)

	w := &Watcher{pool: pool}
	assertPinned := func(stage string) {
		t.Helper()
		overrides, found, err := w.queryNodeHWOverrides(ctx, base)
		if err != nil {
			t.Fatalf("%s: lookup: %v", stage, err)
		}
		if !found {
			t.Fatalf("%s: node row not found", stage)
		}
		if overrides.HWAccel == nil || *overrides.HWAccel != pinned {
			t.Fatalf("%s: hw_accel override = %v, want the lowest-id row's %q", stage, overrides.HWAccel, pinned)
		}
	}

	assertPinned("initial load")

	// What the health sweep does every 30 seconds. It moves the tuple, and with
	// it the physical order an unordered scan would have followed.
	if _, err := pool.Exec(ctx,
		`UPDATE stream_nodes SET healthy = NOT healthy, last_health_check = NOW() WHERE id = $1`, firstID); err != nil {
		t.Fatalf("health sweep update: %v", err)
	}

	assertPinned("after a health sweep rewrote the row")
}
