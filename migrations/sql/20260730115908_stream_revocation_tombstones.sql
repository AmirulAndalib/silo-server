-- +goose Up
ALTER TABLE public.stream_revocations
    ADD COLUMN unrevoked_at timestamptz NULL,
    ADD COLUMN tombstone_expires_at timestamptz NULL;

CREATE INDEX stream_revocations_tombstone_expires_at_idx
    ON public.stream_revocations (tombstone_expires_at)
    WHERE unrevoked_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS public.stream_revocations_tombstone_expires_at_idx;

ALTER TABLE public.stream_revocations
    DROP COLUMN IF EXISTS tombstone_expires_at,
    DROP COLUMN IF EXISTS unrevoked_at;
