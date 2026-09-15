# Artwork storage

Artwork writes and cleanup use `internal/artworkstore.Store`. The store owns
its filesystem root or S3 bucket; callers use the existing logical artwork keys.

## Backends

`artwork.storage_backend` accepts `auto`, `local`, or `s3`. `auto` selects S3
when the public bucket is configured and local storage otherwise. The local
root defaults to `/var/lib/silo/artwork`; containers must persist that directory.
S3 is recommended when multiple hosts serve the same catalog.

Only API and integrated processes open artwork storage. Worker processes do not
probe it or compare the catalog's recorded backend with their local settings.
Startup probes the selected backend with a five-second timeout. Temporary storage
failures allow the process to start with degraded readiness; invalid paths and
backend mismatches remain startup errors. Readiness repeats the probe at most
once every 30 seconds, independently of a caller disconnecting. A local probe
writes, syncs, and removes a temporary file.

## Store contract

`Put` atomically overwrites an object. `Get` and `Stat` return object size,
modification time, and a quoted ETag. Missing objects return `ErrNotFound`.
`Delete` counts absent keys as deleted, and prefix deletion removes a subtree.
Listings use lexical keys and a cursor equal to the last returned key. Local
pagination skips completed subtrees and stops after a page plus one object;
each visited directory's entries are read and sorted in memory.

Keys are relative, non-empty, and at most 1024 bytes. Empty segments, dot
segments, backslashes, and control characters are rejected. Local storage
refuses symlinks and non-regular files below its root. MIME types come from key
extensions. The `.tmp-` and `.probe-` filename prefixes are reserved. Listings
reclaim abandoned temporary files older than 24 hours; readiness also cleans
temporary files in the root. Cleanup preserves files locked by active writers.

## Changing backend

The first successful write records `artwork.storage_backend_active` in
`server_settings`. Startup refuses a different resolved backend. To move:

1. Stop artwork writers.
2. Copy the artwork tree to the new store, preserving logical keys.
3. Update the backend configuration.
4. Delete the `artwork.storage_backend_active` row and restart.

This guard does not migrate data. There is no portability format, storage
health state machine, generation marker, or mount sentinel. Existing revision
tracking, reconciliation, and garbage collection continue to own lifecycle.

## Delivery

Local URLs use an HMAC derived from the JWT secret and the fixed domain
`silo-artwork-url-v1`. The signature covers `artwork-v1`, the logical key, and
the expiry. URLs stay stable within issuance buckets of 15 minutes, reduced to
the TTL for shorter URLs. Their remaining lifetime is at least the configured
TTL, with up to one bucket added. Invalid or expired capabilities return 404 so
the route does not reveal whether a key exists.
Revisioned URLs are cacheable for their remaining lifetime and marked immutable;
mutable uploads use private caching. S3 installations continue to use direct
presigned or public URLs.

Profile avatars use private S3 whenever it is configured, preserving existing
uploads even when catalog artwork uses local storage. Without private S3,
avatars can use local artwork storage with signed delivery. A public artwork
bucket alone does not enable avatar uploads. Avatar URL generation does not
probe storage.
