-- +goose Up
-- +goose StatementBegin
-- User policy fields move from "strictest of user and group wins" to
-- inherit/override: NULL on the user row means "inherit the access group's
-- value"; a non-NULL value is an explicit per-user override that replaces the
-- group value for that field in either direction (grant or restrict).
--
-- Access groups gain the two transcode gates so every user-level field has a
-- group value to inherit.
ALTER TABLE public.access_groups
    ADD COLUMN transcode_allowed boolean NOT NULL DEFAULT true,
    ADD COLUMN audio_transcode_allowed boolean NOT NULL DEFAULT true;

-- Users may now override the group's media-request gate as well.
ALTER TABLE public.users
    ADD COLUMN requests_allowed boolean;

-- Drop NOT NULL and the column defaults: a fresh user row inherits everything.
ALTER TABLE public.users
    ALTER COLUMN max_playback_quality DROP NOT NULL,
    ALTER COLUMN max_playback_quality DROP DEFAULT,
    ALTER COLUMN max_streams DROP DEFAULT,
    ALTER COLUMN max_transcodes DROP DEFAULT,
    ALTER COLUMN transcode_allowed DROP NOT NULL,
    ALTER COLUMN transcode_allowed DROP DEFAULT,
    ALTER COLUMN audio_transcode_allowed DROP NOT NULL,
    ALTER COLUMN audio_transcode_allowed DROP DEFAULT,
    ALTER COLUMN download_allowed DROP DEFAULT,
    ALTER COLUMN download_transcode_allowed DROP DEFAULT;

-- Behavior-preserving mapping of existing rows. Under the old merge a user
-- value of 0 / '' / true meant "no opinion at the user layer, the group
-- decides", so those become NULL (inherit). Restrictive values (false,
-- positive caps, a named quality, an explicit library list) stay as explicit
-- overrides. The one deliberate change: a positive cap that exceeds the
-- group's cap now wins instead of being clamped.
UPDATE public.users SET
    max_streams = NULLIF(max_streams, 0),
    max_transcodes = NULLIF(max_transcodes, 0),
    max_playback_quality = NULLIF(max_playback_quality, ''),
    transcode_allowed = CASE WHEN transcode_allowed THEN NULL ELSE false END,
    audio_transcode_allowed = CASE WHEN audio_transcode_allowed THEN NULL ELSE false END,
    download_allowed = CASE WHEN download_allowed THEN NULL ELSE false END,
    download_transcode_allowed = CASE WHEN download_transcode_allowed THEN NULL ELSE false END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE public.users SET
    max_streams = COALESCE(max_streams, 0),
    max_transcodes = COALESCE(max_transcodes, 0),
    max_playback_quality = COALESCE(max_playback_quality, ''),
    transcode_allowed = COALESCE(transcode_allowed, true),
    audio_transcode_allowed = COALESCE(audio_transcode_allowed, true),
    download_allowed = COALESCE(download_allowed, true),
    download_transcode_allowed = COALESCE(download_transcode_allowed, true);

ALTER TABLE public.users
    ALTER COLUMN max_playback_quality SET DEFAULT '',
    ALTER COLUMN max_playback_quality SET NOT NULL,
    ALTER COLUMN max_streams SET DEFAULT 0,
    ALTER COLUMN max_transcodes SET DEFAULT 0,
    ALTER COLUMN transcode_allowed SET DEFAULT true,
    ALTER COLUMN transcode_allowed SET NOT NULL,
    ALTER COLUMN audio_transcode_allowed SET DEFAULT true,
    ALTER COLUMN audio_transcode_allowed SET NOT NULL,
    ALTER COLUMN download_allowed SET DEFAULT true,
    ALTER COLUMN download_transcode_allowed SET DEFAULT false;

ALTER TABLE public.users
    DROP COLUMN IF EXISTS requests_allowed;

ALTER TABLE public.access_groups
    DROP COLUMN IF EXISTS transcode_allowed,
    DROP COLUMN IF EXISTS audio_transcode_allowed;
-- +goose StatementEnd
