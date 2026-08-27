import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { api } from "@/api/client";
import type { AdminDashboardLayoutDocument, AdminDashboardLayoutResponse } from "@/api/types";
import { adminKeys } from "../keys";

const DASHBOARD_LAYOUT_PATH = "/admin/dashboard/layout";

// A single toast id per concern: a burst of failed saves (offline, server
// down) collapses into one message instead of stacking one per attempt.
const SAVE_TOAST_ID = "admin-dashboard-layout-save";
const RESET_TOAST_ID = "admin-dashboard-layout-reset";

/**
 * Reads this admin account's saved dashboard arrangement.
 *
 * `staleTime: Infinity` on purpose: the layout only changes when this admin
 * edits it, and every edit writes the new document straight into the cache, so
 * there is nothing for a refetch to discover. The dashboard paints from
 * localStorage first and adopts this result when it arrives.
 */
export function useAdminDashboardLayout() {
  return useQuery({
    queryKey: adminKeys.dashboardLayout(),
    queryFn: () => api<AdminDashboardLayoutResponse>(DASHBOARD_LAYOUT_PATH),
    staleTime: Infinity,
    gcTime: Infinity,
  });
}

export function useSaveAdminDashboardLayout() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (layout: AdminDashboardLayoutDocument) =>
      api<void>(DASHBOARD_LAYOUT_PATH, {
        method: "PUT",
        body: JSON.stringify({ layout }),
      }),
    onSuccess: (_data, layout) => {
      queryClient.setQueryData<AdminDashboardLayoutResponse>(adminKeys.dashboardLayout(), {
        layout,
        updated_at: new Date().toISOString(),
      });
    },
    onError: () => {
      // The layout still works from local state, so this is informational.
      toast.error("Failed to save the dashboard layout on the server", { id: SAVE_TOAST_ID });
    },
  });
}

export function useResetAdminDashboardLayout() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>(DASHBOARD_LAYOUT_PATH, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.setQueryData<AdminDashboardLayoutResponse>(adminKeys.dashboardLayout(), {
        layout: null,
        updated_at: null,
      });
    },
    onError: () => {
      toast.error("Failed to reset the dashboard layout on the server", { id: RESET_TOAST_ID });
    },
  });
}
