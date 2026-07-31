package streamrevoke

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func realRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("SILO_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("SILO_TEST_REDIS_ADDR is not set")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping test Redis: %v", err)
	}
	return rdb
}

func uniqueRedisRevocation(t *testing.T) (Key, string) {
	t.Helper()
	id := t.Name() + ":" + time.Now().UTC().Format("20060102150405.000000000")
	key := Key{Kind: KindSession, ID: id}
	return key, redisKey(key)
}

func readMirror(t *testing.T, rdb *redis.Client, key string) mirrorPayload {
	t.Helper()
	data, err := rdb.Get(context.Background(), key).Bytes()
	if err != nil {
		t.Fatalf("get mirror: %v", err)
	}
	var payload mirrorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode mirror %q: %v", data, err)
	}
	return payload
}

func TestRedisMirrorConvergesConcurrentIndependentDimensions(t *testing.T) {
	ctx := context.Background()
	rdb := realRedis(t)
	key, redisMirrorKey := uniqueRedisRevocation(t)
	t.Cleanup(func() { _ = rdb.Del(ctx, redisMirrorKey).Err() })
	base := time.Unix(time.Now().Add(time.Hour).Unix(), 123456789).UTC()
	longer := Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "older-cutoff",
		RevokedAt: base, ExpiresAt: base.Add(2 * time.Hour),
	}
	later := Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "later-cutoff",
		RevokedAt: base.Add(time.Nanosecond), ExpiresAt: base.Add(time.Hour),
	}

	stores := []*Store{New(Options{Redis: rdb}), New(Options{Redis: rdb})}
	revocations := []Revocation{longer, later}
	errs := make(chan error, len(stores))
	var wg sync.WaitGroup
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- stores[i].mirrorToRedis(ctx, revocations[i])
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	got := readMirror(t, rdb, redisMirrorKey)
	if !got.RevokedAt.Equal(later.RevokedAt) || got.Reason != later.Reason {
		t.Fatalf("cutoff=%v reason=%q, want %v %q", got.RevokedAt, got.Reason, later.RevokedAt, later.Reason)
	}
	if !got.ExpiresAt.Equal(longer.ExpiresAt) {
		t.Fatalf("expiry=%v, want %v", got.ExpiresAt, longer.ExpiresAt)
	}
}

func TestRedisMirrorPermanentKillIsNeverDowngraded(t *testing.T) {
	ctx := context.Background()
	rdb := realRedis(t)
	key, redisMirrorKey := uniqueRedisRevocation(t)
	t.Cleanup(func() { _ = rdb.Del(ctx, redisMirrorKey).Err() })
	base := time.Now().UTC()
	store := New(Options{Redis: rdb})
	if err := store.mirrorToRedis(ctx, Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "permanent",
		RevokedAt: base,
	}); err != nil {
		t.Fatal(err)
	}
	bounded := Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "new-cutoff",
		RevokedAt: base.Add(time.Nanosecond), ExpiresAt: base.Add(time.Hour),
	}
	if err := store.mirrorToRedis(ctx, bounded); err != nil {
		t.Fatal(err)
	}

	got := readMirror(t, rdb, redisMirrorKey)
	if !got.ExpiresAt.IsZero() {
		t.Fatalf("expiry=%v, want permanent", got.ExpiresAt)
	}
	if !got.RevokedAt.Equal(bounded.RevokedAt) || got.Reason != bounded.Reason {
		t.Fatalf("cutoff=%v reason=%q, want %v %q", got.RevokedAt, got.Reason, bounded.RevokedAt, bounded.Reason)
	}
}

func TestRedisMirrorLapsedIncomingDoesNotDeleteStrongerKill(t *testing.T) {
	ctx := context.Background()
	rdb := realRedis(t)
	key, redisMirrorKey := uniqueRedisRevocation(t)
	t.Cleanup(func() { _ = rdb.Del(ctx, redisMirrorKey).Err() })
	now := time.Now().UTC()
	store := New(Options{Redis: rdb})
	live := Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "live",
		RevokedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	}
	if err := store.mirrorToRedis(ctx, live); err != nil {
		t.Fatal(err)
	}
	if err := store.mirrorToRedis(ctx, Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "lapsed-new-cutoff",
		RevokedAt: now, ExpiresAt: now.Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	got := readMirror(t, rdb, redisMirrorKey)
	if !got.ExpiresAt.Equal(live.ExpiresAt) || got.Reason != "lapsed-new-cutoff" {
		t.Fatalf("merged mirror=%+v, want live expiry and newer cutoff reason", got)
	}
}

func TestRedisMirrorOverwritesLegacyPayload(t *testing.T) {
	ctx := context.Background()
	rdb := realRedis(t)
	key, redisMirrorKey := uniqueRedisRevocation(t)
	t.Cleanup(func() { _ = rdb.Del(ctx, redisMirrorKey).Err() })
	legacy := Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "legacy",
		RevokedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour),
	}
	legacyData, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := rdb.Set(ctx, redisMirrorKey, legacyData, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	incoming := Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "replacement",
		RevokedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(2 * time.Hour).UTC(),
	}
	if err := New(Options{Redis: rdb}).mirrorToRedis(ctx, incoming); err != nil {
		t.Fatal(err)
	}

	got := readMirror(t, rdb, redisMirrorKey)
	if !got.RevokedAt.Equal(incoming.RevokedAt) || !got.ExpiresAt.Equal(incoming.ExpiresAt) ||
		got.RevokedAtSec == 0 || got.ExpiresAtSec == 0 {
		t.Fatalf("mirror=%+v, want numeric replacement payload for %+v", got, incoming)
	}
}

func TestRedisMirrorEqualNanosecondCutoffKeepsExistingReason(t *testing.T) {
	ctx := context.Background()
	rdb := realRedis(t)
	key, redisMirrorKey := uniqueRedisRevocation(t)
	t.Cleanup(func() { _ = rdb.Del(ctx, redisMirrorKey).Err() })
	cutoff := time.Unix(time.Now().Unix(), 123456789).UTC()
	first := Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "original-reason",
		RevokedAt: cutoff, ExpiresAt: cutoff.Add(time.Hour),
	}
	second := Revocation{
		Kind: key.Kind, ID: key.ID, Reason: "second",
		RevokedAt: cutoff, ExpiresAt: cutoff.Add(2 * time.Hour),
	}
	store := New(Options{Redis: rdb})
	if err := store.mirrorToRedis(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.mirrorToRedis(ctx, second); err != nil {
		t.Fatal(err)
	}

	got := readMirror(t, rdb, redisMirrorKey)
	if !got.RevokedAt.Equal(cutoff) || got.Reason != first.Reason {
		t.Fatalf("cutoff=%v reason=%q, want stable %v %q", got.RevokedAt, got.Reason, cutoff, first.Reason)
	}
	if !got.ExpiresAt.Equal(second.ExpiresAt) {
		t.Fatalf("expiry=%v, want independently merged %v", got.ExpiresAt, second.ExpiresAt)
	}
}
