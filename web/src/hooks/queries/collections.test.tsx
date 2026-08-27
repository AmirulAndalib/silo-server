import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { ProfileRequestContextSnapshot } from "@/api/client";
import { useSetCollectionSortPreference } from "./collections";

const apiMock = vi.hoisted(() => vi.fn());
const apiWithProfileRequestContextMock = vi.hoisted(() => vi.fn());

vi.mock("@/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/api/client")>("@/api/client");
  return {
    ...actual,
    api: apiMock,
    apiWithProfileRequestContext: apiWithProfileRequestContextMock,
  };
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

function profileAuth(profileId: string): ProfileRequestContextSnapshot {
  return {
    accessToken: "token",
    authContextVersion: 1,
    serverOrigin: globalThis.location?.origin ?? "",
    profileId,
    profileToken: null,
  };
}

function renderSortPreferenceHook() {
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return renderHook(() => useSetCollectionSortPreference(), { wrapper });
}

describe("useSetCollectionSortPreference", () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  it("serializes writes so the latest sort choice is persisted last", async () => {
    const first = deferred<unknown>();
    const second = deferred<unknown>();
    apiWithProfileRequestContextMock
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);

    const { result } = renderSortPreferenceHook();

    act(() => {
      result.current.mutate({
        collection_kind: "library",
        collection_id: "collection-1",
        field: "year",
        order: "desc",
        profileAuth: profileAuth("profile-1"),
      });
      result.current.mutate({
        collection_kind: "library",
        collection_id: "collection-1",
        field: "title",
        order: "asc",
        profileAuth: profileAuth("profile-1"),
      });
    });

    await waitFor(() => expect(apiWithProfileRequestContextMock).toHaveBeenCalledTimes(1));

    first.resolve({});
    await waitFor(() => expect(apiWithProfileRequestContextMock).toHaveBeenCalledTimes(2));
    expect(
      JSON.parse(apiWithProfileRequestContextMock.mock.calls[1]?.[2]?.body as string),
    ).toMatchObject({
      field: "title",
      order: "asc",
    });

    second.resolve({});
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });

  // These preferences are profile-scoped and the writes are serialized, so a
  // queued write must carry the profile that chose the sort rather than
  // whichever household member happens to be active when it finally sends.
  it("sends a queued write under the profile captured at selection time", async () => {
    const first = deferred<unknown>();
    apiWithProfileRequestContextMock.mockReturnValueOnce(first.promise).mockResolvedValue({});

    const { result } = renderSortPreferenceHook();
    const chooser = profileAuth("profile-1");

    act(() => {
      result.current.mutate({
        collection_kind: "watchlist",
        field: "title",
        order: "asc",
        profileAuth: chooser,
      });
      // Queued behind the first write; a household profile switch in between
      // must not retarget it.
      result.current.mutate({
        collection_kind: "favorites",
        field: "runtime",
        order: "desc",
        profileAuth: chooser,
      });
    });

    await waitFor(() => expect(apiWithProfileRequestContextMock).toHaveBeenCalledTimes(1));
    first.resolve({});
    await waitFor(() => expect(apiWithProfileRequestContextMock).toHaveBeenCalledTimes(2));

    for (const call of apiWithProfileRequestContextMock.mock.calls) {
      expect(call[0]).toBe("/collections/sort-preference");
      expect(call[1]).toBe(chooser);
      // The snapshot is request authority, not part of the stored preference.
      expect(JSON.parse(call[2]?.body as string)).not.toHaveProperty("profileAuth");
    }
  });
});
