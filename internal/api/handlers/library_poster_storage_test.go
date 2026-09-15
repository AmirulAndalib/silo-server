package handlers

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/artworkstore"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/jackc/pgx/v5/pgxpool"
)

type posterS3Store struct {
	deleted []string
	written []string
}

func (*posterS3Store) Bucket() string { return "artwork" }
func (s *posterS3Store) PutObject(_ context.Context, _, key string, _ []byte) error {
	s.written = append(s.written, key)
	return nil
}
func (s *posterS3Store) DeleteObject(_ context.Context, _, key string) error {
	s.deleted = append(s.deleted, key)
	return nil
}
func (*posterS3Store) PresignGetURL(_ context.Context, _, key string, _ time.Duration) (string, error) {
	return "https://example.invalid/" + key, nil
}

type failedPosterStore struct{ artworkstore.Store }

func (failedPosterStore) Put(context.Context, string, []byte) error {
	return errors.New("no space left on device")
}

func TestLibraryPosterStorageFallbackAndFailureLog(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, err = pool.Exec(t.Context(), `CREATE TEMP TABLE media_folders (LIKE public.media_folders INCLUDING DEFAULTS);
CREATE TEMP TABLE media_folder_paths (LIKE public.media_folder_paths INCLUDING DEFAULTS);
INSERT INTO media_folders (id,type,name,enabled,poster_path) VALUES (7,'movies','Poster test',true,'library-posters/7.jpg');`)
	if err != nil {
		t.Fatal(err)
	}
	h := NewLibraryHandler(catalog.NewFolderRepository(pool), nil, nil, nil, nil)
	s3 := &posterS3Store{}
	h.S3Meta = s3
	if _, err := h.UploadLibraryPoster(t.Context(), 7, "image/png", []byte("poster")); err != nil {
		t.Fatal(err)
	}
	if len(s3.deleted) != 1 || s3.deleted[0] != "library-posters/7.jpg" || len(s3.written) != 1 || s3.written[0] != "library-posters/7.png" {
		t.Fatalf("replacement storage operations: deleted=%v written=%v", s3.deleted, s3.written)
	}
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	h.ArtworkStore = failedPosterStore{}
	if _, err := h.UploadLibraryPoster(t.Context(), 7, "image/png", []byte("replacement")); err == nil {
		t.Fatal("storage failure was ignored")
	}
	if !strings.Contains(logs.String(), "no space left on device") {
		t.Fatalf("storage failure missing from log: %s", logs.String())
	}
}
