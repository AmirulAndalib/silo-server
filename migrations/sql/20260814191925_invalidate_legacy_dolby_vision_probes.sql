-- +goose Up
UPDATE public.media_files AS mf
SET probe_updated_at = NULL
WHERE mf.probe_updated_at IS NOT NULL
  AND EXISTS (
      SELECT 1
      FROM jsonb_array_elements(
          CASE jsonb_typeof(mf.video_tracks)
              WHEN 'array' THEN mf.video_tracks
              ELSE '[]'::jsonb
          END
      ) AS track
      WHERE (
          track ? 'dv_profile'
          OR lower(COALESCE(track ->> 'video_range_type', '')) LIKE '%dovi%'
          OR lower(COALESCE(track ->> 'dolby_vision', '')) LIKE '%dolby%'
      )
        AND (
            NOT (track ? 'dv_config_present')
            OR NOT (track ? 'dv_bl_compat_id_present')
        )
  );

-- +goose Down
SELECT 1;
