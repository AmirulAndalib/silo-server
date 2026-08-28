import { useEffect } from "react";
import { useLocation } from "react-router";

import {
  markNavigationDirection,
  recordNavigation,
  resolvePopDirection,
} from "@/lib/navigationHistory";

/**
 * Keeps `html[data-navigation-direction]` in step with the browser. Mount once,
 * above every route — `WatchPlaybackChrome` and `WatchTogetherJoin` start view
 * transitions outside `Layout`, and the tracked index has to survive the
 * auth-gated routes that unmount it.
 *
 * Push direction comes from the caller, at click time. Back/forward direction
 * cannot: React Router swaps currentLocation and nextLocation on a reverse POP,
 * so both legs of a back-and-forth produce the byte-identical pair and
 * `useViewTransitionState` is blind to direction by construction. The delta
 * only exists inside the router's private history, so an app-level popstate
 * listener recomputing it from the previous index is the only source.
 *
 * The race is not close. React Router's own popstate handler is registered
 * during App's first render, so this one runs second, and from there every path
 * to `startViewTransition` goes through an awaited loader pass and two React
 * commits. The synchronous branch needs `flushSync`, which a POP never carries.
 */
export function useNavigationDirection(): void {
  const location = useLocation();

  useEffect(() => {
    const handlePopState = () => {
      // Null clears the attribute rather than leaving the last push's direction
      // standing — an unknowable direction gets the neutral transition.
      markNavigationDirection(resolvePopDirection());
    };
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  useEffect(() => {
    // Index bookkeeping only, so the next popstate has something to compare
    // against. This must never touch the direction attribute: passive effects
    // for the new location flush before React Router's commit barrier resolves,
    // and therefore before the browser captures the new snapshot, so clearing
    // here would erase the direction mid-transition.
    recordNavigation();
  }, [location]);
}
