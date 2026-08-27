// @vitest-environment jsdom

import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { DASHBOARD_WIDGETS, DEFAULT_LAYOUT } from "./registry";
import { DASHBOARD_LAYOUT_STORAGE_KEY, useDashboardLayout } from "./useDashboardLayout";
import type { DashboardLayoutEntry } from "./types";

function readStored(): { version: number; entries: DashboardLayoutEntry[] } {
  const raw = window.localStorage.getItem(DASHBOARD_LAYOUT_STORAGE_KEY);
  if (raw === null) {
    throw new Error("expected a persisted layout");
  }
  return JSON.parse(raw) as { version: number; entries: DashboardLayoutEntry[] };
}

function writeStored(entries: unknown) {
  window.localStorage.setItem(
    DASHBOARD_LAYOUT_STORAGE_KEY,
    JSON.stringify({ version: 1, entries }),
  );
}

describe("useDashboardLayout", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("uses the default layout when storage is empty", () => {
    const { result } = renderHook(() => useDashboardLayout());

    expect(result.current.entries).toEqual(DEFAULT_LAYOUT);
    expect(result.current.hiddenWidgets).toEqual([]);
    expect(result.current.isCustomizing).toBe(false);
  });

  it("falls back to the default layout on corrupt JSON", () => {
    window.localStorage.setItem(DASHBOARD_LAYOUT_STORAGE_KEY, "{not json");

    const { result } = renderHook(() => useDashboardLayout());

    expect(result.current.entries).toEqual(DEFAULT_LAYOUT);
  });

  it("falls back to the default layout on an unexpected shape", () => {
    window.localStorage.setItem(
      DASHBOARD_LAYOUT_STORAGE_KEY,
      JSON.stringify({ version: 2, entries: [{ id: "libraries", span: 7 }] }),
    );

    const { result } = renderHook(() => useDashboardLayout());

    expect(result.current.entries).toEqual(DEFAULT_LAYOUT);
  });

  it("drops unknown widget ids on load", () => {
    writeStored([
      { id: "libraries", span: 7 },
      { id: "not-a-widget", span: 6 },
      { id: "users", span: 5 },
    ]);

    const { result } = renderHook(() => useDashboardLayout());

    expect(result.current.entries).toEqual([
      { id: "libraries", span: 7 },
      { id: "users", span: 5 },
    ]);
  });

  it("clamps spans to the widget's [minSpan, maxSpan] on load", () => {
    writeStored([
      { id: "stat-movies", span: 1 }, // min 2
      { id: "now-playing", span: 40 }, // max 12
      { id: "users", span: "wide" }, // non-numeric -> defaultSpan
    ]);

    const { result } = renderHook(() => useDashboardLayout());

    expect(result.current.entries).toEqual([
      { id: "stat-movies", span: 2 },
      { id: "now-playing", span: 12 },
      { id: "users", span: 5 },
    ]);
  });

  it("exposes hidden widgets in registry order", () => {
    writeStored([
      { id: "users", span: 5 },
      { id: "stat-storage", span: 3 },
    ]);

    const { result } = renderHook(() => useDashboardLayout());

    expect(result.current.hiddenWidgets.map((w) => w.id)).toEqual(
      DASHBOARD_WIDGETS.filter((w) => w.id !== "users" && w.id !== "stat-storage").map((w) => w.id),
    );
  });

  it("addWidget appends with the default span and persists", () => {
    writeStored([{ id: "libraries", span: 7 }]);
    const { result } = renderHook(() => useDashboardLayout());

    act(() => {
      result.current.addWidget("now-playing");
    });

    const expected = [
      { id: "libraries", span: 7 },
      { id: "now-playing", span: 12 },
    ];
    expect(result.current.entries).toEqual(expected);
    expect(readStored()).toEqual({ version: 1, entries: expected });
  });

  it("removeWidget hides the widget and persists", () => {
    const { result } = renderHook(() => useDashboardLayout());

    act(() => {
      result.current.removeWidget("trakt-sync");
    });

    expect(result.current.entries.some((entry) => entry.id === "trakt-sync")).toBe(false);
    expect(result.current.hiddenWidgets.map((w) => w.id)).toEqual(["trakt-sync"]);
    expect(readStored().entries.some((entry) => entry.id === "trakt-sync")).toBe(false);
  });

  it("moveWidget inserts before the target and persists", () => {
    writeStored([
      { id: "libraries", span: 7 },
      { id: "users", span: 5 },
      { id: "recent-activity", span: 12 },
    ]);
    const { result } = renderHook(() => useDashboardLayout());

    act(() => {
      result.current.moveWidget("recent-activity", "users");
    });

    expect(result.current.entries.map((entry) => entry.id)).toEqual([
      "libraries",
      "recent-activity",
      "users",
    ]);
    expect(readStored().entries.map((entry) => entry.id)).toEqual([
      "libraries",
      "recent-activity",
      "users",
    ]);
  });

  it("moveWidget with a null beforeId moves to the end", () => {
    writeStored([
      { id: "libraries", span: 7 },
      { id: "users", span: 5 },
    ]);
    const { result } = renderHook(() => useDashboardLayout());

    act(() => {
      result.current.moveWidget("libraries", null);
    });

    expect(result.current.entries.map((entry) => entry.id)).toEqual(["users", "libraries"]);
    expect(readStored().entries.map((entry) => entry.id)).toEqual(["users", "libraries"]);
  });

  it("resizeWidget clamps the span and persists", () => {
    writeStored([{ id: "users", span: 5 }]);
    const { result } = renderHook(() => useDashboardLayout());

    act(() => {
      result.current.resizeWidget("users", 6);
    });
    expect(result.current.entries).toEqual([{ id: "users", span: 6 }]);
    expect(readStored().entries).toEqual([{ id: "users", span: 6 }]);

    act(() => {
      result.current.resizeWidget("users", 99);
    });
    expect(result.current.entries).toEqual([{ id: "users", span: 8 }]);

    act(() => {
      result.current.resizeWidget("users", 1);
    });
    expect(result.current.entries).toEqual([{ id: "users", span: 4 }]);
    expect(readStored().entries).toEqual([{ id: "users", span: 4 }]);
  });

  it("resetLayout restores the defaults and clears storage", () => {
    const { result } = renderHook(() => useDashboardLayout());

    act(() => {
      result.current.removeWidget("users");
      result.current.resizeWidget("libraries", 12);
    });
    expect(result.current.entries).not.toEqual(DEFAULT_LAYOUT);

    act(() => {
      result.current.resetLayout();
    });

    expect(result.current.entries).toEqual(DEFAULT_LAYOUT);
    expect(window.localStorage.getItem(DASHBOARD_LAYOUT_STORAGE_KEY)).toBeNull();
  });

  it("round-trips a customized layout through localStorage", () => {
    const first = renderHook(() => useDashboardLayout());
    act(() => {
      first.result.current.removeWidget("stat-shows");
      first.result.current.resizeWidget("trakt-sync", 12);
      first.result.current.moveWidget("recent-activity", "now-playing");
    });
    const saved = first.result.current.entries;
    first.unmount();

    const second = renderHook(() => useDashboardLayout());
    expect(second.result.current.entries).toEqual(saved);
    expect(second.result.current.hiddenWidgets.map((w) => w.id)).toEqual(["stat-shows"]);
  });
});
