import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { SettingsOverviewModel } from "@/hooks/admin/useSettingsOverview";
import { ADMIN_SETTINGS_NAV } from "@/lib/adminSettingsSearch";

import AdminSettingsLayout from "./AdminSettingsLayout";

const mocks = vi.hoisted(() => ({
  useAdminServerStatus: vi.fn(),
  useSettingsOverview: vi.fn(),
}));

// The layout only needs the active tab's component to render; a loading form
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

function overviewModel(
  sectionStatus: Partial<SettingsOverviewModel["sectionStatus"]> = {},
): SettingsOverviewModel {
  return {
    isLoading: false,
    tiles: [],
    cards: [],
    sectionStatus: sectionStatus as SettingsOverviewModel["sectionStatus"],
  };
}

beforeEach(() => {
  mocks.useAdminServerStatus.mockReturnValue({ data: { restart_required: false } });
  mocks.useSettingsOverview.mockReturnValue(overviewModel());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderLayout(search = "") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/admin/settings${search}`]}>
        <AdminSettingsLayout />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function renderInteractiveLayout(search = "") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/admin/settings${search}`]}>
        <AdminSettingsLayout />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("AdminSettingsLayout", () => {
  it("lands on the overview when no tab is selected", () => {
    renderInteractiveLayout();

    expect(screen.getByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
    // The overview is full width: the section rail belongs to a section page.
    expect(
      screen.queryByRole("navigation", { name: "Admin settings sections" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "All settings" })).not.toBeInTheDocument();
  });

  it("renders the rail with Overview and every settings section", () => {
    vi.stubGlobal("scrollTo", vi.fn());
    renderInteractiveLayout("?tab=general");

    const rail = screen.getByRole("navigation", { name: "Admin settings sections" });
    const labels = [...rail.querySelectorAll("a")].map((link) =>
      link.textContent?.replace("Needs attention", "").trim(),
    );

    expect(labels).toEqual(["Overview", ...ADMIN_SETTINGS_NAV.map((item) => item.label)]);
  });

  it("marks only the open section as the current rail item", () => {
    vi.stubGlobal("scrollTo", vi.fn());
    renderInteractiveLayout("?tab=playback");

    const rail = screen.getByRole("navigation", { name: "Admin settings sections" });
    const current = [...rail.querySelectorAll('a[aria-current="page"]')];

    expect(current).toHaveLength(1);
    expect(current[0]).toHaveTextContent("Playback");
    expect(current[0]).toHaveAttribute("href", "/admin/settings?tab=playback");
  });

  it("dots only the sections that need attention", () => {
    vi.stubGlobal("scrollTo", vi.fn());
    mocks.useSettingsOverview.mockReturnValue(
      overviewModel({ playback: "warn", "watch-sync": "off", general: "ok" }),
    );

    const markup = renderLayout("?tab=general");

    expect(markup).toContain("bg-amber-500");
    expect(markup).toContain("Needs attention");
    // A section that is merely off, or healthy, gets no dot at all.
    expect(markup).not.toContain("bg-emerald-500");
    expect(markup).not.toContain("Not set up");
  });

  it("mounts every settings tab", () => {
    vi.stubGlobal("scrollTo", vi.fn());

    for (const item of ADMIN_SETTINGS_NAV) {
      const { unmount } = renderInteractiveLayout(`?tab=${item.id}`);

      expect(screen.getByRole("region", { name: `${item.label} settings` })).toBeInTheDocument();
      unmount();
    }
  });

  it("offers a mobile way back to the overview from a section", () => {
    vi.stubGlobal("scrollTo", vi.fn());
    renderInteractiveLayout("?tab=general");

    expect(screen.getByRole("link", { name: "All settings" })).toHaveAttribute(
      "href",
      "/admin/settings",
    );
  });

  it("focuses the section and resets scroll when a tab opens", async () => {
    const scrollTo = vi.fn();
    vi.stubGlobal("scrollTo", scrollTo);
    renderInteractiveLayout("?tab=general");

    const region = await screen.findByRole("region", { name: "General settings" });
    expect(scrollTo).toHaveBeenCalledWith(0, 0);
    expect(region).toHaveFocus();

    region.scrollTop = 400;
    await userEvent.click(screen.getByRole("link", { name: /Storage & Database/, hidden: true }));

    const next = await screen.findByRole("region", { name: "Storage & Database settings" });
    expect(next.scrollTop).toBe(0);
    expect(next).toHaveFocus();
  });

  it("renders exactly one restart banner for the whole settings area", () => {
    vi.stubGlobal("scrollTo", vi.fn());
    mocks.useAdminServerStatus.mockReturnValue({ data: { restart_required: true } });

    renderInteractiveLayout("?tab=general");

    expect(screen.getAllByText("Restart required")).toHaveLength(1);
  });

  it("resolves every legacy tab id to the tab that absorbed it", () => {
    vi.stubGlobal("scrollTo", vi.fn());
    const aliases: Record<string, string> = {
      jellyfin: "compatibility",
      "compatibility-proxies": "compatibility",
      branding: "appearance",
      theming: "appearance",
      overlays: "appearance",
      "rate-limiting": "security",
      scanner: "library",
      search: "library",
      intro: "library",
      subtitles: "providers",
      integrations: "providers",
      "watch-providers": "watch-sync",
      downloads: "playback",
      email: "notifications",
      database: "infrastructure",
      storage: "infrastructure",
      "log-retention": "infrastructure",
    };

    for (const [legacy, current] of Object.entries(aliases)) {
      expect(renderLayout(`?tab=${legacy}`)).toBe(renderLayout(`?tab=${current}`));
    }
  });

  it("carries no search box of its own; the admin header owns search", () => {
    vi.stubGlobal("scrollTo", vi.fn());
    renderInteractiveLayout("?tab=general");

    expect(screen.queryByRole("searchbox", { name: "Search settings" })).not.toBeInTheDocument();
  });

  it("keeps `ai` pointing at the AI tab rather than an alias", () => {
    vi.stubGlobal("scrollTo", vi.fn());
    renderInteractiveLayout("?tab=ai");

    expect(screen.getByRole("region", { name: "AI Services settings" })).toBeInTheDocument();
  });
});
