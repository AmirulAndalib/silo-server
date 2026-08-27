import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AdminDashboardLayoutResponse } from "@/api/types";

const mocks = vi.hoisted(() => ({
  api: vi.fn(),
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
}));

vi.mock("@/api/client", () => ({
  api: mocks.api,
}));

vi.mock("sonner", () => ({
  toast: {
    error: mocks.toastError,
    success: mocks.toastSuccess,
  },
}));

import {
  useAdminDashboardLayout,
  useResetAdminDashboardLayout,
  useSaveAdminDashboardLayout,
} from "./dashboardLayout";

function createQueryClient() {
  return new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  });
}

function createWrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return createElement(QueryClientProvider, { client: queryClient }, children);
  };
}

const layoutDocument = {
  version: 1,
  entries: [{ id: "libraries", span: 7, rows: 4 }],
};

describe("useAdminDashboardLayout", () => {
  beforeEach(() => {
    mocks.api.mockReset();
    mocks.toastError.mockReset();
    mocks.toastSuccess.mockReset();
  });

  it("reads the saved layout for the current admin", async () => {
    const response: AdminDashboardLayoutResponse = {
      layout: layoutDocument,
      updated_at: "2026-08-26T10:00:00Z",
    };
    mocks.api.mockResolvedValue(response);
    const { result } = renderHook(() => useAdminDashboardLayout(), {
      wrapper: createWrapper(createQueryClient()),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(mocks.api).toHaveBeenCalledWith("/admin/dashboard/layout");
    expect(result.current.data).toEqual(response);
  });

  it("reports a never-saved layout as null rather than an error", async () => {
    mocks.api.mockResolvedValue({ layout: null, updated_at: null });
    const { result } = renderHook(() => useAdminDashboardLayout(), {
      wrapper: createWrapper(createQueryClient()),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(result.current.data).toEqual({ layout: null, updated_at: null });
  });
});

describe("useSaveAdminDashboardLayout", () => {
  beforeEach(() => {
    mocks.api.mockReset();
    mocks.toastError.mockReset();
    mocks.toastSuccess.mockReset();
  });

  it("PUTs the layout document and seeds the query cache", async () => {
    const queryClient = createQueryClient();
    mocks.api.mockResolvedValue(undefined);
    const { result } = renderHook(() => useSaveAdminDashboardLayout(), {
      wrapper: createWrapper(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync(layoutDocument);
    });

    expect(mocks.api).toHaveBeenCalledWith("/admin/dashboard/layout", {
      method: "PUT",
      body: JSON.stringify({ layout: layoutDocument }),
    });
    const cached = queryClient.getQueryData<AdminDashboardLayoutResponse>([
      "admin",
      "dashboard",
      "layout",
    ]);
    expect(cached?.layout).toEqual(layoutDocument);
    expect(cached?.updated_at).toEqual(expect.any(String));
    expect(mocks.toastError).not.toHaveBeenCalled();
  });

  it("surfaces a save failure once without discarding local state", async () => {
    const queryClient = createQueryClient();
    mocks.api.mockRejectedValue(new Error("offline"));
    const { result } = renderHook(() => useSaveAdminDashboardLayout(), {
      wrapper: createWrapper(queryClient),
    });

    await act(async () => {
      try {
        await result.current.mutateAsync(layoutDocument);
      } catch {
        // The hook reports the failure through a toast; local state is kept.
      }
    });

    expect(mocks.toastError).toHaveBeenCalledTimes(1);
    expect(queryClient.getQueryData(["admin", "dashboard", "layout"])).toBeUndefined();
  });
});

describe("useResetAdminDashboardLayout", () => {
  beforeEach(() => {
    mocks.api.mockReset();
    mocks.toastError.mockReset();
    mocks.toastSuccess.mockReset();
  });

  it("DELETEs the layout and clears the cached document", async () => {
    const queryClient = createQueryClient();
    queryClient.setQueryData<AdminDashboardLayoutResponse>(["admin", "dashboard", "layout"], {
      layout: layoutDocument,
      updated_at: "2026-08-26T10:00:00Z",
    });
    mocks.api.mockResolvedValue(undefined);
    const { result } = renderHook(() => useResetAdminDashboardLayout(), {
      wrapper: createWrapper(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync();
    });

    expect(mocks.api).toHaveBeenCalledWith("/admin/dashboard/layout", { method: "DELETE" });
    expect(queryClient.getQueryData(["admin", "dashboard", "layout"])).toEqual({
      layout: null,
      updated_at: null,
    });
  });
});
