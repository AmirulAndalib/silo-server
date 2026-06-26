package abs

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

var errPolicyUnavailable = errors.New("download policy unavailable")

// newFeedsHarnessWithPolicy builds the RSS feeds harness with an explicit
// DownloadPolicy so the download-privilege gate can be exercised.
func newFeedsHarnessWithPolicy(t *testing.T, policy DownloadPolicy, knownItems ...string) (*Handler, *memRSSFeedStore) {
	t.Helper()
	known := map[string]*models.MediaItem{}
	for _, id := range knownItems {
		known[id] = nil
	}
	store := newMemRSSFeedStore()
	h := New(Dependencies{
		MediaStore:     &stubMediaStore{known: known},
		RSSFeedStore:   store,
		DownloadPolicy: policy,
	})
	return h, store
}

// TestFeed_Open_DeniedWhenDownloadDisabled is part of the issue #141 fix: an
// RSS feed mints public, unauthenticated /feed/{slug}/file/{ino} enclosures, so
// a restricted user must not be able to create one (it would otherwise bypass
// the handleFileStream download gate).
func TestFeed_Open_DeniedWhenDownloadDisabled(t *testing.T) {
	policy := &fakeDownloadPolicy{allowed: map[string]bool{"1": false}}
	h, store := newFeedsHarnessWithPolicy(t, policy, "book-1")

	rec := dispatchABSWithParams(http.MethodPost, "/api/feeds/item/book-1/open",
		map[string]string{"itemId": "book-1"}, []byte(`{}`), "1", "", h.handleOpenItemFeed)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if n := len(store.rows); n != 0 {
		t.Errorf("a feed was persisted (%d rows) despite the download gate", n)
	}
}

// TestFeed_Open_AllowedWhenDownloadEnabled confirms the gate does not regress
// the happy path.
func TestFeed_Open_AllowedWhenDownloadEnabled(t *testing.T) {
	policy := &fakeDownloadPolicy{allowed: map[string]bool{"1": true}}
	h, _ := newFeedsHarnessWithPolicy(t, policy, "book-1")

	rec := dispatchABSWithParams(http.MethodPost, "/api/feeds/item/book-1/open",
		map[string]string{"itemId": "book-1"}, []byte(`{}`), "1", "", h.handleOpenItemFeed)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// seedFeed inserts an open feed owned by ownerID directly into the store,
// modeling a feed created while the owner could download.
func seedFeed(t *testing.T, store *memRSSFeedStore, slug, ownerID, itemID string) {
	t.Helper()
	if err := store.CreateFeed(context.Background(), RSSFeed{
		ID:            "feed-" + slug,
		UserID:        ownerID,
		LibraryItemID: itemID,
		Slug:          slug,
	}); err != nil {
		t.Fatalf("seed feed: %v", err)
	}
}

// TestPublicFeed_FailsClosedAfterRevocation verifies that an existing feed
// stops serving once the owner's download privilege is revoked: both the XML
// and the file enclosure must report 404 (hiding the feed) rather than serving.
func TestPublicFeed_FailsClosedAfterRevocation(t *testing.T) {
	policy := &fakeDownloadPolicy{allowed: map[string]bool{"1": false}}
	h, store := newFeedsHarnessWithPolicy(t, policy, "book-1")
	seedFeed(t, store, "revoked-feed", "1", "book-1")

	xml := dispatchABSWithParams(http.MethodGet, "/feed/revoked-feed.xml",
		map[string]string{"slug": "revoked-feed.xml"}, nil, "", "", h.handlePublicFeed)
	if xml.Code != http.StatusNotFound {
		t.Errorf("public feed XML status = %d, want 404 after revocation; body=%s", xml.Code, xml.Body.String())
	}

	file := dispatchABSWithParams(http.MethodGet, "/feed/revoked-feed/file/1",
		map[string]string{"slug": "revoked-feed", "ino": "1"}, nil, "", "", h.handlePublicFeedFile)
	if file.Code != http.StatusNotFound {
		t.Errorf("public feed file status = %d, want 404 after revocation; body=%s", file.Code, file.Body.String())
	}
}

// TestPublicFeed_FailsClosedOnPolicyError ensures a resolver error hides the
// feed rather than serving bytes (fail closed).
func TestPublicFeed_FailsClosedOnPolicyError(t *testing.T) {
	policy := &fakeDownloadPolicy{err: errPolicyUnavailable}
	h, store := newFeedsHarnessWithPolicy(t, policy, "book-1")
	seedFeed(t, store, "err-feed", "1", "book-1")

	file := dispatchABSWithParams(http.MethodGet, "/feed/err-feed/file/1",
		map[string]string{"slug": "err-feed", "ino": "1"}, nil, "", "", h.handlePublicFeedFile)
	if file.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 on resolver error; body=%s", file.Code, file.Body.String())
	}
}
