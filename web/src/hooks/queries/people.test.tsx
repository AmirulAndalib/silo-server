import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import itemFixture from "../../../../contracts/api/v2/fixtures/get_catalog_item_ok.json";
import { catalogItemDetailFromV2 } from "@/api/v2/catalog";
import { installPolicyStorageMocks, jsonResponse } from "@/pages/admin-policy/policyTestUtils";
import { useCatalogItemDetail } from "./catalogRead";
import { catalogKeys, itemKeys } from "./keys";
import { useRefreshPerson, useUpdatePersonMetadata } from "./people";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

// Person IDs can exceed Number.MAX_SAFE_INTEGER. Match the route's string ID.
const personId = "9007199254740993";
const oldPhoto = "https://images.example.test/old.jpg";
const newPhoto = "https://images.example.test/new.jpg";
const credit = {
  person_id: personId,
  name: "Actor",
  character: "Lead",
  order: 0,
  photo_url: oldPhoto,
};

describe("person refresh and cached item credits", () => {
  beforeEach(installPolicyStorageMocks);
  afterEach(() => vi.unstubAllGlobals());

  function setup({ fail = false } = {}) {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 120_000 } },
    });
    const detail = catalogItemDetailFromV2({ ...itemFixture, cast: [credit] });
    const relatedKeys = [
      catalogKeys.itemDetail(detail.content_id),
      catalogKeys.itemDetail(detail.content_id, 12),
      itemKeys.detail(detail.content_id, 12),
    ];
    for (const key of relatedKeys) client.setQueryData(key, detail);
    const unrelatedKey = catalogKeys.itemDetail("unrelated");
    client.setQueryData(unrelatedKey, { ...detail, content_id: "unrelated", cast: [] });
    const crewKey = catalogKeys.itemDetail("crew-item", 12);
    client.setQueryData(crewKey, {
      ...detail,
      content_id: "crew-item",
      cast: [],
      crew: [{ person_id: personId, name: "Actor", job: "Director" }],
    });
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      const path = String(input);
      if (path.startsWith("/api/v2/catalog/items/")) {
        return jsonResponse({ ...itemFixture, cast: [{ ...credit, photo_url: newPhoto }] });
      }
      if (fail) return jsonResponse({ title: "Refresh failed", status: 500 }, 500);
      if (path.startsWith("/api/v2/admin/people/")) {
        return jsonResponse({ id: personId, name: "Actor", photo_url: newPhoto });
      }
      return jsonResponse({ status: "queued" });
    });
    vi.stubGlobal("fetch", fetchMock);
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    return { client, detail, relatedKeys, unrelatedKey, crewKey, wrapper };
  }

  it.each(["refresh", "edit"] as const)(
    "shows the refreshed cast photo when returning to a cached item after %s",
    async (action) => {
      const { client, detail, relatedKeys, unrelatedKey, crewKey, wrapper } = setup();
      const { result } = renderHook(
        () => ({
          refresh: useRefreshPerson(personId, true),
          edit: useUpdatePersonMetadata(personId),
        }),
        { wrapper },
      );
      await act(async () => {
        if (action === "refresh") await result.current.refresh.mutateAsync();
        else await result.current.edit.mutateAsync({ name: "Actor" });
      });

      for (const key of relatedKeys) expect(client.getQueryState(key)?.isInvalidated).toBe(true);
      expect(client.getQueryState(unrelatedKey)?.isInvalidated).toBe(false);
      expect(client.getQueryState(crewKey)?.isInvalidated).toBe(true);

      const returned = renderHook(() => useCatalogItemDetail(detail.content_id, 12), { wrapper });
      await waitFor(() => expect(returned.result.current.data?.cast[0]?.photo_url).toBe(newPhoto));
    },
  );

  it.each(["queued", "failed"] as const)(
    "keeps cached item data when a refresh is only %s",
    async (mode) => {
      const { client, relatedKeys, wrapper } = setup({ fail: mode === "failed" });
      const { result } = renderHook(() => useRefreshPerson(personId, mode === "failed"), {
        wrapper,
      });
      await act(async () => {
        if (mode === "failed") await expect(result.current.mutateAsync()).rejects.toThrow();
        else await result.current.mutateAsync();
      });
      for (const key of relatedKeys) expect(client.getQueryState(key)?.isInvalidated).toBe(false);
    },
  );
});
