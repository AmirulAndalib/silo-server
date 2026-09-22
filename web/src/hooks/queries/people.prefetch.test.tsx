import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { setProfileId } from "@/api/client";
import { installPolicyStorageMocks, jsonResponse } from "@/pages/admin-policy/policyTestUtils";

import { personKeys } from "./keys";
import { usePrefetchPeople } from "./people";

vi.mock("@/lib/routeChunkPrefetch", () => ({
  scheduleWhenIdle: (task: () => void) => {
    task();
    return () => {};
  },
}));

type PendingRead = { url: URL; signal: AbortSignal; resolve: () => void };

function stubPeopleFetch() {
  const reads: PendingRead[] = [];
  const fetchMock = vi.fn<typeof fetch>((input, init) => {
    const url = new URL(String(input), "http://localhost");
    const signal = init?.signal as AbortSignal;
    return new Promise<Response>((resolve, reject) => {
      signal.addEventListener("abort", () => reject(signal.reason));
      reads.push({
        url,
        signal,
        resolve: () => resolve(jsonResponse({ id: url.pathname.split("/").pop(), name: "Person" })),
      });
    });
  });
  vi.stubGlobal("fetch", fetchMock);
  return reads;
}

function seedCapabilities(client: QueryClient, personPrefetch = true) {
  client.setQueryData(personKeys.searchCapabilities(), {
    state: "available",
    person_prefetch: personPrefetch,
  });
}

function renderPrefetch(client: QueryClient, ids: string[]) {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return renderHook(() => usePrefetchPeople(ids), { wrapper });
}

beforeEach(() => {
  installPolicyStorageMocks();
  setProfileId("test-profile");
});
afterEach(() => vi.unstubAllGlobals());

it("prefetches uncached cast a few at a time without queuing provider refreshes", async () => {
  const reads = stubPeopleFetch();
  const client = new QueryClient();
  seedCapabilities(client);
  client.setQueryData(personKeys.detail("2"), { id: "2", name: "Cached" });

  const { unmount } = renderPrefetch(client, ["1", "2", "3", "1", "4", "5"]);

  await waitFor(() => expect(reads).toHaveLength(3));
  expect(reads.map((read) => read.url.pathname)).toEqual([
    "/api/v2/catalog/people/1",
    "/api/v2/catalog/people/3",
    "/api/v2/catalog/people/4",
  ]);
  expect(reads.every((read) => read.url.searchParams.get("prefetch") === "true")).toBe(true);

  reads[0]!.resolve();
  await waitFor(() => expect(reads).toHaveLength(4));
  expect(reads[3]!.url.pathname).toBe("/api/v2/catalog/people/5");
  reads.slice(1).forEach((read) => read.resolve());
  await waitFor(() => expect(client.getQueryData(personKeys.detail("5"))).toBeDefined());
  expect(reads).toHaveLength(4);

  unmount();
  client.clear();
});

it("stops queued reads and cancels unobserved ones when the page unmounts", async () => {
  const reads = stubPeopleFetch();
  const client = new QueryClient();
  seedCapabilities(client);

  const { unmount } = renderPrefetch(client, ["1", "2", "3", "4"]);
  await waitFor(() => expect(reads).toHaveLength(3));

  unmount();

  await waitFor(() => expect(reads.every((read) => read.signal.aborted)).toBe(true));
  expect(reads).toHaveLength(3);
  expect(client.getQueryState(personKeys.detail("1"))?.fetchStatus).toBe("idle");
  client.clear();
});

it("does not prefetch from a server that would reject the prefetch marker", async () => {
  const reads = stubPeopleFetch();
  const client = new QueryClient();
  seedCapabilities(client, false);

  const { unmount } = renderPrefetch(client, ["1", "2"]);
  // One macrotask drains the capability read and any prefetch it would start.
  await new Promise((resolve) => setTimeout(resolve, 0));

  expect(reads).toHaveLength(0);
  unmount();
  client.clear();
});
