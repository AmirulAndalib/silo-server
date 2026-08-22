# Feature Changelog

## 2026-08-22

### Measure delivered bytes on every serving path
Silo now measures what it actually sends, rather than trusting what a client reports it is watching.
- Every byte-serving route across the API server, Jellyfin-compatibility layer, standalone proxy, audiobook listener and transcode nodes reports what it served, to whom and how fast, off the hot path.
- Adds `GET /api/v1/admin/stream-telemetry/parity`, which puts the merged measurement beside the two live-session views admins read today and diffs them. See [docs/admin-api.md](admin-api.md).
- Makes no decisions: nothing is blocked, throttled or ended, and no existing admin view was repointed onto it.
- Fixes four defects on the byte paths themselves — proxied streams recorded against no owner, the proxy's own address recorded as the viewer's, the kernel sendfile fast path dead through the proxy chain, and stream tokens with no reliable creation time.

## 2026-04-09

Covers commits from 2026-04-08 22:32 EDT through 2026-04-09 20:02 EDT.

### Add artwork selection in the metadata editor
Admins can now browse and apply poster, backdrop, and logo images from enabled metadata providers without leaving the edit flow.
- Adds an `Images` tab in the item metadata editor for movies, series, seasons, and episodes.
- Pulls image options from provider plugins, shows provider badges and popularity ordering, and highlights the current selection.
- Applies the chosen image through new admin endpoints, caches it to storage, and locks image fields so future refreshes do not overwrite the manual choice.

### Add native ASS and SSA subtitle support
The player and backend now preserve styled anime subtitles instead of flattening them into simpler text formats.
- Serves ASS tracks natively across the main playback API, Jellyfin compatibility routes, and the proxy subtitle path.
- Integrates JASSUB in the web player so fonts, positioning, karaoke effects, and other advanced subtitle styling render correctly.
- Keeps existing SRT and VTT handling unchanged while exposing subtitle format badges in the subtitle picker.

### Show live server activity in the web app
Admins now get a Plex-style live activity indicator directly in the UI.
- Adds a real-time dropdown that summarizes direct play, remux, and transcode sessions alongside task progress and active library scans.
- Keeps the activity affordance visible on admin screens and only exposes it on regular app pages when activity is actually happening.
- Reuses existing realtime channels instead of introducing new backend plumbing.

### Improve playback reliability and control flow
Recent playback work focused on making copy-mode streaming behave more like direct play while reducing restart-related failures.
- Preserves copy-mode when seeking, enables seek-anywhere manifests when duration is known, and allows supported HDR content to stay in direct play or remux paths instead of forcing full transcodes.
- Retries Firefox copy-mode startup with a compatibility fallback to reduce failed starts on that browser.
- Separates player exit and minimize controls so watch-page navigation is less ambiguous.
- Handles transcode restarts more cleanly during segment waits and fixes copy-mode buffering after a restart so old segments are not served back to the player.

### Refresh playback surfaces after watch-state changes
A few related UI changes now keep browsing and episode detail views aligned with what the viewer just watched.
- Refreshes home sections after watched-state updates so progress-sensitive shelves stay current without a manual reload.
- Shows sticky version preferences on episode detail pages so preferred cuts or versions remain visible when choosing playback options.

### Add a cinematic Playing Next experience
Episode-to-episode playback now transitions through a richer post-roll flow.
- Replaces the bare post-roll screen with a `Playing Next` screen that can appear about 30 seconds before the end of an episode.
- Keeps the current episode running in a resizable mini-player while showing the upcoming episode with a more prominent background treatment.
- Adds auto-play-next playback settings and supports cross-season next-episode lookups.

### Support S3 key prefixes for stored media and assets
Storage configuration can now target a scoped path inside an S3 bucket instead of requiring a bucket root layout.
- Adds S3 key prefix configuration in server config loading, admin storage settings, and setup flows.
- Updates the S3 client so Silo reads and writes through the configured prefix consistently.

### Add chapter thumbnails and chapter-aware navigation
Playback now has much deeper chapter support, including generated preview images and more resilient thumbnail processing.
- Scans and stores embedded chapter metadata, exposes it through watch detail responses, and adds player chapter menus and seek-bar chapter affordances.
- Introduces library settings and backfill tasks for chapter thumbnail generation, with realtime thumbnail delivery into active playback sessions.
- Tracks thumbnail failure state, retry timing, and HDR thumbnail policy so generation can recover more predictably when extraction fails.
