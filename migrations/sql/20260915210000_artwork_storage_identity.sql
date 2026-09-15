-- +goose Up
-- Artwork storage used to be recorded three ways: the backend name from the
-- first write, an S3 fingerprint the reconcile task certified, and a delivery
-- scope on each revision. One row now names where the catalog's artwork keys
-- live, in the form the store reports: "local|<root>" or
-- "s3|<endpoint>|<bucket>|<prefix>". Existing installs keep their recorded
-- storage by translating the old rows; deployments that never wrote artwork
-- record it on their next write.
INSERT INTO server_settings (key, value)
SELECT 'artwork.storage_identity',
       CASE
           WHEN identity.value LIKE 'local|%' THEN identity.value
           WHEN active.value = 's3' AND identity.value <> '' THEN 's3|' || identity.value
           ELSE ''
       END
FROM (SELECT value FROM server_settings WHERE key = 'artwork.storage_backend_active') active
LEFT JOIN (SELECT value FROM server_settings WHERE key = 's3.public_storage_identity') identity ON TRUE
WHERE NOT EXISTS (SELECT 1 FROM server_settings WHERE key = 'artwork.storage_identity');
-- An empty value is treated as absent by the settings store.
DELETE FROM server_settings WHERE key = 'artwork.storage_identity' AND value = '';
DELETE FROM server_settings
WHERE key IN ('artwork.storage_backend_active', 's3.public_storage_identity', 's3.public_storage_sweep_checkpoint');

-- +goose Down
DELETE FROM server_settings WHERE key = 'artwork.storage_identity';
