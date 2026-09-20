# Watch Party synchronization

Postgres owns the room anchor and its `runtime` JSONB value: connection identities,
leased presence, attached playback sessions, readiness, and the latest room
transport command. Each room operation locks its room row and commits the anchor
and runtime changes together. Commands and snapshots are delivered after commit.
Other rooms use independent locks. Playback ownership and catalog lookups happen
before acquiring the room transaction so they cannot exhaust the connection pool
while transactions wait for another pooled connection.

A transport command has one identity across the room. Seek readiness is tied to
that identity and destination, including an attachment during the seek. A new
selection clears previous attachments and readiness. Readiness considers all
attached, connected members across API servers. The existing 30-second waiting
deadline excludes stragglers so one client cannot hold everyone indefinitely.

A connection owns a unique identity. Replacing it fences reports and disconnects
from the old socket. A transient disconnect retains its session for the host's
reconnect grace period; attaching that same session only synchronizes that viewer.
The durable playback-attempt store validates sessions started on another API
server. Legacy playback sessions still use the local session manager.

Presence leases last 45 seconds and are refreshed at most every 15 seconds by the
server holding the socket. An expired lease stops contributing to membership and
readiness. Host disconnect checks use shared presence and the current disconnect
time, so an old server cannot end a room whose host reconnected elsewhere. The
host retains the existing two-minute grace period.

PubSub notifications prompt a refresh of the authoritative row. Every server with
local viewers also reconciles rooms every two seconds, repairing missed
notifications and expiring lost connections. Unchanged reconciliation does not
broadcast another snapshot. SQL work has a five-second timeout; a failed mutation
rolls back and sends no command. This coordinates Watch Party state; it does not
restart a media worker or replace the player's transport recovery behavior.

Each WebSocket has one writer and a queue of 64 frames. A full queue or failed
write closes that socket, allowing the client's existing reconnect flow to
recover. Other viewers' broadcasts do not wait for its write deadline. Each
broadcast builds and sorts the common roster once, then adds each viewer's own
permissions and identity flags.

The web player keeps stalls shorter than 500 ms local. A sustained lack of media
reports buffering and enters the room barrier. Viewer status identifies who is
still buffering or syncing. The timeline remains at the requested seek position
while the player waits for the replacement stream.

## Deployment and clients

Every viewer uses the room's selected source file. For automatic selection, the
server prefers the highest-quality SDR version up to 1080p in the catalogue's
preferred edition and presentation part, then other SDR sources, then HDR. This
avoids unnecessary HDR and 4K conversion requirements; it is a compatibility
preference, not a claim that every viewer's device has been negotiated. Each
viewer still receives a playback plan for their own capabilities. Explicit file
selections retain their existing meaning.

The web player disables version switching while in a room and starts with
`allow_alternate_versions: false`, requiring the playback capability
`fixed_media_file_v1`. That constraint survives every replan, so decoder recovery
cannot silently move one viewer to another timeline. Streaming quality and audio
or subtitle adaptations can still use the same source.

Apply the runtime-column migration before the updated API servers. All API
instances serving a room must run this coordinator; older instances do not
participate in its leases or transactions. Update the complete API fleet before
testing synchronization across nodes. Existing clients may omit readiness command
IDs, but their seek position must still reach the destination. Updated clients
send the command ID so the server can also reject superseded acknowledgements.

The additive member status fields are optional in raw socket frames and HTTP v2
snapshots. Apple has no active Watch
Party implementation; Android's surface remains disabled. Jellyfin compatibility
does not use these room sockets.

The Postgres tests run independent service instances against the same room and
exercise readiness, notification loss, connection replacement, expiration, and
rollback. They do not establish deployed playback behavior; that requires testing
with actual viewers and media transports.
