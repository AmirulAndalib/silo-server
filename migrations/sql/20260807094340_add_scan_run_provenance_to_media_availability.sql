-- +goose NO TRANSACTION

-- +goose Up
ALTER TABLE public.media_files
    ADD COLUMN IF NOT EXISTS first_seen_scan_run_id text NULL;

ALTER TABLE public.episode_libraries
    ADD COLUMN IF NOT EXISTS first_seen_scan_run_id text NULL;

-- Add the foreign keys without scanning the populated availability tables
-- under a write-blocking lock. Validation below uses the lighter lock mode.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'public.media_files'::regclass
          AND conname = 'media_files_first_seen_scan_run_id_fkey'
    ) THEN
        ALTER TABLE public.media_files
            ADD CONSTRAINT media_files_first_seen_scan_run_id_fkey
            FOREIGN KEY (first_seen_scan_run_id)
            REFERENCES public.scan_runs(id) ON DELETE SET NULL
            NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'public.episode_libraries'::regclass
          AND conname = 'episode_libraries_first_seen_scan_run_id_fkey'
    ) THEN
        ALTER TABLE public.episode_libraries
            ADD CONSTRAINT episode_libraries_first_seen_scan_run_id_fkey
            FOREIGN KEY (first_seen_scan_run_id)
            REFERENCES public.scan_runs(id) ON DELETE SET NULL
            NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

-- A failed concurrent build can leave an invalid index that IF NOT EXISTS
-- would otherwise skip on retry.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        JOIN pg_index i ON i.indexrelid = c.oid
        WHERE n.nspname = 'public'
          AND c.relname = 'idx_media_files_first_seen_scan_run_id'
          AND NOT i.indisvalid
    ) THEN
        DROP INDEX public.idx_media_files_first_seen_scan_run_id;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_media_files_first_seen_scan_run_id
    ON public.media_files (first_seen_scan_run_id)
    WHERE first_seen_scan_run_id IS NOT NULL;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        JOIN pg_index i ON i.indexrelid = c.oid
        WHERE n.nspname = 'public'
          AND c.relname = 'idx_episode_libraries_first_seen_scan_run_id'
          AND NOT i.indisvalid
    ) THEN
        DROP INDEX public.idx_episode_libraries_first_seen_scan_run_id;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_episode_libraries_first_seen_scan_run_id
    ON public.episode_libraries (first_seen_scan_run_id)
    WHERE first_seen_scan_run_id IS NOT NULL;

ALTER TABLE public.media_files
    VALIDATE CONSTRAINT media_files_first_seen_scan_run_id_fkey;

ALTER TABLE public.episode_libraries
    VALIDATE CONSTRAINT episode_libraries_first_seen_scan_run_id_fkey;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS public.idx_episode_libraries_first_seen_scan_run_id;
DROP INDEX CONCURRENTLY IF EXISTS public.idx_media_files_first_seen_scan_run_id;

ALTER TABLE public.episode_libraries
    DROP COLUMN IF EXISTS first_seen_scan_run_id;

ALTER TABLE public.media_files
    DROP COLUMN IF EXISTS first_seen_scan_run_id;
