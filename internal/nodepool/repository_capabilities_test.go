package nodepool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newNodeTestPool follows the repository-wide convention for tests that need a
// real database: skip unless one is configured, and skip again if it predates
// the migration under test rather than failing on a missing column.
func newNodeTestPool(t *testing.T) *pgxpool.Pool {
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
	var column *string
	if err := pool.QueryRow(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_name = 'stream_nodes' AND column_name = 'capabilities_hash'`).Scan(&column); err != nil {
		t.Skip("test database has not applied the node capabilities migration")
	}
	return pool
}

// Stored capabilities are what makes GPU inventory survive an API restart, so
// the payload, its hash, and its age must all come back through an ordinary
// List — the same read the pools and the admin API use.
func TestRepositoryUpdateCapabilitiesRoundTrip(t *testing.T) {
	pool := newNodeTestPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	node, err := repo.Create(ctx, CreateNodeInput{
		Name: fmt.Sprintf("capability-test-%d", time.Now().UnixNano()),
		Type: NodeTypeTranscode,
		URL:  fmt.Sprintf("http://capability-test-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(ctx, node.ID) })

	if node.Capabilities != nil || node.CapabilitiesHash != nil || node.CapabilitiesRefreshedAt != nil {
		t.Fatalf("new node already carries capabilities: %+v", node)
	}

	payload := json.RawMessage(`{"resolved":"nvenc","render_devices":["/dev/dri/renderD128"]}`)
	refreshedAt := time.Now().UTC().Truncate(time.Millisecond)
	if err := repo.UpdateCapabilities(ctx, node.ID, payload, "sha256:abc", refreshedAt); err != nil {
		t.Fatalf("update capabilities: %v", err)
	}

	reloaded, err := repo.GetByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	var stored, want map[string]any
	if err := json.Unmarshal(reloaded.Capabilities, &stored); err != nil {
		t.Fatalf("stored capabilities are not json: %v", err)
	}
	if err := json.Unmarshal(payload, &want); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(stored) != fmt.Sprint(want) {
		t.Fatalf("stored capabilities = %v, want %v", stored, want)
	}
	if reloaded.CapabilitiesHash == nil || *reloaded.CapabilitiesHash != "sha256:abc" {
		t.Fatalf("stored hash = %v", reloaded.CapabilitiesHash)
	}
	if reloaded.CapabilitiesRefreshedAt == nil || !reloaded.CapabilitiesRefreshedAt.UTC().Equal(refreshedAt) {
		t.Fatalf("stored refresh time = %v, want %v", reloaded.CapabilitiesRefreshedAt, refreshedAt)
	}
}

func TestRepositoryUpdateCapabilitiesUnknownNode(t *testing.T) {
	repo := NewRepository(newNodeTestPool(t))
	err := repo.UpdateCapabilities(context.Background(), -1, []byte(`{}`), "sha256:abc", time.Now())
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("err = %v, want ErrNodeNotFound", err)
	}
}
