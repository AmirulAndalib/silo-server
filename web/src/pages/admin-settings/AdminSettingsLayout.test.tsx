import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { SettingsOverviewModel } from "@/hooks/admin/useSettingsOverview";
import { ADMIN_SETTINGS_NAV, LEGACY_ADMIN_SETTINGS_PAGE_ALIASES } from "@/lib/adminSettingsSearch";

import AdminSettingsLayout from "./AdminSettingsLayout";

const mocks = vi.hoisted(() => ({
  useAdminServerStatus: vi.fn(),
  useSettingsOverview: vi.fn(),
}));

// The layout only needs the active page's component to render; a loading form
// keeps every settings page on its skeleton state so no other hooks fire.
vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: () => ({
    isLoading: true,
    getValue: () => "",
    setValue: () => {},
    resetValue: () => {},
    dirtyCount: 0,
    dirtyKeys: [],
    isDirty: () => false,
    save: () => {},
    discard: () => {},
    isSaving: false,
    restartRequired: false,
    sensitiveConfigured: [],
    sensitiveManagedByEnv: [],
    sensitiveStatusReady: false,
    sensitiveStatusError: null,
    buildConnectionCheckRequest: () => ({}),
  }),
}));

vi.mock("@/hooks/queries/admin/settings", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/hooks/queries/admin/settings")>()),
  useAdminServerStatus: (...args: unknown[]) => mocks.useAdminServerStatus(...args),
}));

vi.mock("@/hooks/admin/useSettingsOverview", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/hooks/admin/useSettingsOverview")>()),
  useSettingsOverview: () => mocks.useSettingsOverview(),
}));

function overviewModel(): SettingsOverviewModel {
  return {
    isLoading: false,
    tiles: [],
    cards: [],
  };
}

beforeEach(() => {
  mocks.useAdminServerStatus.mockReturnValue({ data: { restart_required: false } });
  mocks.useSettingsOverview.mockReturnValue(overviewModel());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function TestRoutes() {
  return (
    <Routes>
      <Route path="/admin/settings/*" element={<AdminSettingsLayout />} />
    </Routes>
  );
}

function renderInteractiveLayout(suffix = "") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/admin/settings${suffix}`]}>
        <TestRoutes />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("AdminSettingsLayout", () => {
  it("lands on the overview at the settings index", () => {
    renderInteractiveLayout();

    expect(screen.getByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "All settings" })).not.toBeInTheDocument();
  });

  it("renders a settings category with the page rail beside it", () => {
    vi.stubGlobal("scrollTo", vi.fn());
    renderInteractiveLayout("/general");

    expect(screen.getByRole("region", { name: "General settings" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "All settings" })).toHaveAttribute(
      "href",
      "/admin/settings",
    );

    // The rail lists every settings page and marks the open one.
    const rail = screen.getByRole("navigation", { name: "Settings pages" });
    for (const item of ADMIN_SETTINGS_NAV) {
      const link = within(rail).getByRole("link", { name: item.label });
      expect(link).toHaveAttribute("href", `/admin/settings/${item.id}`);
    }
    expect(within(rail).getByRole("link", { name: "General" })).toHaveAttribute(
      "aria-current",
      "page",
    );
  });

  it("mounts every settings page at its own route", () => {
    vi.stubGlobal("scrollTo", vi.fn());

    for (const item of ADMIN_SETTINGS_NAV) {
      const { unmount } = renderInteractiveLayout(`/${item.id}`);

      expect(screen.getByRole("region", { name: `${item.label} settings` })).toBeInTheDocument();
      unmount();
    }
  });

  it("focuses the settings page and resets document scroll when it opens", async () => {
    const scrollTo = vi.fn();
    vi.stubGlobal("scrollTo", scrollTo);
    renderInteractiveLayout("/general");

    const region = await screen.findByRole("region", { name: "General settings" });
    expect(scrollTo).toHaveBeenCalledWith(0, 0);
    expect(region).toHaveFocus();
  });

  it("renders exactly one restart banner for the whole settings area", () => {
    vi.stubGlobal("scrollTo", vi.fn());
    mocks.useAdminServerStatus.mockReturnValue({ data: { restart_required: true } });

    renderInteractiveLayout("/general");

    expect(screen.getAllByText("Restart required")).toHaveLength(1);
  });

  it("redirects legacy query-string tabs to their canonical pages", async () => {
    vi.stubGlobal("scrollTo", vi.fn());

    for (const [legacy, current] of Object.entries(LEGACY_ADMIN_SETTINGS_PAGE_ALIASES)) {
      const label = ADMIN_SETTINGS_NAV.find((item) => item.id === current)?.label;
      expect(label).toBeDefined();

      const { unmount } = renderInteractiveLayout(`?tab=${legacy}`);
      expect(await screen.findByRole("region", { name: `${label} settings` })).toBeInTheDocument();
      unmount();
    }
  });

  it("redirects a retired page route to the page that absorbed it", async () => {
    vi.stubGlobal("scrollTo", vi.fn());
    renderInteractiveLayout("/jellyfin");

    expect(
      await screen.findByRole("region", { name: "Compatibility settings" }),
    ).toBeInTheDocument();
  });

  it("redirects an unknown settings page to the overview", async () => {
    vi.stubGlobal("scrollTo", vi.fn());
    renderInteractiveLayout("/not-a-page");

    expect(await screen.findByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
  });

  it("filters the page rail from the settings search box", async () => {
    vi.stubGlobal("scrollTo", vi.fn());
    renderInteractiveLayout("/general");

    const box = screen.getByRole("searchbox", { name: "Search settings" });
    await userEvent.type(box, "transcode");

    const rail = screen.getByRole("navigation", { name: "Settings pages" });
    expect(within(rail).getByRole("link", { name: "Playback" })).toBeInTheDocument();
    expect(within(rail).queryByRole("link", { name: "General" })).not.toBeInTheDocument();

    await userEvent.clear(box);
    expect(within(rail).getByRole("link", { name: "General" })).toBeInTheDocument();
  });

  it("keeps `ai` pointing at the AI Services page rather than an alias", () => {
    vi.stubGlobal("scrollTo", vi.fn());
    renderInteractiveLayout("/ai");

    expect(screen.getByRole("region", { name: "AI Services settings" })).toBeInTheDocument();
  });
});
