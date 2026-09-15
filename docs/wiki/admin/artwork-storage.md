# Artwork storage

In **Settings → Infrastructure → Artwork storage**, choose Automatic, Local
disk, or S3. Automatic uses the public S3 bucket when one is configured and local
disk otherwise. Changes require a server restart.

Local disk suits a single server. Persist the displayed artwork directory in
Docker; it contains provider caches and uploads. S3 is recommended when multiple
hosts share a catalog, so every host can read the same artwork.

Provider artwork caching is enabled by default on new installations and works
with either backend. The setup wizard can finish without configuring S3.

## Move artwork to another backend

1. Stop the server's artwork writers.
2. Copy the artwork tree to the target store, keeping the same logical keys.
3. Save the new backend setting.
4. Delete the `artwork.storage_identity` row from `server_settings`.
5. Restart the server and verify artwork loads.

The server refuses to start if the configured storage differs from the recorded
one, including a different bucket or local directory. Clearing the record alone
does not move files. Profile avatars remain in private S3 when configured.
Otherwise they use local storage; the public artwork bucket cannot receive
avatar uploads.
