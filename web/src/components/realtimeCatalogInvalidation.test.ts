import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { catalogKeys, sectionKeys } from "@/hooks/queries/keys";
import {
  createCatalogInvalidationScheduler,
  userStateChangeAffectsSectionMembership,
} from "./realtimeCatalogInvalidation";

const WINDOW_MS = 2_000;

function catalogListKey(libraryId: number) {
  return catalogKeys.list({
    source: "section",
    scope: "library",
    section_id: "all",
    library_id: libraryId,
    limit: 60,
    offset: 0,
  });
}

/** Seeds one cached query per library so invalidation scope is observable. */
function seedLibraries(queryClient: QueryClient, libraryIds: number[]) {
  for (const libraryId of libraryIds) {
    queryClient.setQueryData(catalogListKey(libraryId), { items: [] });
  }
}

function invalidatedLibraries(queryClient: QueryClient, libraryIds: number[]) {
  return libraryIds.filter(
    (libraryId) => queryClient.getQueryState(catalogListKey(libraryId))?.isInvalidated,
  );
}

describe("createCatalogInvalidationScheduler", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("invalidates the first event immediately", async () => {
    const queryClient = new QueryClient();
    seedLibraries(queryClient, [1, 3]);
    const scheduler = createCatalogInvalidationScheduler(queryClient, WINDOW_MS);

    scheduler.schedule({ libraryId: 3, allowDashboardRefetch: false });
    await vi.advanceTimersByTimeAsync(0);

    expect(invalidatedLibraries(queryClient, [1, 3])).toEqual([3]);
  });

  it("coalesces a burst into a single trailing sweep", async () => {
    const queryClient = new QueryClient();
    const invalidateQueries = vi.spyOn(queryClient, "invalidateQueries");
    const scheduler = createCatalogInvalidationScheduler(queryClient, WINDOW_MS);

    scheduler.schedule({ libraryId: 3, allowDashboardRefetch: false });
    await vi.advanceTimersByTimeAsync(0);
    const afterLeadingEdge = invalidateQueries.mock.calls.length;

    for (let i = 0; i < 50; i += 1) {
      scheduler.schedule({ itemId: `item-${i}`, libraryId: 3, allowDashboardRefetch: false });
    }
    await vi.advanceTimersByTimeAsync(WINDOW_MS - 1);

    expect(invalidateQueries.mock.calls.length).toBe(afterLeadingEdge);

    await vi.advanceTimersByTimeAsync(1);

    // Exactly one more sweep for all 50 events, not 50 sweeps.
    expect(invalidateQueries.mock.calls.length).toBe(afterLeadingEdge * 2);
  });

  it("widens to an unscoped sweep when a window spans several libraries", async () => {
    const queryClient = new QueryClient();
    seedLibraries(queryClient, [1, 3]);
    const scheduler = createCatalogInvalidationScheduler(queryClient, WINDOW_MS);

    // Leading edge consumes the first event; the rest share one window.
    scheduler.schedule({ libraryId: 3, allowDashboardRefetch: false });
    await vi.advanceTimersByTimeAsync(0);
    seedLibraries(queryClient, [1, 3]);

    scheduler.schedule({ libraryId: 3, allowDashboardRefetch: false });
    scheduler.schedule({ libraryId: 1, allowDashboardRefetch: false });
    await vi.advanceTimersByTimeAsync(WINDOW_MS);

    expect(invalidatedLibraries(queryClient, [1, 3])).toEqual([1, 3]);
  });

  it("still invalidates the touched library's own sections on a scoped sweep", async () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(sectionKeys.libraryLayout(3), { sections: [] });
    const scheduler = createCatalogInvalidationScheduler(queryClient, WINDOW_MS);

    scheduler.schedule({ libraryId: 3, allowDashboardRefetch: false });
    await vi.advanceTimersByTimeAsync(0);

    expect(queryClient.getQueryState(sectionKeys.libraryLayout(3))?.isInvalidated).toBe(true);
  });

  it("drops queued work on cancel", async () => {
    const queryClient = new QueryClient();
    const scheduler = createCatalogInvalidationScheduler(queryClient, WINDOW_MS);

    scheduler.schedule({ libraryId: 3, allowDashboardRefetch: false });
    await vi.advanceTimersByTimeAsync(0);
    seedLibraries(queryClient, [3]);

    scheduler.schedule({ libraryId: 3, allowDashboardRefetch: false });
    scheduler.cancel();
    await vi.advanceTimersByTimeAsync(WINDOW_MS * 2);

    expect(invalidatedLibraries(queryClient, [3])).toEqual([]);

    // The window timer is gone too, so the next event leads again.
    scheduler.schedule({ libraryId: 3, allowDashboardRefetch: false });
    await vi.advanceTimersByTimeAsync(0);

    expect(invalidatedLibraries(queryClient, [3])).toEqual([3]);
  });

  it("marks home section data stale (without refetching) on a library-scoped sweep", async () => {
    const queryClient = new QueryClient();
    const homeItemsKey = sectionKeys.homeItems("recently-added");
    queryClient.setQueryData(sectionKeys.homeLayout(), { sections: [] });
    queryClient.setQueryData(homeItemsKey, { items: [] });
    const scheduler = createCatalogInvalidationScheduler(queryClient, WINDOW_MS);

    scheduler.schedule({ libraryId: 3, allowDashboardRefetch: false });
    await vi.advanceTimersByTimeAsync(0);

    // Stale, so the throttled home queue reset fetches real data instead of
    // re-rendering the fresh-but-outdated cache…
    expect(queryClient.getQueryState(sectionKeys.homeLayout())?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(homeItemsKey)?.isInvalidated).toBe(true);
    // …but the sweep itself never refetches them (no observers here, and the
    // invalidation is refetchType "none").
    expect(queryClient.isFetching()).toBe(0);
  });
});

describe("userStateChangeAffectsSectionMembership", () => {
  it("ignores progress ticks and accepts every membership change", () => {
    expect(userStateChangeAffectsSectionMembership("progress")).toBe(false);
    for (const change of ["favorite", "watchlist", "history", "watched", "home_dismissal"]) {
      expect(userStateChangeAffectsSectionMembership(change)).toBe(true);
    }
  });

  it("treats an unknown change as membership-affecting", () => {
    expect(userStateChangeAffectsSectionMembership(undefined)).toBe(true);
  });
});
