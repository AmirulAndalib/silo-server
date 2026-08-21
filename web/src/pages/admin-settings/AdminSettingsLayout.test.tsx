import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import AdminSettingsLayout from "./AdminSettingsLayout";

const mocks = vi.hoisted(() => ({
  useAdminServerStatus: vi.fn(),
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

beforeEach(() => {
  mocks.useAdminServerStatus.mockReturnValue({ data: { restart_required: false } });
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
  it("renders the grouped navigation sections", () => {
    const markup = renderLayout();

    for (const group of ["Server", "Connections &amp; Data"]) {
      expect(markup).toContain(`>${group}<`);
    }
  });

  it("names each settings group exactly once", () => {
    renderInteractiveLayout();

    // The category jump bar used to repeat every group name and count directly
    // above the headings that already carry them.
    expect(
      screen.queryByRole("navigation", { name: "Admin settings sections categories" }),
    ).not.toBeInTheDocument();
    for (const group of ["Server", "Connections & Data"]) {
      expect(screen.getAllByRole("heading", { name: group })).toHaveLength(1);
      expect(
        screen.queryByRole("link", { name: new RegExp(`^${group}, \\d+ settings`) }),
      ).toBeNull();
    }
  });

  it("uses one desktop grid and card geometry for every settings group", () => {
    const markup = renderLayout();

    expect(markup.match(/2xl:grid-cols-4/g)).toHaveLength(2);
    expect(markup).not.toContain("2xl:grid-cols-3");
    expect(markup.match(/lg:h-28/g)).toHaveLength(9);
    expect(markup.match(/lg:line-clamp-3/g)).toHaveLength(9);
  });

  it("renders every settings tab", () => {
    const markup = renderLayout();

    for (const label of [
      "General",
      "Appearance",
      "Security &amp; Access",
      "Library &amp; Metadata",
      "Playback",
      "Integrations",
      "Notifications",
      "Compatibility",
      "Infrastructure",
    ]) {
      expect(markup).toContain(label);
    }
  });

  it("renders the settings index at the root and preserves tab deep links", () => {
    renderInteractiveLayout();

    expect(screen.getByRole("link", { name: /General.*Server identity/ })).toHaveAttribute(
      "href",
      "/admin/settings?tab=general",
    );
    expect(screen.queryByRole("link", { name: "All settings" })).not.toBeInTheDocument();

    const detail = renderLayout("?tab=general");
    expect(detail).toContain('aria-current="page"');
    expect(detail).toContain('href="/admin/settings"');
  });

  it("focuses the detail heading and resets scroll when an overview link opens", async () => {
    const scrollTo = vi.fn();
    vi.stubGlobal("scrollTo", scrollTo);
    renderInteractiveLayout();

    await userEvent.click(screen.getByRole("link", { name: /Infrastructure.*Redis/ }));

    const detailRegion = await screen.findByRole("region", {
      name: "Infrastructure settings",
    });
    expect(scrollTo).toHaveBeenCalledWith(0, 0);
    expect(detailRegion).toHaveFocus();
  });

  it("adds a mobile detail heading when the settings component has none", () => {
    vi.stubGlobal("scrollTo", vi.fn());

    renderInteractiveLayout("?tab=appearance");

    expect(screen.getByRole("heading", { name: "Appearance", level: 2 })).toHaveFocus();
  });

  it("resets the scrolling detail pane when switching admin tabs", async () => {
    vi.stubGlobal("scrollTo", vi.fn());
    renderInteractiveLayout("?tab=general");

    const generalRegion = screen.getByRole("region", { name: "General settings" });
    generalRegion.scrollTop = 400;

    await userEvent.click(screen.getByRole("button", { name: /Infrastructure/ }));

    const infrastructureRegion = await screen.findByRole("region", {
      name: "Infrastructure settings",
    });
    expect(infrastructureRegion.scrollTop).toBe(0);
    expect(infrastructureRegion).toHaveFocus();
  });

  it("surfaces durable restart-required state above the active tab", () => {
    mocks.useAdminServerStatus.mockReturnValue({ data: { restart_required: true } });

    const markup = renderLayout();

    expect(markup).toContain("Server restart required for saved settings to take effect.");
  });

  it("resolves every legacy tab id to the tab that absorbed it", () => {
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
      subtitles: "integrations",
      ai: "integrations",
      "watch-providers": "integrations",
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

  it("badges Infrastructure as advanced in the settings nav", () => {
    vi.stubGlobal("scrollTo", vi.fn());

    const markup = renderLayout("?tab=general");

    expect(markup).toContain(">Advanced<");
  });

  it("filters admin settings sections from the search box", async () => {
    renderInteractiveLayout();

    await userEvent.type(screen.getByRole("searchbox", { name: "Search settings" }), "redis");

    expect(screen.getAllByRole("link", { name: /Infrastructure/ })).toHaveLength(1);
    expect(screen.queryByRole("link", { name: /Playback/ })).not.toBeInTheDocument();
    expect(screen.getByText("1 match")).toBeInTheDocument();
  });

  it("matches individual admin setting labels", async () => {
    renderInteractiveLayout();

    await userEvent.type(
      screen.getByRole("searchbox", { name: "Search settings" }),
      "silenced log messages",
    );

    expect(screen.getAllByRole("link", { name: /General/ })).toHaveLength(1);
    expect(screen.queryByRole("link", { name: /Playback/ })).not.toBeInTheDocument();
  });

  it("focuses admin settings search with Cmd+K", () => {
    renderInteractiveLayout();

    const searchBox = screen.getByRole("searchbox", { name: "Search settings" });
    fireEvent.keyDown(document, { key: "k", metaKey: true });

    expect(searchBox).toHaveFocus();
  });

  it("focuses admin settings search with Ctrl+K", () => {
    renderInteractiveLayout();

    const searchBox = screen.getByRole("searchbox", { name: "Search settings" });
    fireEvent.keyDown(document, { key: "k", ctrlKey: true });

    expect(searchBox).toHaveFocus();
  });

  it("does not consume Cmd+K when the admin detail search is hidden", () => {
    vi.stubGlobal(
      "matchMedia",
      vi.fn(() => ({ matches: false })),
    );
    vi.stubGlobal("scrollTo", vi.fn());
    renderInteractiveLayout("?tab=general");

    const event = new KeyboardEvent("keydown", {
      key: "k",
      metaKey: true,
      cancelable: true,
    });

    expect(document.dispatchEvent(event)).toBe(true);
    expect(event.defaultPrevented).toBe(false);
  });
});
