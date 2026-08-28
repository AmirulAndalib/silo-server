import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AdminSession } from "@/api/types";
import type { DashboardLayout } from "./useDashboardLayout";

const mocks = vi.hoisted(() => ({
  useAdminSessions: vi.fn(),
}));

vi.mock("@/hooks/queries/admin/stats", async () => {
  const actual = await vi.importActual<typeof import("@/hooks/queries/admin/stats")>(
    "@/hooks/queries/admin/stats",
  );
  return { ...actual, useAdminSessions: mocks.useAdminSessions };
});

import { DashboardGrid } from "./DashboardGrid";

function session(overrides: Partial<AdminSession> = {}): AdminSession {
  return {
    session_id: "s1",
    user_id: 1,
    username: "quick",
    media_file_id: 7,
    media_title: "Arrival",
    media_type: "movie",
    is_paused: false,
    started_at: new Date(Date.now() - 60_000).toISOString(),
    ...overrides,
  } as AdminSession;
}

/**
 * A layout holding only the now-playing widget: the grid renders whatever is in
 * `entries`, so a one-entry layout keeps the test off every other widget's data
 * hooks.
 */
function layoutWith(isCustomizing: boolean): DashboardLayout {
  return {
    entries: [{ id: "now-playing", span: 12, rows: 3 }],
    hiddenWidgets: [],
    isCustomizing,
    setCustomizing: vi.fn(),
    moveWidget: vi.fn(),
    resizeWidget: vi.fn(),
    setWidgetRange: vi.fn(),
    removeWidget: vi.fn(),
    addWidget: vi.fn(),
    resetLayout: vi.fn(),
  };
}

function renderGrid(isCustomizing = false) {
  return render(
    <MemoryRouter>
      <DashboardGrid
        layout={layoutWith(isCustomizing)}
        isAddPanelOpen={false}
        onAddPanelOpenChange={() => {}}
      />
    </MemoryRouter>,
  );
}

function rowsOf(container: HTMLElement): string | undefined {
  const host = container.querySelector<HTMLElement>('[data-widget-id="now-playing"]');
  return host?.style.getPropertyValue("--widget-rows");
}

describe("DashboardGrid collapse", () => {
  beforeEach(() => {
    mocks.useAdminSessions.mockReset();
  });

  it("gives back rows when a collapsible widget reports it is empty", async () => {
    mocks.useAdminSessions.mockReturnValue({ data: [], isLoading: false, error: null });

    const { container } = renderGrid();

    expect(await screen.findByText("Nothing playing right now")).toBeTruthy();
    expect(rowsOf(container)).toBe("1");
    expect(
      container.querySelector('[data-widget-id="now-playing"]')?.getAttribute("data-collapsed"),
    ).toBe("true");
  });

  it("keeps its full height while loading and after a failure", () => {
    mocks.useAdminSessions.mockReturnValue({ data: undefined, isLoading: true, error: null });
    const loading = renderGrid();
    expect(rowsOf(loading.container)).toBe("3");
    loading.unmount();

    mocks.useAdminSessions.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error("boom"),
    });
    const failed = renderGrid();
    expect(screen.getByText("Failed to load streams.")).toBeTruthy();
    expect(rowsOf(failed.container)).toBe("3");
  });

  it("stays full size in customize mode so drag and resize targets hold still", async () => {
    mocks.useAdminSessions.mockReturnValue({ data: [], isLoading: false, error: null });

    const { container } = renderGrid(true);

    expect(await screen.findByText("Nothing playing right now")).toBeTruthy();
    expect(rowsOf(container)).toBe("3");
    expect(
      container.querySelector('[data-widget-id="now-playing"]')?.getAttribute("data-collapsed"),
    ).toBeNull();
  });

  it("grows back to its placed height once a stream appears", async () => {
    mocks.useAdminSessions.mockReturnValue({ data: [], isLoading: false, error: null });

    const { container, rerender } = renderGrid();
    expect(rowsOf(container)).toBe("1");

    mocks.useAdminSessions.mockReturnValue({
      data: [session()],
      isLoading: false,
      error: null,
    });
    rerender(
      <MemoryRouter>
        <DashboardGrid
          layout={layoutWith(false)}
          isAddPanelOpen={false}
          onAddPanelOpenChange={() => {}}
        />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Arrival")).toBeTruthy();
    expect(rowsOf(container)).toBe("3");
  });
});
