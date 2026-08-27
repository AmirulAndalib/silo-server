-- +goose Up
-- The admin dashboard's activity aggregates scan by start/watch time over a
-- rolling window. Both tables are only indexed on their end-of-play timestamps
-- today (idx_playback_history_admin_ended, idx_user_watch_history_*), which a
-- "started in the last N hours" or "watched in the last N days" filter cannot
-- use.
CREATE INDEX IF NOT EXISTS idx_playback_history_admin_started
    ON public.playback_history_admin USING btree (started_at DESC);

CREATE INDEX IF NOT EXISTS idx_user_watch_history_watched_at
    ON public.user_watch_history USING btree (watched_at DESC);

-- +goose Down
DROP INDEX IF EXISTS public.idx_user_watch_history_watched_at;

DROP INDEX IF EXISTS public.idx_playback_history_admin_started;
