-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.media_files
    ADD COLUMN first_seen_scan_run_id text NULL
        REFERENCES public.scan_runs(id) ON DELETE SET NULL;

ALTER TABLE public.episode_libraries
    ADD COLUMN first_seen_scan_run_id text NULL
        REFERENCES public.scan_runs(id) ON DELETE SET NULL;

CREATE INDEX idx_media_files_first_seen_scan_run_id
    ON public.media_files (first_seen_scan_run_id)
    WHERE first_seen_scan_run_id IS NOT NULL;

CREATE INDEX idx_episode_libraries_first_seen_scan_run_id
    ON public.episode_libraries (first_seen_scan_run_id)
    WHERE first_seen_scan_run_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.idx_episode_libraries_first_seen_scan_run_id;
DROP INDEX IF EXISTS public.idx_media_files_first_seen_scan_run_id;

ALTER TABLE public.episode_libraries
    DROP COLUMN IF EXISTS first_seen_scan_run_id;

ALTER TABLE public.media_files
    DROP COLUMN IF EXISTS first_seen_scan_run_id;
-- +goose StatementEnd
