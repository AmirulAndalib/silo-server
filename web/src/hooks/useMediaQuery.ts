import { useCallback, useSyncExternalStore } from "react";

/**
 * Tracks a CSS media query.
 *
 * `fallback` answers where matchMedia does not exist — server rendering, tests,
 * and older browsers — so a caller can say which side of the query those
 * environments should be treated as.
 */
export function useMediaQuery(query: string, fallback = false): boolean {
  const subscribe = useCallback(
    (onStoreChange: () => void) => {
      const media = window.matchMedia?.(query);
      media?.addEventListener("change", onStoreChange);
      return () => media?.removeEventListener("change", onStoreChange);
    },
    [query],
  );

  const getSnapshot = useCallback(
    () => window.matchMedia?.(query).matches ?? fallback,
    [query, fallback],
  );

  return useSyncExternalStore(subscribe, getSnapshot, () => fallback);
}
