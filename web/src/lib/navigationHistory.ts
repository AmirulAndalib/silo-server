/**
 * The one bit of navigation state CSS needs: which way the page is moving.
 *
 * React Router stamps its own entry index on `window.history.state.idx`. That is
 * the only ordering signal a history entry carries — `location.key` changes on
 * every visit and the History API exposes neither the stack nor the delta to an
 * arbitrary entry — so comparing it across a popstate is the only way to tell a
 * Back from a Forward.
 *
 * It is deliberately used for nothing more than that. An earlier revision also
 * mirrored idx -> path so a breadcrumb could `navigate(-n)` and unwind to an
 * ancestor already behind the user instead of pushing a duplicate. That map
 * cannot be made sound here: React Router's `push()` computes
 * `index = getIndex() + 1` over `(globalHistory.state || { idx: null }).idx`
 * (react-router/dist/development/lib/router/history.js), so any entry that lands
 * without an `idx` makes `null + 1 === 1` and silently restarts the numbering
 * while the real stack keeps growing. This app ships that trigger: the
 * "Skip to content" links in Layout and AdminLayout are raw `<a href="#...">`,
 * and a same-document fragment navigation pushes an unindexed entry and fires
 * `hashchange`, which the router does not listen for. From then on a recorded
 * index is no longer a fixed offset from a real stack position, and the delta
 * lands the user on a page they never asked for. Pushing a duplicate entry is a
 * cosmetic annoyance; navigating somewhere unrelated is not, so the unwind is
 * gone and breadcrumbs push like any other link.
 */

export type NavigationDirection = "forward" | "back";

/**
 * The index the browser has committed. During a popstate this is already the
 * DESTINATION entry — the browser installs it before dispatching the event.
 * Null when nothing stamped an index: a first load before React Router writes
 * its state, or an entry pushed by something outside the router.
 */
function readHistoryIndex(): number | null {
  if (typeof window === "undefined") return null;
  const idx = (window.history.state as { idx?: unknown } | null)?.idx;
  return typeof idx === "number" ? idx : null;
}

/** Index of the entry we believe is current; null until the first navigation. */
let trackedIndex: number | null = null;

/**
 * Writes the direction CSS selects page motion on. Pass null when the direction
 * is genuinely unknowable; the attribute comes off and `main-content` falls back
 * to the neutral, non-directional transition rather than guessing a side.
 *
 * Must be called BEFORE the navigation starts. React Router opens the view
 * transition two commits later and the browser resolves `::view-transition-*`
 * styles later still, so nothing may clear this from a location effect — that
 * would erase the direction for the transition it belongs to. The attribute is
 * overwritten at the start of the next navigation; that is its whole lifecycle.
 */
export function markNavigationDirection(direction: NavigationDirection | null): void {
  if (typeof document === "undefined") return;
  if (direction === null) {
    delete document.documentElement.dataset.navigationDirection;
    return;
  }
  document.documentElement.dataset.navigationDirection = direction;
}

/**
 * Direction of the POP the browser has just committed, and null when it cannot
 * be derived — an entry from before this page load, an index the router never
 * stamped, or a same-index pop such as a bare hash change.
 *
 * Advances the tracked index, so it must be called once per popstate: the
 * comparison is against the entry we came from, not against the last entry
 * React Router happened to commit.
 *
 * An unindexed entry in the stack degrades this to null — no attribute, neutral
 * motion — rather than to a wrong answer, which is why the index is safe to use
 * for direction even though it was not safe to use for deltas.
 */
export function resolvePopDirection(): NavigationDirection | null {
  const destination = readHistoryIndex();
  const origin = trackedIndex;
  trackedIndex = destination;
  if (destination === null || origin === null || destination === origin) return null;
  return destination < origin ? "back" : "forward";
}

/**
 * Records the index React Router has just committed, so the next popstate has
 * something to compare against. Safe to call from a location effect: the history
 * entry exists by the time the router notifies subscribers.
 */
export function recordNavigation(): void {
  const idx = readHistoryIndex();
  if (idx === null) return;
  trackedIndex = idx;
}

/**
 * Whether stepping back one entry stays inside the app. React Router numbers its
 * entries from 0, so anything above that has an in-app entry behind it.
 *
 * Unlike a path map this survives a reload — the index lives in `history.state`,
 * not in memory — and it asks nothing about *which* page is behind, only that
 * one is, which is all a plain "go back one" needs.
 */
export function hasEarlierEntry(): boolean {
  const idx = readHistoryIndex();
  return idx !== null && idx > 0;
}

/** Test seam: module state outlives a render, so tests have to reset it. */
export function resetNavigationHistory(): void {
  trackedIndex = null;
}
