package streamrevoke

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// permanentExpiry is the far-future sentinel written when a Revocation has a
// zero-value ExpiresAt. The hot path treats a zero ExpiresAt as "never expires"
// (a permanent kill), but the DB column is NOT NULL and Prune/List compare
// expires_at <= now(): a literal zero time (0001-01-01) would be excluded by
// List and deleted by the very next Prune, silently evaporating a
// permanent kill. Writing a year-2999 sentinel preserves the intent durably.
var permanentExpiry = time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)

// PostgresDurableStore is the Postgres-backed DurableStore: a durable mirror of
// the kill list so revocations survive a Redis flush or a server restart. It is
// never on the hot path — Store consults it only on write (Upsert), on
// warm/reconcile (List), and on trim (Prune).
//
// Rows are keyed by (kind, id) so re-revoking the same session/user (the async
// over-cap enforcer does this every pass) UPSERTs the same row rather than
// accumulating duplicates; physical growth is reclaimed by Prune.
type PostgresDurableStore struct {
	pool *pgxpool.Pool
}

const durableUpsertSQL = `
	INSERT INTO stream_revocations (kind, id, reason, revoked_at, expires_at)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT (kind, id) DO UPDATE SET
		reason = CASE WHEN EXCLUDED.revoked_at > stream_revocations.revoked_at
			THEN EXCLUDED.reason ELSE stream_revocations.reason END,
		revoked_at = GREATEST(stream_revocations.revoked_at, EXCLUDED.revoked_at),
		expires_at = CASE WHEN stream_revocations.unrevoked_at IS NOT NULL
			THEN EXCLUDED.expires_at
			ELSE GREATEST(stream_revocations.expires_at, EXCLUDED.expires_at) END,
		unrevoked_at = NULL,
		tombstone_expires_at = NULL
	WHERE stream_revocations.unrevoked_at IS NULL
		OR stream_revocations.tombstone_expires_at <= now()
		OR EXCLUDED.revoked_at > stream_revocations.unrevoked_at`

const durableTombstoneUpsertSQL = `
	INSERT INTO stream_revocations (
		kind, id, reason, revoked_at, expires_at, unrevoked_at, tombstone_expires_at
	)
	VALUES ($1, $2, '', $3, $4, $3, $4)
	ON CONFLICT (kind, id) DO UPDATE SET
		unrevoked_at = EXCLUDED.unrevoked_at,
		tombstone_expires_at = EXCLUDED.tombstone_expires_at
	WHERE EXCLUDED.unrevoked_at >= stream_revocations.revoked_at
		AND (
			stream_revocations.unrevoked_at IS NULL
			OR EXCLUDED.unrevoked_at >= stream_revocations.unrevoked_at
		)`

// NewPostgresDurableStore builds a DurableStore from a pgx pool. It returns a
// nil DurableStore interface when pool is nil so callers can pass the result
// straight into Options.Durable and a Redis-less/DB-less mode degrades to a
// true nil interface (avoiding the "non-nil interface wrapping a nil pointer"
// trap that would make Store.durable != nil erroneously true).
func NewPostgresDurableStore(pool *pgxpool.Pool) DurableStore {
	if pool == nil {
		return nil
	}
	return &PostgresDurableStore{pool: pool}
}

// Upsert writes or refreshes a revocation, keyed by (kind, id).
func (s *PostgresDurableStore) Upsert(ctx context.Context, r Revocation) error {
	// A zero ExpiresAt means "permanent" on the hot path; persist it as a
	// far-future sentinel so Prune/List don't immediately reap the row.
	expiresAt := r.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = permanentExpiry
	}
	// Merge cutoff and expiry independently, matching applyLocal: the later
	// RevokedAt (and its reason) wins while expiry remains monotonic. A live
	// tombstone rejects stale revocations; a genuinely newer revocation replaces
	// the tombstone and starts a fresh expiry horizon.
	_, err := s.pool.Exec(ctx, durableUpsertSQL,
		string(r.Kind), r.ID, r.Reason, r.RevokedAt, expiresAt)
	if err != nil {
		return fmt.Errorf("streamrevoke upsert: %w", err)
	}
	return nil
}

// UpsertTombstone durably records an explicit unrevoke. A stale tombstone
// cannot erase a newer revocation, and a stale replica cannot overwrite a live
// tombstone through Upsert.
func (s *PostgresDurableStore) UpsertTombstone(ctx context.Context, t Tombstone) error {
	if _, err := s.pool.Exec(ctx, durableTombstoneUpsertSQL,
		string(t.Kind), t.ID, t.UnrevokedAt, t.ExpiresAt); err != nil {
		return fmt.Errorf("streamrevoke tombstone upsert: %w", err)
	}
	return nil
}

// List returns one snapshot of every active revocation and live tombstone.
// Tombstoned rows are never surfaced as revocations.
func (s *PostgresDurableStore) List(ctx context.Context) (DurableState, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind, id, reason, revoked_at, expires_at,
			unrevoked_at, tombstone_expires_at
		FROM stream_revocations
		WHERE (unrevoked_at IS NULL AND expires_at > now())
			OR (unrevoked_at IS NOT NULL AND tombstone_expires_at > now())`)
	if err != nil {
		return DurableState{}, fmt.Errorf("streamrevoke list: %w", err)
	}
	defer rows.Close()

	var state DurableState
	for rows.Next() {
		var r Revocation
		var kind string
		var unrevokedAt, tombstoneExpiresAt *time.Time
		if err := rows.Scan(
			&kind, &r.ID, &r.Reason, &r.RevokedAt, &r.ExpiresAt,
			&unrevokedAt, &tombstoneExpiresAt,
		); err != nil {
			return DurableState{}, fmt.Errorf("streamrevoke scan: %w", err)
		}
		r.Kind = Kind(kind)
		if unrevokedAt != nil {
			if tombstoneExpiresAt != nil {
				state.Tombstones = append(state.Tombstones, Tombstone{
					Kind: r.Kind, ID: r.ID, UnrevokedAt: *unrevokedAt, ExpiresAt: *tombstoneExpiresAt,
				})
			}
			continue
		}
		state.Revocations = append(state.Revocations, r)
	}
	if err := rows.Err(); err != nil {
		return DurableState{}, fmt.Errorf("streamrevoke list rows: %w", err)
	}
	return state, nil
}

// Prune physically deletes expired revocations and expired tombstones so
// neither state grows without bound.
func (s *PostgresDurableStore) Prune(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM stream_revocations
		WHERE (unrevoked_at IS NULL AND expires_at <= now())
			OR (unrevoked_at IS NOT NULL AND tombstone_expires_at <= now())`); err != nil {
		return fmt.Errorf("streamrevoke prune: %w", err)
	}
	return nil
}
