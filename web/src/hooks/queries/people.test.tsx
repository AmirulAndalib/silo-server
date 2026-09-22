import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { setProfileId } from "@/api/client";
import type { PersonSearchMediaScope } from "@/api/v2/people";
import { installPolicyStorageMocks, jsonResponse } from "@/pages/admin-policy/policyTestUtils";
import { usePersonSearch } from "./people";

beforeEach(() => {
  installPolicyStorageMocks();
  setProfileId("test-profile");
});
afterEach(() => vi.unstubAllGlobals());

it("keeps cached people results separate for Media, Audiobooks, and All", async () => {
  const fetchMock = vi.fn<typeof fetch>(async (input) => {
    const scope = new URL(String(input), "http://localhost").searchParams.get("media_scope");
    return jsonResponse({ items: [{ id: "9007199254740993", name: scope ?? "all" }] });
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const { result, rerender, unmount } = renderHook(
    ({ scope }: { scope?: PersonSearchMediaScope }) => usePersonSearch("Nathan", 20, true, scope),
    { wrapper, initialProps: { scope: "video" as PersonSearchMediaScope | undefined } },
  );
  await waitFor(() => expect(result.current.data?.[0]?.name).toBe("video"));
  rerender({ scope: "audiobook" });
  expect(result.current.data).toBeUndefined();
  await waitFor(() => expect(result.current.data?.[0]?.name).toBe("audiobook"));
  rerender({ scope: undefined });
  await waitFor(() => expect(result.current.data?.[0]?.name).toBe("all"));
  rerender({ scope: "video" });
  expect(result.current.data?.[0]?.name).toBe("video");
  expect(fetchMock).toHaveBeenCalledTimes(3);
  unmount();
  client.clear();
});
