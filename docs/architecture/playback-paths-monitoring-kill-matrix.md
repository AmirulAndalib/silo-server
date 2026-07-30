# Playback Paths — Monitoring & Kill-Switch Coverage Matrix

> Reference checklist for stream monitoring + revocation kill-switch coverage.
> Check every change to playback/monitoring/revocation against this. As-built
> companion to the design plan
> [`2026-07-04-stream-monitoring-and-kill-switch.md`](../superpowers/plans/2026-07-04-stream-monitoring-and-kill-switch.md)
> (that doc is the intent; this one is the shipped coverage). Status as of
> 2026-07-05: monitoring, enforcement, and the async over-cap enforcer are all
> shipped across two code commits — the *monitoring* commit (`feat(playback):
> server-observed stream monitoring`) and the *kill-switch* commit
> (`feat(playback): stream kill switch + async over-cap enforcer`). GAP-1..GAP-4
> are all RESOLVED (see Findings): GAP-4 (kill list did not survive a restart) was
> closed by wiring a durable Postgres mirror, and GAP-3 (in-flight cut for
> integrated direct-play) was closed by adding `Unwrap()` to the native/compat
> middleware writers so the cut reaches the socket. A post-review hardening pass
> then closed GAP-5..GAP-9 found by adversarial re-review: user-kill cutoff
> semantics, compat manifest/subtitle/mint-bypass coverage, terminate-by-id,
> in-flight download cut, admin-list dedupe, and the metered-writer sendfile chain
> (see Findings). That hardening is folded into the two code commits above — the
> branch is organized as monitoring → kill-switch → this docs commit, not as a
> running series of fix commits. (This doc references sibling commits by role, not
> SHA — squash/rebase rewrites hashes, so a SHA citation goes stale the moment the
> branch is amended.)
>
> **Update 2026-07-29 — two-model review round (Claude Opus 5 + Codex gpt-5.6-sol).**
> A second adversarial pass over the rebased branch found **six open gaps**, none of
> which the branch's own tests caught: GAP-10 (ABS in-flight cuts no-op because
> `statusRecorder` lacks `Unwrap`), GAP-11 (ebook/comic/PDF serving is neither
> observed nor killable), GAP-12 (the rolling write deadline can erase a revocation
> cut), GAP-13 (`mergeStreams` discards edge `BytesServed`), GAP-14
> (transfer-registry saturation serves unmonitored), GAP-15 (edge transcode liveness
> is request-observed, not byte-observed). GAP-10 and GAP-11 in particular mean the
> previous blanket claim that enforcement holds "on every serve surface" was **wrong**.
>
> **Update 2026-07-30 — serve-path batch landed.** **GAP-10, GAP-11, GAP-12 and
> GAP-13 are RESOLVED**; each is scored inline below. GAP-12 was pulled forward from
> the revocation batch because it makes the GAP-10 fix inert (see its entry).
> **GAP-14 and GAP-15 remain open** — GAP-14 to the liveness/replica batch under
> decision A7 (fail closed + connection cap), GAP-15 to the tracker-lifecycle batch.
>
> Two claims elsewhere in this doc are therefore **still not true** and are called out
> where they appear: the kill switch does not "keep a stream dead" (an over-cap kill
> still reopens after 5m until the revocation batch lands), and monitoring does not
> "never trust client progress" (the `LastActivityAt` fallback survives until decision
> A5 lands in the liveness batch). Do not restore the blanket phrasing.
>
> Note also that every "cut" in this document means **cut within ~5s**, not
> immediately: the in-flight watcher polls on a 5s interval.

## Dimensions

- **Server layouts:** (A) integrated, no Redis · (B) integrated, with Redis ·
  (C) multi-node, with Redis (proxy/transcode edge nodes).
- **Playback types:** direct play · remux · transcode (HLS).
- **Routes:** native silo `/api/v1` · jellycompat (`:8096`) ·
  Audiobookshelf-compatible (`/abs`, `/api`, and public feed/session paths).

## Key runtime facts (why cells collapse)

- **Integrated (A, B) serves bytes locally.** Node selection returns an empty
  plan when no edge nodes exist, and both native and jellycompat fall through to
  in-process serving (`nodepool/planner.go:248`, `jellycompat/handlers_playback.go:306`
  — the `proxyNode == nil` guard in `buildProxyRedirectURL`). A vs B differ only
  in *revocation propagation* (memory-only vs Redis) and *monitor source*
  (FuncSource vs MultiSource) — the **serve + enforcement points are identical**.
  So A ≡ B for this matrix; treated as one column "Integrated".
- **Multi-node (C) serves bytes on edges.** Native playback hands the client a
  proxy-node URL at session start (`playback.go:1575`); jellycompat 302-redirects
  to a proxy node (`streams.go:120`). Edge serving = `internal/proxy` + `internal/transcodenode`.
- **jellycompat shares the SessionManager.** A jellycompat play starts a real
  `playback.SessionManager` session (`streams.go:1127`), so the monitor/enforcer
  see it exactly like native. The compat `PlaybackSessionStore` is separate
  bookkeeping (PlaySessionId → recipe), not liveness/enforcement.
- **Local transcode fallback:** in multi-node, if no transcode node is available,
  jellycompat falls back to LOCAL transcode (`streams.go:277`,
  `LocalTranscodeFallbackAllowed`) — i.e. the "Integrated jellycompat" serve path
  can also occur under layout C.

## The matrix

Legend: ✅ covered · ⚠️ covered with caveat · ❌ gap.

### Serving handler (where the bytes come from)

| Route | Type | Integrated (A/B) | Multi-node (C) |
|---|---|---|---|
| native | direct | `StreamHandler.HandleStream` → `ServeDirectPlay` (`stream.go:141`) | proxy `handleDirectPlay` (`proxy/server.go:171`) |
| native | remux | `HandleStream` → `ServeRemux` (`stream.go:157`) | proxy `handleRemux` |
| native | transcode | `HandleGetTranscodeSegment` local → `ServeFile` (`playback.go:2860`) | proxy → transcode node `handleSegment` |
| jellycompat | direct | `HandleVideoStream` → `ServeDirectPlay` (`streams.go:153`) | 302 → proxy `handleDirectPlay` |
| jellycompat | remux | `HandleVideoStream` → `ServeRemux` (`streams.go:151`) | 302 → proxy `handleRemux` |
| jellycompat | transcode | `HandleHLSSegment` → `ServeFile` (`streams.go:543`) | 302 → proxy → transcode node |
| ABS | authenticated file/download | `handleFileStream` → `ServeDirectPlay` | same integrated handler |
| ABS | public playback track | `handlePublicTrack` → `ServeDirectPlay` | same integrated handler |
| ABS | public RSS feed file | `handlePublicFeedFile` → `ServeFile` | same integrated handler |
| ebook | original file | `serveEbookInline` → `ServeContent` (`ebook_reader.go:626`) | same integrated handler |
| ebook | converted EPUB | `serveConvertedEpub` → `ServeContent` (`ebook_convert_serve.go:160`) | same integrated handler |

### Monitoring (server-observed existence, reported to central)

The monitoring model separates **existence** (must be server-observed, never
gated by a client report — the hidden-stream defense) from **timing** (client
progress is fine, especially for native). A disguised re-streamer that pulls
bytes but withholds/falsifies progress must still be counted.

| Route | Type | Integrated (A/B) — existence signal | Multi-node (C) — existence signal |
|---|---|---|---|
| native | direct | ✅ transport-count shield for the whole pour (`stream.go:136`) | ✅ edge tracker, byte-observed (`sessionByteWriter`) |
| native | remux | ✅ transport-count shield (`stream.go:146`) | ✅ edge tracker, byte-observed |
| native | transcode | ✅ per-segment transport marker around `ServeFile` (`playback.go:2860`) | ✅ edge tracker (Touch + AddBytes/segment) |
| jellycompat | direct | ✅ transport-count shield (`streams.go:129`) | ✅ edge tracker, owner+route carried |
| jellycompat | remux | ✅ transport-count shield | ✅ edge tracker, owner+route carried |
| jellycompat | transcode | ✅ per-segment transport marker around `ServeFile` (`streams.go:543`) | ✅ edge tracker, owner+route carried |
| ABS | authenticated file/download | ✅ separate in-memory transfer record; cap-exempt | same process only |
| ABS | public playback track | ✅ native-session transport marker + metered bytes | same |
| ABS | public RSS feed file | ✅ separate in-memory transfer record; cap-exempt | same process only |
| ebook | original / converted EPUB | ✅ transfer record + meter (cap-exempt per A4) | same |

- **GAP-11 — RESOLVED (serve-path batch).** The
  `/api/v1/ebooks/{content_id}/files/{file_id}/read` route group applied only
  `apimw.RequireProfile`, and both serve paths wrapped in `RollingDeadlineWriter`
  alone: no metered writer, no transfer record, no `Refuse`, no `WatchAndCut`. Not a
  text-file-sized path — `ebookReaderFormat` accepts `epub, pdf, mobi, azw, azw3, cbz,
  cbr, fb2, fbz`, and CBZ/CBR archives and scanned PDFs routinely run 100 MB–1 GB+, so
  a revoked user kept pulling the whole comic library at full speed while the admin view
  showed nothing.

  Now metered, registered in the transfer registry, refused on entry and cut in flight,
  following the ABS file-handler idiom. **Cap-exempt per decision A4** — admission is
  untouched and reading consumes no video stream slot.

  ⚠️ **Correction to the earlier follow-up guidance, which said to "reuse
  `guardRevocationCut`" here — do not.** That wrapper keys on a `session_id` URL param
  this route does not have (it would pass `""`) and takes identity from
  `streamRequestIdentity`, which looks for a `?st=` stream token this route never
  carries. It compiles and silently guards nothing. The identity has to come from the
  profile/auth context instead.
- **GAP-13 — RESOLVED (serve-path batch).** `streammonitor.mergeStreams` picked the
  record with the later `LastServedAt` *wholesale* and backfilled only
  `UserID`/`ProfileID`/`MediaFileID`, `Route`, `ClientIP`, `ClientName`, `HWAccel` and
  `Position` — **`BytesServed` was not merged**, so when the central record was fresher
  a stream whose bytes were all poured at an edge reported `0`. Now merged as a **max,
  not a sum**: the two records are two observers of one pour, so summing would
  double-count. `DedupeSessionInfos` had the identical hole and feeds the admin view;
  fixed there too.
- ⚠️ **GAP-14 — registry saturation serves unmonitored (open).** The transfer
  registry is capped at `defaultMaxEntries = 10_000`
  (`transfers/registry.go:16`). Past that, `Begin` returns `ErrRegistryFull` and all
  four call sites (`downloads/service.go:1103`, `jellycompat/streams.go:249`,
  `abs/file_handler.go:150`, `abs/rss_feeds_handler.go:260`) log at **Debug** and
  serve anyway; byte updates for the unregistered pour are discarded
  (`registry.go:122`). Since there is no connection cap anywhere (abuse matrix E28)
  and the compat surface is unthrottled (E25/E27), an attacker can hold the registry
  at its ceiling and blind download-class monitoring for every other user. The
  registry's own once-per-minute `Warn` is the only operator signal.

  **Decision A7 — still open, scheduled for the liveness/replica batch: fail closed,
  plus a connection cap.** A per-user/credential concurrent-connection cap makes
  saturation unreachable by one actor, *and* download-class pours are refused rather
  than served unobserved if it saturates anyway. Until that lands, "no invisible
  streams" silently stops being true under load — do not claim otherwise.
- **Existence is server-observed on every cell** *except the ebook rows above*.
  Integrated: a session is
  unreapable while `activeTransportCount > 0`, and every byte-serving path now
  holds that marker — direct/remux for the whole pour, transcode for each segment
  serve (the segment markers were added to close a hidden-stream hole where a slow
  single-segment drain past the 45s grace could be reaped mid-serve; the compat
  HLS path previously refreshed liveness *only* from the client's progress POST,
  a direct violation, now also transport-marked). Edge: a Redis record exists for
  the whole connection (direct/remux) or while segments are pulled (transcode),
  advanced only by real bytes. **No path requires a client progress report to stay
  visible or counted.**
- **Timing is secondary and may trust the client only for position.**
  `Session.LastServedAt` is deliberately distinct from `LastActivityAt` and is
  advanced only by server-observed bytes or transport begin/end. Client progress
  can still advance `LastActivityAt`, preserving reaping semantics, but cannot
  influence the enforcer's victim ordering. Integrated `BytesServed` and
  `LastServedAt` are mapped alongside the edge equivalents. On the central side this
  holds exactly: `Session.LastServedAt` is written only by `BeginTransport`,
  `EndTransport` and `AddServedBytes` (`playback/session.go:1132,1151,1169`) — no
  progress-report path touches it.
  ⚠️ **One edge exception (GAP-15, open):** the proxy's transcode path calls
  `touchTranscodeSession` immediately after token verification and *before* proxying
  to the node (`proxy/server.go:308,317`), so `Touch` advances the edge record's
  `LastServedAt` even when the node returns a 404 and no media byte is served. Edge
  transcode liveness is therefore **request-observed, not byte-observed**. The blast
  radius is bounded: the enforcer groups by user and keeps the freshest sessions
  (`streamenforcer/enforcer.go:140`), so a client hammering dead segment URLs can
  only change *which of its own* over-cap streams gets trimmed — it cannot exceed the
  cap or shield another account. Worth fixing so the claim above is literally true.
- Report-to-central: **C** = async via Redis (edge writes `silo:sessions:*`,
  `streammonitor.RedisSource` reads). **A/B** = in-process SessionManager IS
  central; read via `streammonitor.FuncSource`. MultiSource unions both, deduped
  by session id.
- Owner + attribution: the transcode node's start record now carries owner + route
  + client (threaded via `TranscodeStartRequest`), and `streammonitor.mergeStreams`
  additionally backfills any missing owner/route/client from another record for the
  same session — so an ownerless-but-freshest node record can never bucket a stream
  under user 0 (which the enforcer skips) or drop route/client from the view.

### Monitoring data model (what central sees per stream)

First-class monitoring goal: see **all** active streams with enough to act on.
Captured per live stream (`streammonitor.LiveStream` / `nodesessions.SessionInfo`):

| Field | Source | Notes |
|---|---|---|
| existence, session id | both | primary; server-observed |
| user id, profile id | token claims / Session | primary; owner attribution |
| media file id | claims / Session | primary; human title is a display-time lookup (follow-up) |
| play method (direct/remux/transcode) | `Type` | primary |
| **route (native/jellycompat)** | `Route` — `Session.Origin` (integrated) or token `Origin` claim (edge) | primary; native and jellycompat share the SessionManager and `Type`, so route is a distinct field seeded at the two `WithClientInfo` sites and carried to edges in the token |
| node serving | NodeName/NodeURL | integrated stamps the local host |
| client ip / client name | `Session.ClientIP/ClientName` (integrated) or edge request + `ClientName` claim | secondary; also the natural re-streaming fingerprint |
| position | `Session.Position` | secondary timing |
| bytes served | edge `AddBytes` / integrated `SessionMeteredWriter` | server-observed throughput signal |
| hw/sw, resolution, codecs | Session / node record | secondary |

- **Admin visibility:** `HandleListSessions` unions the Redis edge records with the
  in-process integrated sessions (`LiveLocalSessions`), so a single-node integrated
  deployment is no longer blind (previously Redis-only). The union is deduped by
  session id (`streammonitor.DedupeSessionInfos`, mirroring `mergeStreams`) so a
  stream tracked by both the central manager and the edge serving it shows as ONE
  row — matching the enforcer's count. A `node_id` filter targets
  an edge, so integrated sessions appear only in the unfiltered listing.
- **Download visibility is a separate sibling collection.** The same unfiltered
  admin response includes `transfers`, sourced from a bounded process-local
  registry. Transfer rows contain a unique per-pour id, optional download-row
  correlation id, owner/profile, media file, route, client metadata, timestamps,
  and server-observed bytes. A `node_id` filter excludes them because they belong
  to this API process, not an edge. The byte timestamp advances in coarse metered
  chunks (1 MiB or final close), and multi-replica deployments see only the
  registry of the process answering the request.
- **Downloads are an intentional exemption — with asymmetries.** `/downloads/*`
  (native), compat `/Items/{id}/Download`, and ABS authenticated file/download
  and RSS feed serves transfer full media without a live-stream session and do NOT
  count against the live-stream cap. They now create separate, in-memory transfer
  records while a pour is active. Native download quotas run only when a download
  row is created; neither `ServeDownload` nor `ServeDirect` gates a pour, and
  compat/ABS routes have no shared download quota. All routes arm the shared
  `WatchAndCut`, and the same hook deletes every compat login so reconnects need
  re-auth. **Arming it was previously not the same as it working:** on ABS routes the
  cut could not reach the socket at all (GAP-10) and on the native download routes it
  was undone by the rolling write deadline (GAP-12). Both are now fixed, so "a per-user
  stream revocation cuts an in-flight download pour" holds on every one of these routes
  — within one ~5s watch tick.

### Kill switch (revocation enforced on the serve path)

| Route | Type | Integrated (A/B) | Multi-node (C) |
|---|---|---|---|
| native | direct | ✅ `guardRevocationCut` (Refuse + in-flight cut†) | ✅ `verifyToken` + `cutOnRevocation` (in-flight cut) |
| native | remux | ✅ `guardRevocationCut` (Refuse + cut†) | ✅ verifyToken + cutOnRevocation |
| native | transcode | ✅ `guardRevocation` (refused within one segment) | ✅ proxy verifyToken + node `refuseIfRevoked` + reconstruct guard‡ |
| jellycompat | direct | ✅ `Refuse` + in-flight cut† (GAP-1/3 fixed) | ✅ via proxy; ✅ local fallback guarded |
| jellycompat | remux | ✅ `Refuse` + in-flight cut† | ✅ via proxy; ✅ local fallback guarded |
| jellycompat | transcode | ✅ `Refuse` per segment (GAP-1 fixed) | ✅ via proxy/node; ✅ local fallback guarded |
| ABS | authenticated file/download | ✅ bearer JWT `iat` owner cutoff refuses on entry + in-flight cut† (GAP-10 fixed) | same |
| ABS | public playback track | ✅ native session id + session `StartedAt` owner cutoff + cut† (GAP-10 fixed) | same |
| ABS | public RSS feed file | ✅ feed `CreatedAt` owner cutoff refuses on entry + cut† (GAP-10 fixed) | same |
| ebook | original file / converted EPUB | ✅ `Refuse` on entry + in-flight cut† (GAP-11 fixed) | same |
| native | subtitles | ✅ `guardRevocationCut` (Refuse + cut†) | ✅ verifyToken + cutOnRevocation |
| jellycompat | subtitles | ✅ `Refuse` + cut† (delivery only — extraction is buffered) | same |

† In-flight cut uses `streamrevoke.Store.WatchAndCut` (SetWriteDeadline, checked on
entry then every `Options.WatchInterval` — **5s in production**, so a cut lands within
~5s, not instantly). Works at the edge (the proxy's metered writer implements
`Unwrap`) **and** on the native/compat integrated surfaces: the native
(`statusWriter`, `requestStatusWriter`) and compat (`loggingResponseWriter`,
`compatImageProxyTagResponseWriter`, `debugResponseWriter`) middleware writers
implement `Unwrap()`, so `http.NewResponseController` reaches the socket instead of
no-oping (see GAP-3). chi's own `middleware.Compress` wrapper also implements
`Unwrap()` (chi v5.2.5 `middleware/compress.go:374`), so the globally-mounted
compressor does not break the chain either. ABS's `statusRecorder` now implements it
too (GAP-10), and a **latch** on the request context stops any
`RollingDeadlineWriter` downstream of the cut from re-arming the socket (GAP-12).

**The audit that produced that list originally missed the ABS surface — see GAP-10.**
Any new middleware that wraps a byte-serving route must implement `Unwrap()`, and the check
belongs in a test that exercises the *mounted* router, not the handler in isolation.

**GAP-10 — RESOLVED (serve-path batch).** Every ABS route is wrapped by `accessLog`
(`audiobooks/abs/handler.go:334`), whose `statusRecorder`
(`audiobooks/abs/access_log.go`) implemented `Write`, `WriteHeader`, `Hijack` and
`Flush` but **not** `Unwrap()`. The chain `SessionMeteredWriter → statusRecorder`
dead-ended, `http.NewResponseController(...).SetWriteDeadline` returned
`ErrNotSupported`, and `WatchAndCut` discarded that error and silently stopped
watching — a multi-GB ABS pour started before a `RevokeUser` ran to completion.
Fixed by adding `Unwrap()`. `WatchAndCut` now **logs** a failed `SetWriteDeadline`
(once per watcher) instead of discarding it, so the next wrapper of this shape is
loud rather than invisible.

The old test passed throughout because `audiobooks/abs/revocation_test.go` called the
handlers directly and never saw the middleware. The replacement drives the **mounted**
router over a **real socket**; it and the compile-time `Unwrap` assertion were both
confirmed to fail when `Unwrap()` is removed again, so this cannot regress silently.

**GAP-12 — RESOLVED (serve-path batch).** Was scoped to Batch 3, but pulled forward
because it makes the GAP-10 fix *inert*: adding `Unwrap()` is what makes
`SetWriteDeadline` start succeeding, which is also what makes
`RollingDeadlineWriter.bump()` start succeeding — and `bump()` pushed the deadline
back out to `now + StallWindow` (180s) once `bumpStep` (15s) had elapsed, plus once
more from its own constructor. Fixing GAP-10 alone would have left the ABS kill switch
broken, just differently.

The obvious fix does not work and was rejected: the rolling writer is constructed
*inside* `ServeDirectPlay`/`ServeRemux` and **wraps** the writer `WatchAndCut` holds,
so it sits *above* the watcher. `Unwrap()` walks toward the socket, so the watcher can
never reach it by writer introspection. The cut has to travel by a side channel.

Fixed with `httpstream.CutLatch`, carried on the request context and consulted by
`bump()`. Once latched the writer never extends the deadline again. `bump()` re-checks
the latch *after* setting a future deadline so a concurrent cut cannot be lost to the
check/set race, and `WatchAndCut` keeps re-applying the deadline each tick rather than
returning after the first cut, as belt-and-braces for any topology the latch misses.

**Known bound, by design:** the watcher polls, so a revoked pour keeps delivering for
up to one watch interval — **5s in production** — before the cut lands. On a fast link
that is a meaningful amount of data. Read every "cut" claim below as "cut within ~5s",
not "cut immediately". `Options.WatchInterval` makes the interval injectable so
real-socket tests do not have to wait it out.

‡ The transcode node's serve-path check is session-only (`refuseIfRevoked` passes
`userID = 0`), so a per-*user* kill is enforced by the fronting proxy's `verifyToken`
(which passes `claims.UserID`), not at the node's segment serve. The node's
reconstruct guard *does* use the real `claims.UserID`, so a rebuild-after-restart of
a user-killed session is blocked.

### Restart durability (kill survives a process restart / Redis loss)

This axis exists because the branch sits on top of PR #174 (restart-resilient
playback): a session killed before a restart is **reconstructed** afterward from a
durable recipe card, so if the kill list does not also survive the restart the
reconstructed stream is silently re-served. The kill list must be at least as
durable as the thing it kills.

| Deployment | Session survives restart (PR #174) | Kill survives restart | Kill survives Redis flush |
|---|---|---|---|
| A — integrated, no Redis | ✅ recipe card (PG) | ✅ durable PG mirror | ✅ (never used Redis) |
| B — integrated, with Redis | ✅ | ✅ durable PG mirror (+ Redis warm) | ✅ durable PG mirror |
| C — multi-node, with Redis | ✅ | ✅ central durable PG mirror; edges re-warm from Redis | ✅ central re-warms edges from PG→Redis on next write/poll |

\* Steady-state guarantee. Transient boot caveat: if the durable warm exhausts its
bounded retry **and** Redis is empty, the kill list is empty until the first poll
tick (≤60s). This fails *open* by design — see the "Warm is async-tolerant" bullet
below.

- **Hot path unchanged.** `IsRevoked` is still a pure in-memory map read. Postgres
  is touched only on write (`Upsert`), on warm/reconcile (`ListActive` at
  `StartSync` and on the poll tick), and on trim (`Prune`) — never per request.
- **Central-side only.** The durable mirror is wired into the integrated/api
  `streamrevoke.Store` (`cmd/silo/main.go`), not the edge/proxy/transcode nodes,
  which have no app DB and enforce via Redis pub/sub + poll.
- **Bounded growth.** Rows are keyed by `(kind, id)`, so the async enforcer
  re-revoking every pass UPSERTs one row rather than accumulating; expired rows are
  physically reclaimed by `Prune` on the poll tick, and `ListActive` filters on
  `expires_at > now()`.
- **Expiry is monotonic.** Both the in-memory `applyLocal` and the durable
  `Upsert` keep whichever copy expires LATER (`GREATEST`), never shortening an
  existing kill. This matters because the async over-cap enforcer re-revokes with a
  short 5m self-heal TTL on the same `KindSession` key an admin may have killed for
  24h; without monotonic expiry the enforcer would silently shrink the admin kill
  to 5m and reopen the restart-resurrection window this whole axis defends against.
- **Warm is async-tolerant.** By product decision, a just-reconstructed revoked
  stream may serve a few seconds before the durable warm/next Refuse tick cuts it;
  no blocking startup ordering is required. Edge caveat: if the boot durable warm
  exhausts its bounded retry **and** Redis is empty, the kill list is empty until
  the first poll tick (≤60s). This fails *open* by design (a kill switch cannot
  fail closed without blocking all playback when the DB is briefly unavailable).
- **Edge Redis-outage fails open.** Edge nodes (proxy/transcode) have no durable
  store and learn *new* kills only from Redis pub/sub + SCAN. While Redis is
  unreachable, already-cached kills persist but a kill issued *during* the outage
  does not reach the edge until Redis recovers — enforcement fails open there.
- **Kills are operator-visible and undoable.** Admins can list, create, and
  remove entries with `GET/POST /api/v1/admin/streams/revocations` and
  `DELETE /api/v1/admin/streams/revocations/{kind}/{id}`. `Unrevoke` deletes the
  local, durable, and Redis copies and publishes an unrevocation event. A bounded
  tombstone prevents a delayed reconcile from resurrecting the removed kill,
  while a later legitimate revoke clears the tombstone. If unrevocation publish
  fails, other processes retain the kill until expiry: the residual failure is
  safe (dead rather than unexpectedly live) and is reported as a warning.
- **Durable self-heal compares expiry.** The maintenance pass re-upserts a local
  entry when the durable copy is missing or expires earlier, repairing a failed
  longer mirror instead of treating mere row presence as healthy.

## Findings

> GAP-1..GAP-3 all shipped in the kill-switch commit (see the Status banner for
> how commits are referenced by role rather than SHA).

**GAP-1 — RESOLVED.** jellycompat LOCAL serving now consults the
shared `Store.Refuse` in `HandleVideoStream` (direct/remux) and `HandleHLSSegment`
(per segment), closing the integrated / local-transcode-fallback hole.

**GAP-2 — RESOLVED.** `buildProxyRedirectURL` now stamps
`uid`/`pid`/`mfid` onto the jellycompat proxy-redirect token (looked up from the
upstream SessionManager session), so edge monitoring/kill attribution matches
native. `AuthUserID = 0` no longer occurs for jellycompat.

**GAP-3 — RESOLVED.** The in-flight cut is shared as
`streamrevoke.Store.WatchAndCut` and applied to native `/stream`
(`guardRevocationCut`) and jellycompat `HandleVideoStream`. The original caveat —
`SetWriteDeadline` might not reach the socket through the native/compat middleware
chain — was **confirmed** as a guaranteed integrated no-op (four middleware writers
wrapped `w` without `Unwrap()`) and then fixed by adding `Unwrap()` to
`statusWriter`, `requestStatusWriter`, `loggingResponseWriter`,
`compatImageProxyTagResponseWriter`, and `debugResponseWriter`. The cut now reaches
the socket in integrated mode; if it ever no-ops again the stream still stops on
its next request via `Refuse` (no regression). See VERIFY-1.

**GAP-4 — RESOLVED.** The kill list did not survive a process restart or a Redis
flush, but PR #174's restart-resilient playback *does* reconstruct the session — so
a stream revoked before a restart was silently re-served afterward. This was
universal in deployment A (integrated, no Redis: the kill list was pure RAM) and
occurred on any Redis loss in B/C. Fixed by wiring a concrete Postgres
`DurableStore` (`internal/streamrevoke/durable_postgres.go`, table
`stream_revocations`) into the central `streamrevoke.Store`: `Revoke` mirrors to
Postgres, `StartSync` warms from it on boot, and the poll tick re-warms (heals a
Redis flush) and `Prune`s expired rows. The `defaultTTL` (24h) is held `>=` the
recipe-card `MaxTokenTTL` (24h) as an invariant so a kill cannot expire before its
session can be reconstructed, and expiry is **monotonic** on every write (in-memory
`applyLocal` + durable `Upsert` `GREATEST`) so the async enforcer's short 5m
re-revoke can never shorten a longer admin kill on the same session key. Hot path
(`IsRevoked`) stays an in-memory read.

**GAP-5 — RESOLVED (post-review).** User-kind revocations were a blanket ban:
any admin user edit (via `OnUserSessionsRevoked`) 403'd that user's playback for
24h even after re-login, unrevocably. Fixed with cutoff semantics — see
"user-ID kills" above.

**GAP-6 — RESOLVED (post-review).** Compat coverage holes: `HandleMasterManifest`
/ `HandleHLSManifest` had no revocation check and kept (or, post-restart,
re-spawned) a killed session's ffmpeg via the ensure path; `HandleVideoStream`
checked revocation only after `ensureUpstreamPlayback`, which can replace an
unreconstructable killed session with a fresh id that passed the check (kill
dodged by re-hitting the URL); `HandleSubtitleStream` ignored kills entirely.
All four now `Refuse` up front.

**GAP-7 — RESOLVED (post-review).** Admin terminate 404'd before revoking when
the session was absent from the central in-memory manager — exactly the
edge-served / post-restart / progress-withholding streams the kill switch
exists for (the admin list showed them via the Redis union; terminate couldn't
touch them). Terminate now writes the revocation FIRST, keyed on the session id
alone, and answers `202 {status: "revoked"}` when there is no local session for
the cooperative command.

**GAP-8 — RESOLVED (post-review).** The admin session list double-counted every
edge-served stream (central manager row + edge Redis row, no dedupe), unlike
the enforcer's merged picture. Now deduped by session id with owner/attribution
carry-forward (`streammonitor.DedupeSessionInfos`).

**GAP-9 — RESOLVED (post-review).** The "restore sendfile" commit was dead code
end-to-end: every stream route runs inside `meterEgress`, and
`meteredResponseWriter` hid `io.ReaderFrom`, so `sessionByteWriter.ReadFrom`'s
fast path never fired and all direct-play/remux bytes went through a userspace
copy. `meteredResponseWriter` now forwards `ReadFrom` (metering the returned
total); a chain test locks the full production writer stack onto the sendfile
path. Related tracker fixes in the same pass: `Touch`/`Track` preserve the
first-seen `StartedAt` (was reset per segment/range request, corrupting the
enforcer's tie-break and the admin start time), `AddBytes` no longer recreates
entries for pruned sessions (slow permanent leak), the proxy→node segment pour
attributes bytes incrementally (a slow drain can't go invisible mid-segment or
post bytes to a dead record), and the transcode node marks serve activity so
its record's `LastServedAt` is no longer frozen at start time.

**GAP-3 (original, superseded) — no in-flight cut for native/compat LOCAL direct-play.**
The original finding: `guardRevocation` refused the next request/reconnect but could
not hang up an in-flight `ServeFile` on the central process for a single long GET.
Now closed — see GAP-3 above (shared `WatchAndCut` + middleware `Unwrap()`). Even
if the cut degrades, admin terminate also fires the realtime command + session stop,
so a killed direct-play stops on its next request at worst.

## Open verification items (check before prod)

- [x] **VERIFY-1 — in-flight cut reaches the socket on native + jellycompat — RESOLVED.**
  `streamrevoke.Store.WatchAndCut` cuts a revoked long-GET direct-play/remux by
  setting a zero write deadline via `http.NewResponseController(w)`, which walks the
  writer chain via `Unwrap()`. This was **confirmed broken** in integrated mode:
  `statusWriter`/`requestStatusWriter` (native) and `loggingResponseWriter`/
  `compatImageProxyTagResponseWriter`/`debugResponseWriter` (compat) wrapped `w`
  without `Unwrap()`, so `SetWriteDeadline` returned `ErrNotSupported` and the cut
  no-oped. Fixed by adding `Unwrap() http.ResponseWriter` to all five (pattern from
  `activitylog/middleware.go:186`). A live smoke test is still worthwhile: start a
  native and a jellycompat single-long-GET direct-play, admin-terminate it, confirm
  the connection drops within ~5s rather than only on the next request.
- [x] **VERIFY-2 — download routes vs the stream cap — RESOLVED (documented exemption).**
  Native, compat, and ABS download-class routes remain intentionally exempt from
  the live-stream cap but are visible in the sibling, process-local `transfers`
  array. Native concurrency/period checks happen only at row creation, not at
  serve time. A per-user revocation is a cutoff: it refuses/cuts older pours but
  deliberately allows a new authenticated pour started after the cutoff.
- [x] **VERIFY-4 — transcode buffer-ahead evasion — RESOLVED.** Unpaused
  transcodes use a bounded 10-minute integrated grace measured from the same
  activity resolution advanced by server-observed bytes/transports; paused
  transcodes retain the longer 30-minute paused grace. Edge transcode records
  use a 180-second idle window consistently in `ActiveCount`, `Snapshot`, and
  refresh. This deliberately avoids a local ffmpeg liveness probe, which cannot
  represent offloaded or completed copy-mode transcodes.
- [ ] **VERIFY-3 — multi-replica central enforcement.** The async enforcer runs on
  every integrated/api process. Two *integrated* replicas behind a load balancer
  each see only their own in-process `FuncSource` (integrated mode writes no
  `silo:sessions:*` records), so a user split across both can exceed `max_streams`
  undetected; multiple *api* replicas each run an independent enforcer and revoke
  the same victims (idempotent, but redundant write load). A single-leader election
  for the enforcer, or having integrated nodes publish their local sessions to the
  shared Redis picture, would close this.

## Design assessment (shared vs duplicated code)

**Kill DECISION is well-centralized — keep it.** All revocation state and the
`IsRevoked(sessionID, userID)` check live in one place (`internal/streamrevoke`).
Session kills and user kills use the SAME store and the SAME check — `RevokeSession`
and `RevokeUser` write into one `items` map; `IsRevoked` tests both keys. No
duplication at the decision layer.

**Kill ENFORCEMENT — DONE.** The "extract sid/uid → refuse (403)"
step is now a single shared method `streamrevoke.Store.Refuse(w, sessionID, userID)
bool`. All four serve surfaces call it (proxy `verifyToken`, transcode node
`refuseIfRevoked`, native `guardRevocation`, jellycompat serve handlers); each only
owns its token extraction (path `{token}`, query `st`, or compat `PlaySessionId`).
The in-flight connection-cut is the shared `WatchAndCut` (SetWriteDeadline), used by
both the edge (`cutOnRevocation`) and the native/compat long-pour paths. ONE section
per concern, as intended.

**Monitoring WRITE side is two implementations by necessity — leave it.** Edges
have no SessionManager; the integrated process has no tracker. `nodesessions.Tracker`
(edge) and `SessionManager` (integrated) are different runtimes, not duplicated
logic, and they already converge at the single read layer (`streammonitor`,
unioned by MultiSource). Forcing one implementation would be worse.

**user-ID kills — plumbing check (as asked).** Two distinct layers, correctly
separate:
- *Auth/login revocation* (pre-existing): `OnUserSessionsRevoked` revokes native
  auth sessions (`auth.SessionRepository.RevokeAllByUser`) and drops compat login
  tokens (`SessionStore.DeleteByUserID`). This governs "can this user authenticate",
  not "stop this byte stream".
- *Stream revocation* (new): `streamRevocation.RevokeUser` writes a `KindUser`
  entry into the SAME `streamrevoke` store as session kills. Session-level and
  user-level STREAM kills already share all plumbing (one store, one `IsRevoked`).
- **A user kill is a CUTOFF, not a ban (GAP-5 fix).** `IsRevoked` matches a
  `KindUser` entry only when the stream's credential predates the revocation:
  the stream token's `iat` on token-bearing surfaces (edge proxy, transcode
  node, native `?st=`), the request entry time on freshly-authenticated
  surfaces (native session auth, compat login — every pre-revocation login is
  reset by the same hook, so reaching a serve path afterward proves fresh
  auth). An in-flight pour always predates a later revocation, so mid-pour
  user kills still cut. An unknown (zero) credential time never matches (fail
  open). This is what makes it safe for `OnUserSessionsRevoked` — which fires
  on ANY admin edit of password/role/enabled/permissions/quality — to also
  write a stream kill: without the cutoff, a routine permission tweak 403'd
  the user's playback for the full 24h TTL after re-login unless an operator
  explicitly removed the kill.
- These two layers should NOT be merged — conflating "may log in" with "this
  stream must die" would couple auth and playback. They are chained in the same
  hook, which is the right seam.

## Recommended follow-up order

GAP-1, GAP-2, GAP-3, and GAP-4 are all shipped (see Findings). Already-shipped
hardening not to re-do: `Revoke` propagation uses `context.WithoutCancel`, so an
aborted admin request can no longer strand a kill in central memory only.
Remaining work:

1. **Operator config keys (small):** wire `auth.stream_revocation_poll` /
   `auth.stream_revocation_ttl` through the settings pipeline; defaults (60s / 24h)
   are currently hardcoded in `internal/streamrevoke/store.go`.
2. **Media title enrichment:** resolve `MediaFileID → title` at admin display time
   so the view shows *what* is being watched, not just a numeric id (also enables a
   distinct-title re-streaming heuristic).
3. **Multi-replica enforcement (VERIFY-3, explicitly deferred):** single-leader election for the async
   enforcer, or have integrated nodes publish local sessions to the shared Redis
   picture so the cap holds across replicas.
4. **Invariant guard test:** `defaultTTL >= playback.MaxTokenTTL` is coupled only by
   a prose comment (streamrevoke can't import playback — import cycle). Add a test
   in a package that can import both, so raising `MaxTokenTTL` can't silently
   violate it.
5. **Transcode-node ghost records (explicitly deferred):** the node's session-backed `Track` record is
   refreshed forever until an explicit stop/cleanup; if central dies or loses the
   session without the stop reaching the node, an owner-attributed ghost persists
   in Redis indefinitely — permanently +1 in the user's live count (the enforcer's
   freshness ordering trims the ghost first, so real streams survive, but the
   enforcer re-revokes it every pass and the admin view overcounts). Needs a
   node-side idle sweep tied to real serve activity (`MarkServed` now provides the
   signal).
6. **Download controls phase 2:** add per-download kill and a standing per-user
   download block (a user revocation is only a cutoff and permits newer
   credentials/requests). Separately unify native, compat, and ABS under a shared
   download quota and rolling volume budget. Also resolve the pre-existing native
   completion semantics (`ServeContent` cannot report write failure), correlate
   ABS playback sessions with duplicate `abs_file_stream` transfer rows, and make
   transfer visibility multi-replica rather than process-local.
