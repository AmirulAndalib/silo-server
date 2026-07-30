# Stream & API Abuse — Coverage Matrix

> **What this is.** A red-team assessment of how the `feat/sauron-async-enforcer`
> branch (server-observed stream monitoring + revocation kill switch + async
> over-cap enforcer) — layered on the current `main` — reacts to ~29 concrete
> abuse stories. Each story is scored on two axes the branch itself separates:
>
> - **Detection** — does the server *see* the abuse and *label it correctly*
>   (right user, right stream, right kind)?
> - **Enforcement** — can the server *stop* it, and *how fast*?
>
> **Companion docs:** the intent lives in
> [`../superpowers/plans/2026-07-04-stream-monitoring-and-kill-switch.md`](../superpowers/plans/2026-07-04-stream-monitoring-and-kill-switch.md);
> the as-built coverage-by-path lives in
> [`playback-paths-monitoring-kill-matrix.md`](playback-paths-monitoring-kill-matrix.md).
> This doc is the *adversarial* companion: it assumes a motivated abuser and asks
> where the design ends.
>
> **Scope note.** The branch is scoped to **concurrent playback-stream abuse**. A
> large fraction of real-world "abuse" (library enumeration load, bulk ripping,
> API request floods, CPU exhaustion, re-broadcasting) lives *outside* that scope.
> Where that is the case this doc says so plainly and points at the subsystem that
> would actually own the defense (`internal/ratelimit`, `internal/downloads`,
> catalog browse, node scheduling) — several of which have real gaps of their own.

## Legend

| Symbol | Detection | Enforcement |
|---|---|---|
| ✅ | Seen and correctly attributed | Stopped (and how fast) |
| ⚠️ | Seen but partial / mislabeled / lossy | Partial, slow, best-effort, or config-dependent |
| ❌ | Invisible to the server | Cannot stop |

Enforcement latency budget for the async path is **~120s** = enforcer tick
(`DefaultInterval = 30s`, `streamenforcer/enforcer.go`) + revocation propagation
(pub/sub push immediate; ≤60s poll safety net, `streamrevoke/store.go`) + in-flight
cut (5s `WatchAndCut` ticker for long pours; next segment for HLS). Synchronous
admission refusal (`ErrTooManyStreams`, `playback/session.go`) is **immediate**.

---

## Corrections the investigations turned up (read first)

The branch docs oversell several things. The stories below are scored against the
**code**, not the plan.

> **Round 2 — 2026-07-29, two-model review (Claude Opus 5 + Codex gpt-5.6-sol).**
> Corrections 4–8 below were found by a second adversarial pass over the rebased
> branch and are **open defects, not doc drift**. They matter because they hit the
> two stated priorities directly: #1 "see all bytes, no invisible streams"
> (corrections 5, 7, 8) and #2 "kill any stream" (corrections 4, 6). Affected rows in
> the summary matrix have been downgraded accordingly.

1. **The async enforcer uses the effective, group-merged limit.** An earlier
   audit found it reading raw `users.max_streams`, which is zero ("inherit") for
   standard accounts. That is no longer true: its limit function calls
   `SessionManager.EffectiveLimits`, the same group-merged policy used by
   admission. Standard users therefore receive the Default Group cap instead
   of being treated as unlimited.

2. **The re-streaming heuristic does not exist.** The plan describes shipping
   `OverCapRule` + a disabled `RestreamHeuristicRule` behind a pluggable `Rule`
   interface. In code, `streamenforcer/enforcer.go` is a single hardcoded over-cap
   loop — no rule interface, no restream logic, not even a disabled stub. The
   fingerprints a heuristic *would* need (`ClientIP`, `ClientName`, `MediaFileID`,
   `BytesServed`) are captured in `streammonitor` but nothing consumes them for
   detection.

3. **There is no server-wide or per-node transcode/CPU cap.** Only a per-*user*
   `max_transcodes` (default 2, `<=0` = unlimited) checked at admission. The one
   ffmpeg semaphore (`reconstructSem`, sized to `NumCPU`) guards **only** the
   restart-reconstruct path in `transcodenode/server.go`, **not** fresh starts.

> **Update 2026-07-30 — serve-path batch landed.** Corrections **4, 5, 6 and 7 below
> are now FIXED** (GAP-10, GAP-11, GAP-12, GAP-13); they are kept here with their
> original wording because the summary-matrix rows and the follow-up list still
> reference them, and because the *reason* each was invisible to the test suite is the
> durable lesson. Correction **8 (GAP-14) remains open**, now with decision A7
> attached. See the follow-up list at the end for what is left.
>
> **Update 2026-07-30 — tracker-lifecycle batch landed.** GAP-15 is also fixed, along
> with three defects that all produced a **wrong over-cap count**: overlapping edge
> requests deleting a live record, an async-tracking race that could strand a
> permanently non-expiring ghost session, and the protocol-v3 logical-vs-transport
> identity split that counted one stream twice (masked until now because fresh v3
> starts sent no owner at all, so the record landed under user 0, which the enforcer
> skips — silently exempting the stream from the cap). These precede the revocation
> batch on purpose: decision A1 removes the self-healing that limits the damage of a
> miscount, so the count must be trustworthy first.
>
> Two claims this document makes elsewhere are **still false** and must not be
> restored to blanket phrasing: the kill switch does not "keep a stream dead" (an
> over-cap kill still reopens after 5m until the revocation batch lands), and
> monitoring does not "never trust client progress" (the `LastActivityAt` fallback
> survives until decision A5 lands). Also read every "cut" as "cut within ~5s" — the
> in-flight watcher polls on a 5s interval.

4. **The ABS in-flight kill switch does not work.** *(FIXED — `Unwrap()` added; the
   replacement test drives the mounted router over a real socket and was confirmed to
   fail without the fix.)* Every ABS route is wrapped by
   `accessLog` (`audiobooks/abs/handler.go:334`), whose `statusRecorder`
   (`audiobooks/abs/access_log.go:83`) implements `Write`, `WriteHeader` and `Hijack`
   but **not** `Unwrap()`. `WatchAndCut` walks
   `SessionMeteredWriter → statusRecorder` and dead-ends, so
   `SetWriteDeadline` returns `ErrNotSupported` — an error `WatchAndCut` discards
   (`streamrevoke/store.go:187`). The watcher then silently stops. A multi-GB
   audiobook pour started before a `RevokeUser` runs to completion.
   `audiobooks/abs/revocation_test.go` calls handlers directly, bypassing the
   middleware, so the test suite reports this as working. (GAP-10)

5. **Ebook, comic and PDF serving is invisible and un-killable.** *(FIXED — now
   metered, registered in the transfer registry, refused on entry and cut in flight;
   cap-exempt per decision A4. Note the fix does **not** reuse `guardRevocationCut` as
   originally suggested — that wrapper keys on a `session_id` param and `?st=` token
   this route does not carry, so it would have silently guarded nothing.)* The
   `/api/v1/ebooks/{content_id}/files/{file_id}/read` group (`api/router.go:2417`)
   carries only `apimw.RequireProfile`. Both `serveEbookInline`
   (`ebook_reader.go:626`) and `serveConvertedEpub` (`ebook_convert_serve.go:160`)
   pour whole files with no meter, no transfer record, no `Refuse` and no
   `WatchAndCut`. `ebookReaderFormat` (`ebook_reader.go:675`) accepts `cbz`/`cbr`
   comic archives and `pdf` — routinely 100 MB–1 GB+ each. (GAP-11)

6. **The rolling write deadline can erase a revocation cut.** *(FIXED — a `CutLatch`
   on the request context stops `bump()` from re-arming a cut socket, and the watcher
   keeps re-applying. Pulled forward from the revocation batch because it makes
   correction 4's fix inert: adding `Unwrap()` is what makes `SetWriteDeadline` start
   working, which is also what makes `bump()` start working.)* `WatchAndCut` sets the
   socket deadline to *now* once, then returns. On routes wrapped in
   `httpstream.RollingDeadlineWriter` — native managed and direct downloads
   (`api/handlers/downloads.go:447`) — `bump()`
   (`httpstream/rolling_deadline.go:95`) runs before every write slice and, once the
   15s `bumpStep` has elapsed, pushes the deadline back out to `now + 180s`. Nothing
   re-arms the watcher. The cut is therefore reliable against a *stalled* pour and
   unreliable against a *fast-draining* one — weakest against the ripping case it
   exists to stop. (GAP-12)

7. **Merged snapshots discard edge byte totals.** *(FIXED — merged as a max, not a sum,
   since the two records are two observers of one pour; `DedupeSessionInfos` had the
   same hole and was fixed too.)* `streammonitor.mergeStreams` takes
   the record with the later `LastServedAt` wholesale and backfills owner, route,
   client, HWAccel and position — but **not `BytesServed`**. When the central record
   is fresher, a stream whose bytes were all poured at an edge reports `0` bytes to
   the operator. (GAP-13)

8. **Transfer-registry saturation serves unmonitored.** The registry caps at
   `defaultMaxEntries = 10_000` (`transfers/registry.go:16`); past that `Begin`
   returns `ErrRegistryFull` and all four call sites log at **Debug** and serve
   anyway, discarding byte updates (`registry.go:122`). With no connection cap
   anywhere (E28) and an unthrottled compat surface (E25/E27), an attacker can pin
   the registry at its ceiling and blind download-class monitoring for everyone
   else. (GAP-14)

One thing the docs *under*-sell: `main` already ships a real general API rate
limiter (`internal/ratelimit/`), enabled by default — but it is mounted only on
the authenticated native surface + specific auth endpoints, and the entire
Jellyfin-compat surface is unthrottled (see Category E).

---

## Summary matrix

### Category A — Concurrency-cap abuse (the branch's core competency)

| # | Abuse story | Detection | Enforcement | One-line verdict |
|---|---|---|---|---|
| 1 | User opens N+1 concurrent **transcodes** (over cap) | ✅ | ✅ immediate | Admission refuses the N+1 start synchronously |
| 2 | User opens N+1 concurrent **direct-play** streams | ✅ | ✅ immediate | Same admission gate; method-agnostic |
| 3 | **Shared account**, many devices streaming at once | ✅ | ✅ / ⚠️ | Capped per-user on one process; leaks across replicas (#7) |
| 4 | **Buffer-ahead then pause** fetches >45s to duck the cap | ✅ | ✅ | Unpaused transcodes retain a 10m integrated / 180s edge server-observed grace |
| 5 | **Withhold/falsify progress** to hide a stream | ✅ | ✅ | Existence is byte-observed; progress only moves a display field |
| 6 | **Rapid session churn** between enforcer ticks | ⚠️ | ⚠️ | Admission blocks over-cap starts; no rate limit on `/start` |
| 7 | Split streams across **multiple integrated replicas** | ⚠️ | ⚠️ | Admission and integrated monitoring remain per-process (VERIFY-3) |
| 8 | **Standard user** exceeds cap via the edge/multi-node path | ✅ | ✅ ~120s | Enforcer resolves the group-merged effective cap and trims the excess |

### Category B — Admin control / kill switch

| # | Abuse story | Detection | Enforcement | One-line verdict |
|---|---|---|---|---|
| 9 | Admin **terminates** a specific abusive live session | ✅ | ✅ | Terminate writes revocation first, keyed on sid; 202 even if no local session |
| 10 | Admin **bans a user** (revoke all their streams) | ✅ | ✅ | Cutoff refuses every *next* request; in-flight cut now lands on every surface within ~5s (GAP-10/GAP-12 fixed) |
| 11 | Killed stream **reconnects** with the same token | ✅ | ✅ | Revocation outlives token; every reconnect `Refuse`d |
| 12 | Killed stream **survives a server restart** | ✅ | ✅ | Durable Postgres mirror re-warms the kill list; monotonic expiry |
| 13 | Kill issued **during an edge Redis outage** | ⚠️ | ⚠️ | Edge fails **open**: new kills don't reach edge until Redis recovers |

### Category C — Re-streaming / redistribution

| # | Abuse story | Detection | Enforcement | One-line verdict |
|---|---|---|---|---|
| 14 | Re-broadcast **one** Silo stream to many external viewers | ❌ | ❌ | Under-cap = untouched; no restream heuristic exists (correction #2) |
| 15 | **Many** concurrent Silo streams feeding a restream service | ⚠️ | ✅ ~120s | Caught only as raw over-count, not labeled re-streaming |
| 16 | **Mint & hoard** many 24h tokens, fan out later | ❌ | ❌ / ⚠️ | No mint cap, signature-only, stop doesn't revoke; caught only if fan-out becomes over-cap |

### Category D — Ripping / bulk data exfiltration

| # | Abuse story | Detection | Enforcement | One-line verdict |
|---|---|---|---|---|
| 17 | Rip whole library via **native download** endpoints | ⚠️ | ⚠️ | Active pours/bytes visible *until the registry saturates* (GAP-14); creation-time gate only; **no volume cap** |
| 18 | Rip via **compat `/Items/{id}/Download`** (Infuse) | ⚠️ | ❌ | Active pour visible, but **no quota at all** |
| 18a | Rip via authenticated **ABS file/download** route | ✅ | ⚠️ | Active pour visible and now cuttable in flight (GAP-10 fixed); still **no volume quota** |
| 18b | Pull an **ABS public playback track** | ✅ | ✅ | Native session id is monitored/metered; session and owner kills land |
| 18c | Pull a **public ABS RSS feed file** | ✅ | ⚠️ | Active pour visible; owner cutoff uses feed creation time and the in-flight cut now lands (GAP-10 fixed). Closing a feed still does not cut its current pour; that needs a separately designed, collision-safe feed capability revocation id and is deferred. |
| 18d | Rip via the **ebook/comic/PDF reader** route | ✅ | ⚠️ | Now metered, transfer-rowed, refused on entry and cut in flight (GAP-11 fixed); cap-exempt per A4 and still no volume quota |
| 19 | Rip via **sequential direct-play GETs** (one at a time) | ⚠️ | ❌ | Cap counts concurrency, not volume; stays at 1 forever |
| 20 | Admin **stops an in-progress rip** mid-transfer | — | ✅ | `WatchAndCut` now lands on every download-class route within ~5s (GAP-10/GAP-12 fixed); admin must still issue the revoke |

### Category E — API / DB load abuse (non-stream; branch is orthogonal)

| # | Abuse story | Detection | Enforcement | One-line verdict |
|---|---|---|---|---|
| 21 | **Infuse pulls the whole library list on every app start** | ❌ | ⚠️ | Per-page capped at 1000; full browse is uncached & unthrottled |
| 22 | **Thundering herd** — many clients cold-cache full enumeration | ❌ | ⚠️ | Latest/hub rails singleflight; general browse does not |
| 23 | Client **pages a 100k+ library as fast as possible** | ❌ | ❌ | Per-page COUNT + rising OFFSET; no page-rate limit, no cache |
| 24 | **Expensive sorted/filtered views** over the whole library | ❌ | ⚠️ | Some sorts index-optimized; `DateCreated`/filters = full top-N + COUNT, uncapped |
| 25 | Badly-coded client **retry storm** (thousands req/s) | ✅ | ⚠️ | Native authed surface throttled (per-IP 120rps); compat & `/auth/refresh` not |
| 26 | Tight-loop polling **`/auth/refresh`** | ✅ | ❌ | No rate-limit middleware on that route |
| 27 | **Credential stuffing** via compat `/Users/AuthenticateByName` | ✅ | ❌ | Compat router mounts no limiter; native login is capped 20/min |
| 28 | Many concurrent **HTTP connections / slowloris** | ❌ | ❌ | No connection cap / `LimitListener`; only Read/Write/Idle timeouts |
| 29 | Spawn many concurrent **transcodes to exhaust CPU** | ⚠️ | ❌ | Per-user `max_transcodes` only; no per-node/server-wide ffmpeg cap |

---

## Detailed stories

### Category A — Concurrency-cap abuse

**A1. N+1 concurrent transcodes (over cap).**
The (N+1)th `StartSession` returns `ErrTooManyStreams` *synchronously* at admission
(`playback/session.go`, `inlineAdmissionErrorLocked`), using the group-merged
effective cap (5 for a standard user). The client never gets a session. The async
enforcer resolves the same effective cap as its redundant backstop.
**Detection ✅ / Enforcement ✅ (immediate).**

**A2. N+1 concurrent direct-play streams.**
Direct-play sessions are `Track`-backed and counted identically at admission — the
`MaxStreams` check is method-agnostic. Same immediate refusal. **✅ / ✅.**

**A3. Shared account, many devices at once.**
All sessions carry the same `AuthUserID`; `streammonitor.ByUser` groups them and
`Client*` fields expose the fan-out. The per-user cap bounds simultaneous streams
to the effective `MaxStreams`. **Caveat:** the cap is per-process (see A7), so
spreading devices across integrated replicas multiplies the allowance.
**Detection ✅ / Enforcement ✅ on one process, ⚠️ across replicas.**

**A4. Buffer-ahead then pause (VERIFY-4 resolved).**
An unpaused transcode now has a bounded 10-minute integrated grace, driven by
server-observed `LastServedAt`, and ephemeral edge transcode records use a
180-second idle window consistently in count, snapshot, and refresh. A genuinely
paused session still receives the longer 30-minute paused grace. This covers
local, completed copy-mode, and offloaded transcodes without probing local ffmpeg.
**Detection ✅ / Enforcement ✅.**

**A5. Withhold/falsify progress reports.**
This *fails* as an evasion. Existence and liveness are **byte-observed**
(`BeginTransport`/`EndTransport` bracket every pour; `AddBytes`/`MarkServed` set
`LastServedAt`), never derived from client progress. Falsifying `Position` only
corrupts a secondary display field and, at worst, changes which of the user's
*own* over-cap sessions `selectVictims` trims first. **Detection ✅ / Enforcement ✅.**
*Two round-2 caveats, both bounded:* (a) at the **edge**, `touchTranscodeSession`
fires before the node is proxied (`proxy/server.go:308,317`), so hammering dead
segment URLs advances `LastServedAt` without serving a byte — liveness there is
request-observed, not byte-observed (GAP-15). Because the enforcer groups by user,
this only lets an attacker choose *which of its own* streams gets trimmed. (b) the
merged snapshot could report `0` bytes for an edge-served stream (GAP-13) — **fixed**;
`BytesServed` is now merged as a max in both `mergeStreams` and `DedupeSessionInfos`,
so the byte signal is sound as well as the existence signal.

**A6. Rapid session churn between ticks.**
No rate limit exists on `/start` or `/transcode/start` (the `ratelimit`
middleware is wired only to auth endpoints). In integrated mode synchronous
admission still refuses over-cap starts instantly, so churn can't exceed the cap
locally. The gap is the multi-node/edge picture, where admission may have run on a
different node and the enforcer only reconciles every ~120s.
**Detection ⚠️ / Enforcement ⚠️ (integrated fast, edge lags).**

**A7. Split across multiple integrated replicas.**
`activeCountLocked` counts only the local `SessionManager` map — there is no
distributed admission counter. Two integrated replicas each admit up to the cap
independently. The enforcer is the only cross-replica backstop, but integrated
replicas write no `silo:sessions:*` records, so a remote enforcer cannot see them.
**Detection ⚠️ / Enforcement ⚠️.** (VERIFY-3.)

**A8. Standard user exceeds the cap on the edge path.**
The enforcer resolves `SessionManager.EffectiveLimits`, so a raw
`users.max_streams = 0` inherits the group cap rather than meaning unlimited.
The edge monitoring picture is grouped by resolved owner and excess victims are
revoked on the async pass. **✅ / ✅ (~120s).**

### Category B — Admin control / kill switch

**B9. Admin terminates a specific session.**
`terminate` writes the revocation **first**, keyed on the session id alone, then
issues the cooperative realtime command; it answers `202 {status:"revoked"}` even
when the session is absent from the central in-memory manager (GAP-7) — exactly the
edge-served / post-restart / progress-withholding streams the kill switch exists
for. **Detection ✅ / Enforcement ✅.**

**B10. Admin bans a user.**
`RevokeUser` writes a `KindUser` entry; `IsRevoked` matches it for any of the
user's sessions whose credential predates the revocation (cutoff semantics, GAP-5),
so in-flight pours are cut (`WatchAndCut`) and reconnects `Refuse`d, while a
later legitimate re-login is not permanently banned. **✅ / ✅.**

**B11. Killed stream reconnects with the same token.**
The revocation entry outlives the token TTL, so every reconnect with the same
credential is refused. "Stays dead." **✅ / ✅.**

**B12. Killed stream survives a restart.**
The durable Postgres mirror (`streamrevoke/durable_postgres.go`, table
`stream_revocations`) is warmed on boot and re-armed on the poll tick. Revocation
expiry and cutoff merge independently, and durable unrevocation tombstones prevent
restart or stale-replica resurrection. This closes GAP-4, where PR #174's
restart-resilient playback would
otherwise reconstruct and re-serve a killed stream. **✅ / ✅.** *Transient boot
caveat:* if the durable warm exhausts its bounded retry **and** Redis is empty, the
kill list is empty until the first poll tick (≤60s) — fails **open** by design.

**B13. Kill issued during an edge Redis outage.**
Edge nodes have no durable store and learn *new* kills only from Redis pub/sub +
SCAN. While Redis is unreachable, already-cached kills persist but a kill issued
*during* the outage does not reach the edge until Redis recovers. **Detection ⚠️ /
Enforcement ⚠️ (fails open at the edge).**

### Category C — Re-streaming / redistribution

**C14. Re-broadcast one Silo stream to many external viewers. — NO DEFENSE.**
The classic Stremio/Plex-share abuse: one account pulls a single stream and a proxy
fans it out to hundreds. It stays at concurrency 1, so admission and the enforcer
never fire, and the re-streaming heuristic that would catch it *does not exist*
(correction #2). The fingerprints to build it (`ClientIP`, `ClientName`,
`BytesServed`, distinct `MediaFileID`) are captured but unused. **Detection ❌ /
Enforcement ❌.**

**C15. Many concurrent streams feeding a restream service (over cap).**
This *is* caught — but only as a raw over-count, not labeled as re-streaming.
`ByUser` counts sessions per uid; the enforcer revokes victims beyond the limit
within ~120s (and admission refuses new over-cap starts immediately). Relies on
correct owner attribution (jellycompat/edge records must carry the owner or they
bucket under user 0 and are skipped — mitigated by `mergeStreams` owner-adoption).
**Detection ⚠️ (as over-cap, not as restream) / Enforcement ✅ ~120s.**

**C16. Mint & hoard 24h tokens, fan out later.**
Stream tokens are signature-only 24h JWTs (`streamtoken/token.go`) with no `jti`,
no one-time-use, no per-user mint counter. A normal Stop does **not** write a
revocation, so a token for a stopped session stays cryptographically valid for its
full 24h. Hoarding is free and invisible at mint time. Fan-out is caught *only* if
the aggregate live use trips the per-user over-cap enforcer — i.e. a low-concurrency
fan-out (few tokens, each widely re-streamed) collapses back into C14 and evades
everything. **Detection ❌ (at mint) / Enforcement ❌ (unless it becomes over-cap).**

### Category D — Ripping / bulk data exfiltration

**D17. Rip via native download endpoints.**
Download record creation is bounded by `download.max_concurrent_per_user`
(default **3**, `internal/downloads/limiter.go`). Serving and re-fetching an
existing row is not gated. `download.max_per_period` and both bandwidth
knobs default to **0 = unlimited** — and the bandwidth managers are *rate* throttles
(token buckets), never *volume* caps. The concurrency check runs at record
creation, so a `create → complete → create` loop pulls the entire library 3 files
at a time forever. Active pours and server-observed bytes are visible through the
process-local admin `transfers` array, but disappear at completion and are not a
cumulative signal. **No total-volume cap exists anywhere.** **Detection ⚠️ /
Enforcement ⚠️ (creation-time gate only).**

**D18. Rip via compat `/Items/{id}/Download`. — WIDEST HOLE.**
`HandleDownload` (`jellycompat/streams.go`) requires only a compat session and the
per-item library-access filter, then serves the raw file with full Range support.
**No download row, no quota, no bandwidth throttle, and no byte cap.** An
Infuse-style client can GET/Range every original file back-to-back. The active
pour and served bytes are visible in the process-local admin transfer registry,
but there is no durable history or automated consumer. **Detection ⚠️ /
Enforcement ❌.**

**D18d. Rip via the ebook/comic/PDF reader route. — SECOND-WIDEST HOLE.**
`GET /api/v1/ebooks/{content_id}/files/{file_id}/read` (`api/router.go:2420`) is
guarded only by `apimw.RequireProfile`. `serveEbookInline` (`ebook_reader.go:626`)
opens the original file and hands it to `http.ServeContent` wrapped in nothing but a
`RollingDeadlineWriter`; `serveConvertedEpub` (`ebook_convert_serve.go:160`) does the
same for the converted EPUB. There is **no metered writer, no transfer registry
entry, no `Refuse`, and no `WatchAndCut`** — none of the `guardRevocation*` wrappers
used by `/stream/{session_id}` (`api/router.go:2541`) are applied. Full Range support
is inherited from `ServeContent`. The accepted formats include `cbz`, `cbr` and `pdf`
(`ebook_reader.go:675`), so this is a large-file path, not a text path. A user
revoked for ripping previously kept pulling the entire comic/ebook library at full
speed while the admin transfer list showed nothing and `BytesServed` never moved.

**FIXED (GAP-11):** the route now registers a transfer record, meters its bytes,
refuses a revoked reader on entry and cuts an in-flight pour within ~5s. It stays
**cap-exempt** per decision A4, so reading never consumes a video stream slot, and
there is still no *volume* quota — a patient single-threaded rip remains possible.
**Detection ✅ / Enforcement ⚠️.**

**D19. Rip via sequential direct-play GETs.**
Playing items one at a time keeps `activeCountLocked == 1`, always under the cap.
Because the cap counts concurrency and never cumulative bytes, sequential
direct-play of every raw file is a fully-permitted rip. Each play *is* an
attributable session, but nothing correlates sequential sessions into "this user is
ripping." **Detection ⚠️ / Enforcement ❌.**

**D20. Admin stops an in-progress rip mid-transfer.**
`streamrevoke.Store.WatchAndCut` (checks on entry +
every 5s, forces the socket via `SetWriteDeadline`) and arms it on native downloads
(`api/handlers/downloads.go`) and compat download/direct-play
(`jellycompat/streams.go`). So a **user revocation now cuts an in-flight download**
— best-effort (no-op if the writer chain lacks write-deadline support, then stops on
next request). The admin still has to *decide* to revoke; nothing auto-detects the
rip. **Enforcement ⚠️.**

### Category E — API / DB load abuse (branch is orthogonal)

**E21. Infuse pulls the whole library list on every app start.**
Per-page size is hard-capped (`compatBrowseMaxLimit = 1000`,
`jellycompat/content_direct.go`; native 100), so no single call dumps 100k. But the
general paged browse (`handleBrowseItems` → `BrowsePage`) is **uncached and
unthrottled** — every app-start re-runs fresh SQL, and full-page requests add a
per-page `COUNT` over the filtered set. The `Latest`/hub rails *are* cached (15-min
shared resolved-list cache + singleflight), but that's a rail, not the full list.
Nothing attributes listing load per client/device. **Detection ❌ / Enforcement ⚠️
(first page cheap & capped; full enumeration pays full price each start).**

**E22. Thundering herd on cold cache.**
For `Latest`/hub rails, `singleflight` collapses concurrent identical builds into
one DB hit. For the **general browse there is no singleflight and no cache** — N
concurrent full-library enumerations run N independent SQL workloads, bounded only
by the shared pool (`userdb.pool_max_open`, default 500), beyond which requests
queue/time out rather than shed gracefully. **Detection ❌ / Enforcement ⚠️ (only
the cached rails are protected).**

**E23. Page a 100k+ library as fast as possible.**
Each page is capped at 1000 rows, but nothing caps the number of pages, the page
rate, or aggregate cost; deep pages incur a per-page filtered `COUNT` and rising
`OFFSET` cost. No rate limit on `/Items` (compat router mounts no throttle).
**Detection ❌ / Enforcement ❌ (beyond the per-page size cap).**

**E24. Expensive sorted/filtered views over the whole library.**
Some sorts are index-optimized (`recently_added` walks a dedicated index; the
multi-library case fans into per-library index walks + in-memory merge). But
arbitrary client sorts map through `mapSortBy` — `SortBy=DateCreated` "forces a
full-library top-N heapsort" (code comment), and genre/name-prefix/person filters
add WHERE/JOIN predicates with no cost cap beyond the LIMIT, plus a full-filtered
`COUNT` per page when `include_total` is on (default). No query-cost estimator, no
cache for filtered browses, no rate limit. **Detection ❌ / Enforcement ⚠️
(query-shape-dependent).**

**E25. Badly-coded client retry storm.**
`main` *does* ship `internal/ratelimit/` (token-bucket, default-on): global 1000
rps + per-IP 120 rps, mounted inside the **authenticated native group**
(`api/router.go`, after `RequireAuth`). So a storm against an authed native
endpoint is shed per-IP. But the limiter runs *after* auth, the entire
**jellycompat surface is unthrottled**, `/auth/refresh` has no limiter, the Redis
backend **fails open** on error, and the default memory backend is per-process (so
"global 1000 rps" is really per-instance). Attribution + telemetry exist
(Prometheus by path/status, access log with `client_ip`). **Detection ✅ /
Enforcement ⚠️ (native authed only).**

**E26. Tight-loop polling `/auth/refresh`.**
Registered as a plain public route (`api/router.go`) with **no** `AuthEndpointHandler`
and **outside** the authenticated group — it hits neither the global, per-IP, nor
per-endpoint limiter. A tight refresh loop is unthrottled. **Detection ✅ (logged) /
Enforcement ❌.**

**E27. Credential stuffing via compat `/Users/AuthenticateByName`.**
Native `/api/v1/auth/login` is capped at 20/min/IP via `AuthEndpointHandler`. The
Jellyfin-compat login has **zero throttling** — the compat router mounts no limiter
— and there is no account-level lockout/backoff anywhere. **Detection ✅ /
Enforcement ❌ (compat path wide open).**

**E28. Many concurrent HTTP connections / slowloris.**
No connection cap: no `netutil.LimitListener`, no per-IP connection limit, no
handler semaphore. The `http.Server` sets only Read/Write/Idle timeouts (and the
compat + ABS servers even set `WriteTimeout: 0`). The rate limiter counts
*requests*, not *connections*. **Detection ❌ / Enforcement ❌.**

**E29. Spawn many concurrent transcodes to exhaust CPU.**
Only the per-user `max_transcodes` (default 2, `<=0` = unlimited) is checked, at
admission. There is **no per-node or server-wide ffmpeg/CPU cap** on fresh starts
(the `reconstructSem` NumCPU semaphore guards only the restart-reconstruct path).
Node scheduling has an optional `MaxJobs`/`MaxBandwidthKbps` per node
(`nodepool`), but both default to unlimited. N users at a high per-user cap — or one
user with `max_transcodes <= 0` — can saturate the box. **Detection ⚠️ (sessions
visible, no CPU/process signal) / Enforcement ❌ (no aggregate cap).**

---

## What the branch genuinely delivers vs. what it does not

**Delivers (and it's solid):**
- Authoritative, **byte-observed** stream existence on the *playback* surfaces that a
  client cannot hide by lying about or withholding progress (A5) — with the two
  bounded caveat GAP-15 noted there, and now **including** ebooks (GAP-11 fixed) and
  subtitle pours. GAP-13 is fixed, so merged byte totals are trustworthy too. Still
  **not** guaranteed under transfer-registry saturation (GAP-14, open).
- A durable, restart-surviving, reconnect-proof **kill switch** for a *specific
  session* or a *user*, via one shared `Refuse` + `WatchAndCut` (B9–B12, D20).
  `Refuse` — the next-request half — holds everywhere it is mounted. The
  `WatchAndCut` half was weaker than previously documented — it no-op'd on all ABS
  routes (GAP-10), was racy on rolling-deadline routes (GAP-12) and was absent from
  ebooks (GAP-11) — and **all three are now fixed**, so it lands on every mounted
  surface. Two honest bounds remain: the cut takes up to one **~5s** watch tick, and it
  does **not** "keep a stream dead" — an over-cap kill still reopens after 5m until
  decision A1 lands.
- Immediate **synchronous admission** refusal of over-cap starts, and an async
  over-cap reconciler for the multi-node picture (A1, A2, C15) — *when a per-user
  cap is set*.

**Does not cover (by scope or by gap):**
- **Under-cap re-streaming** (C14) and **token hoarding/fan-out** (C16) — no
  detection, no enforcement; the heuristic that was scoped for this is unimplemented.
- **Bulk ripping** by download (D17/D18) or sequential direct-play (D19) — the cap
  is a *concurrency* gate, not a *volume* quota; compat download is entirely
  unquota'd.
- **Library-enumeration DB load** (E21–E24) — orthogonal subsystem; per-page capped
  but per-client-uncached, unthrottled, and unattributed.
- **API floods on the compat surface, `/auth/refresh`, and connection exhaustion**
  (E25–E28) — rate limiter has real coverage gaps.
- **CPU/transcode exhaustion** (E29) — no aggregate cap.

## Settled design decisions (2026-07-30)

These were open forks blocking the remaining batches. All are now decided, so follow-up
issues inherit them rather than re-litigating.

| # | Question | Decision |
|---|---|---|
| **A1** | Over-cap enforcement model | **Long revocation** matching the token's reconstructable lifetime, so an over-cap kill stops reopening every 5m. **Must not ship before the tracker-lifecycle batch** — it removes the self-healing property that currently limits the damage of a wrong count, and every known source of a wrong count is in that batch. |
| **A2** | Revocation state model | **Durable tombstones** — an independent `RevokedAt`/`ExpiresAt` merge *plus* a durable tombstone so an un-ban survives a restart and cannot be re-`Upsert`ed by a stale replica. One Goose migration. |
| **A3** | Credential identity for the user cutoff | **Presented credential time.** Native access uses token `iat`; normal Jellyfin compatibility sessions use their stable login `CreatedAt`, not the refreshed bridged token. API keys and valid JWTs without `iat` use zero and deliberately fail open, so a user cutoff cannot cut their pours. Per-login logout cuts stay out of scope (they need authorization-generation/per-login identity in the stream credential). |
| **A4** | What counts as a "stream" | **Observe + make killable, keep cap-exempt** for the ABS bare file route and ebook/comic/PDF reading. Neither consumes a video stream slot. **Implemented.** |
| **A5** | Liveness source of truth | **Separate server-observed liveness entirely.** Client progress becomes UI metadata and never feeds enforcement or reaping; the `LastActivityAt` fallback for `LastServedAt` is removed. This is what makes "never trusts client progress" true — it is **not** true today. |
| **A6** | Multi-replica visibility | **Publish every integrated stream to Redis** so all replica enforcers share one picture. Fixes the incomplete-input root cause; snapshot staleness handled by re-reading at kill time rather than a distributed lock. |
| **A7** | Fail-open vs fail-closed monitoring | **Fail closed + connection cap.** See follow-up 0e. |
| **A8** | Scope | **Split.** The serve-path, capability and docs work lands first; tracker lifecycle → revocation state → liveness/replicas → re-stream heuristic follow in that dependency order. |

A new finding recorded while implementing A4, for the A3 batch: `streamRequestIdentity`
verifies a stream token's signature but never checks that the token's `SessionID`
matches the `session_id` in the URL, so a valid token for one session can supply
identity on another session's route. It belongs with A3 rather than being fragmented
across two batches.

## Recommended follow-ups (prioritized by exposure)

**Round-2 defects come first — these are open bugs in shipped behavior, not
enhancements.** In rough fix-cost order:

0a. ~~**Add `Unwrap()` to `abs.statusRecorder`**~~ — **DONE.** Plus `WatchAndCut` now
    logs (once per watcher) rather than discarding a failed `SetWriteDeadline`, and the
    replacement test drives the *mounted* router over a real socket. Verified
    non-vacuous: it fails if `Unwrap()` is removed again. (GAP-10)
0b. ~~**Wire the ebook routes into the stream kill switch and the transfer registry**~~
    — **DONE**, cap-exempt per decision A4. ⚠️ **The original advice in this line —
    "reuse `guardRevocationCut`" — was wrong and was not followed.** That wrapper reads
    `chi.URLParam(r, "session_id")`, which the ebook route does not have (it would pass
    `""`), and takes identity from `streamRequestIdentity`, which looks for a `?st=`
    stream token the route never carries. It compiles and silently guards nothing. The
    ebook fix takes identity from the profile/auth context and otherwise follows the ABS
    file-handler idiom. (GAP-11)
0c. ~~**Make the revocation cut survive the rolling deadline**~~ — **DONE** via a
    `CutLatch` on the request context. Note the sticky-flag-on-`RollingDeadlineWriter`
    option suggested here could not be implemented as written: the rolling writer is
    constructed *inside* `ServeDirectPlay`/`ServeRemux` and wraps the writer the watcher
    holds, so it sits *above* the watcher and `Unwrap()` (which walks toward the socket)
    can never reach it. Hence the context side channel. The re-applying watcher was also
    kept, as belt-and-braces. (GAP-12)
0d. ~~**Merge `BytesServed` as a max, not wholesale**~~ — **DONE** in both
    `mergeStreams` and `DedupeSessionInfos` (GAP-13). **GAP-15 is also DONE:** edge
    transcode visibility is created before proxying, while liveness and byte totals
    advance only for bytes actually written from a 200/206 upstream response.
0e. **Monitoring projection is not uniformly asynchronous.** The first Redis write
    for each edge session remains synchronous so the record is visible before the
    request returns; subsequent liveness and byte projection happens asynchronously
    on the refresh tick. A slow Redis therefore adds latency to the first request of
    a stream. The proposed ordered, bounded projection queue is deliberately deferred
    until its startup, drain, cleanup ordering, backpressure, and refresh interaction
    can be designed together.
0f. **Decide the registry-saturation policy explicitly** (GAP-14) — **DECIDED (A7),
    not yet implemented:** fail *closed* for download-class pours once the registry is
    full, **and** add a per-user/credential concurrent-connection cap (E28) so
    saturation is unreachable by a single actor in the first place. Scheduled for the
    liveness/replica batch. Today it still fails open, logging at Debug at the call
    sites.
0g. ~~**Bound the kill-list propagation lock.**~~ **DONE.** Redis and pub/sub run
    before the durable mirror, and detached propagation/startup warm use bounded
    contexts. `opMu` deliberately remains held across propagation so a same-process
    unrevoke cannot interleave with an older mirror write. Two central replicas can
    still race the unconditional Redis `SET`; an atomic cross-replica merge remains
    deferred to A6/Batch 4.

Then, as originally scoped:

1. **Download controls phase 2:** add per-download kill and a standing per-user
   download block. The existing user revocation is a cutoff, not a ban: it cuts
   older pours but deliberately allows new ones.
2. **Unify download quotas and add a per-user *volume* budget** (D17/D19): cover
   native serving, compat, and ABS with a rolling bytes-per-period cap that spans
   downloads and direct-play, since concurrency caps cannot bound a rip.
   Also deferred: correct native completion semantics after partial/write-failed
   responses, correlate duplicate ABS playback-session and `abs_file_stream`
   views, and aggregate transfer visibility across API replicas.
3. **Extend rate limiting to the compat surface + `/auth/refresh`** and add a
   connection cap / `LimitListener` (E25–E28).
4. **Add a per-node concurrent-transcode cap** and wire `nodepool.MaxJobs` defaults
   (E29).
5. **Implement the restream heuristic** (C14/C16): the fingerprints already flow
   through `streammonitor`; a distinct-viewer-IP / distinct-title / throughput rule
   is the missing consumer. Ship disabled, tune against real traffic.
6. **Per-client listing-load accounting** (E21–E24): attribute browse cost per
   device and add caching/singleflight to the general paged browse, not just the
   Latest/hub rails.

## AI-use disclosure

This assessment was produced with AI assistance, from a read-only audit of the
`feat/sauron-async-enforcer` branch and current `main`. No code behavior was changed.
Findings cite the code as of the audit; the corrections above are where the branch's
own plan/coverage docs overstate what the shipped code does.

Round 1 (corrections 1–3) was produced with Claude. Round 2 (corrections 4–8, dated
2026-07-29) was a **two-model** pass: Claude Opus 5 and Codex `gpt-5.6-sol` reviewed
the rebased branch independently, and every finding recorded here was re-verified
against the code by hand before being written down. Corrections 4, 6, 7 and the
`opMu` follow-up (0f) originated with Codex; corrections 5 and 8 were found by both
models independently. Two Codex claims were **rejected** on verification and are
deliberately not recorded above: that chi's `middleware.Compress` wrapper breaks the
`Unwrap` chain (chi v5.2.5 implements `Unwrap()` at `middleware/compress.go:374`),
and that client progress reports advance the central `Session.LastServedAt` (only
`BeginTransport`/`EndTransport`/`AddServedBytes` write it — the real mechanism is the
edge `Touch`, recorded as GAP-15).

Round 3 (2026-07-30) implemented corrections 4–7 as a **cross-model relay**: Claude
Opus 5 (`claude-opus-5[1m]`) planned, reconciled and reviewed; Codex `gpt-5.6-sol` at
medium effort adversarially reviewed the plan, implemented it, and took one remediation
round. Involvement classification: **AI-assisted implementation with human-directed
scope and AI cross-review**; every design fork (A1–A8) was decided by the maintainer,
not by either model.

Adversarial findings and their resolution, recorded because the disclosure standard
requires it:

- Codex's plan review found the decisive issue: fixing GAP-10 alone is **inert**,
  because the same `Unwrap()` that makes `SetWriteDeadline` work also makes
  `RollingDeadlineWriter.bump()` work, and `bump()` erases the cut. GAP-12 was pulled
  forward from a later batch as a result. **Accepted.**
- Codex correctly refuted two claims in the Claude plan: that native subtitle URLs carry
  a `?st=` stream token (they do not — `subtitleStreamURL` emits only `file_id`), and
  that the `transfers` capability belonged on `/admin/sessions/capabilities` (it belongs
  to `/admin/node-sessions`, which is separately gated). It also correctly refuted a
  claimed "latent nil bug" in the ABS transfer defer — `Registry.End` guards a nil
  receiver. **All accepted; the plan was corrected before implementation.**
- Claude's review of the Codex implementation found five defects, all confirmed by
  reading or by running the tests: a **failing real-socket ebook test** (Codex's sandbox
  could not run `httptest` listeners, so every real-socket test it wrote was
  unexecuted); **GAP-12 left unfixed on the proxy edge remux path**; Warn-log spam every
  5s from the now-perpetual watcher; `HandleVideoStream` still using three separate
  `time.Now()` values — the exact race Codex itself had raised and fixed only in the
  subtitle path; and subtitle metering counting error-response bodies. **All five fixed
  in one remediation round and re-verified here.**
- Claude's Batch-5 capability code was then reviewed by Codex, which found that the
  vocabulary drift test was not genuinely bidirectional — it sampled rejected strings,
  so a kind newly *accepted* by the parser could go unadvertised. **Accepted:** the
  parser and the advertisement now read one shared map.

Verification note: `pnpm` is absent on the host used for this round, so `make build`,
`pnpm run lint` and `pnpm run format:check` could not be run locally and must come from
CI. No frontend files were changed. The `internal/access` and `internal/jellycompat`
test packages **do not compile on `main`** (stale `UserStore` doubles missing
`GetOnboardingState`), so the compat changes in this round are **verified by reading
only** — CI cannot exercise them either until that is fixed separately.
