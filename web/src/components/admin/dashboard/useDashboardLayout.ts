import { useCallback, useMemo, useState } from "react";
import { DASHBOARD_WIDGETS, DEFAULT_LAYOUT, findDashboardWidget } from "./registry";
import type { DashboardLayoutEntry, DashboardWidgetDefinition, WidgetId } from "./types";

export const DASHBOARD_LAYOUT_STORAGE_KEY = "silo.admin-dashboard-layout.v1";

interface StoredLayout {
  version: 1;
  entries: DashboardLayoutEntry[];
}

function clampSpan(span: unknown, widget: DashboardWidgetDefinition): number {
  if (typeof span !== "number" || !Number.isFinite(span)) {
    return widget.defaultSpan;
  }
  return Math.min(widget.maxSpan, Math.max(widget.minSpan, Math.round(span)));
}

function loadStoredLayout(): DashboardLayoutEntry[] {
  let raw: string | null = null;
  try {
    raw = window.localStorage.getItem(DASHBOARD_LAYOUT_STORAGE_KEY);
  } catch {
    return [...DEFAULT_LAYOUT];
  }
  if (!raw) {
    return [...DEFAULT_LAYOUT];
  }
  try {
    const parsed = JSON.parse(raw) as Partial<StoredLayout> | null;
    if (!parsed || parsed.version !== 1 || !Array.isArray(parsed.entries)) {
      return [...DEFAULT_LAYOUT];
    }
    const seen = new Set<WidgetId>();
    const entries: DashboardLayoutEntry[] = [];
    for (const entry of parsed.entries) {
      if (!entry || typeof entry !== "object" || typeof entry.id !== "string") {
        continue;
      }
      const widget = findDashboardWidget(entry.id);
      if (!widget || seen.has(widget.id)) {
        continue;
      }
      seen.add(widget.id);
      entries.push({ id: widget.id, span: clampSpan(entry.span, widget) });
    }
    return entries;
  } catch {
    return [...DEFAULT_LAYOUT];
  }
}

function persistLayout(entries: DashboardLayoutEntry[]) {
  try {
    const stored: StoredLayout = { version: 1, entries };
    window.localStorage.setItem(DASHBOARD_LAYOUT_STORAGE_KEY, JSON.stringify(stored));
  } catch {
    // Storage may be unavailable (private mode, quota); the layout still works in-memory.
  }
}

export interface DashboardLayout {
  entries: DashboardLayoutEntry[];
  hiddenWidgets: DashboardWidgetDefinition[];
  isCustomizing: boolean;
  setCustomizing: (customizing: boolean) => void;
  moveWidget: (id: WidgetId, beforeId: WidgetId | null) => void;
  resizeWidget: (id: WidgetId, span: number) => void;
  removeWidget: (id: WidgetId) => void;
  addWidget: (id: WidgetId) => void;
  resetLayout: () => void;
}

export function useDashboardLayout(): DashboardLayout {
  const [entries, setEntries] = useState<DashboardLayoutEntry[]>(loadStoredLayout);
  const [isCustomizing, setCustomizing] = useState(false);

  const update = useCallback(
    (updater: (prev: DashboardLayoutEntry[]) => DashboardLayoutEntry[]) => {
      setEntries((prev) => {
        const next = updater(prev);
        if (next === prev) {
          return prev;
        }
        persistLayout(next);
        return next;
      });
    },
    [],
  );

  const moveWidget = useCallback(
    (id: WidgetId, beforeId: WidgetId | null) => {
      update((prev) => {
        if (id === beforeId) {
          return prev;
        }
        const moving = prev.find((entry) => entry.id === id);
        if (!moving) {
          return prev;
        }
        const without = prev.filter((entry) => entry.id !== id);
        if (beforeId === null) {
          return [...without, moving];
        }
        const index = without.findIndex((entry) => entry.id === beforeId);
        if (index === -1) {
          return [...without, moving];
        }
        return [...without.slice(0, index), moving, ...without.slice(index)];
      });
    },
    [update],
  );

  const resizeWidget = useCallback(
    (id: WidgetId, span: number) => {
      update((prev) => {
        const widget = findDashboardWidget(id);
        if (!widget) {
          return prev;
        }
        const nextSpan = clampSpan(span, widget);
        let changed = false;
        const next = prev.map((entry) => {
          if (entry.id !== id || entry.span === nextSpan) {
            return entry;
          }
          changed = true;
          return { ...entry, span: nextSpan };
        });
        return changed ? next : prev;
      });
    },
    [update],
  );

  const removeWidget = useCallback(
    (id: WidgetId) => {
      update((prev) =>
        prev.some((entry) => entry.id === id) ? prev.filter((entry) => entry.id !== id) : prev,
      );
    },
    [update],
  );

  const addWidget = useCallback(
    (id: WidgetId) => {
      update((prev) => {
        const widget = findDashboardWidget(id);
        if (!widget || prev.some((entry) => entry.id === id)) {
          return prev;
        }
        return [...prev, { id: widget.id, span: widget.defaultSpan }];
      });
    },
    [update],
  );

  const resetLayout = useCallback(() => {
    try {
      window.localStorage.removeItem(DASHBOARD_LAYOUT_STORAGE_KEY);
    } catch {
      // Ignore storage failures; state still resets below.
    }
    setEntries([...DEFAULT_LAYOUT]);
  }, []);

  const hiddenWidgets = useMemo(() => {
    const visible = new Set(entries.map((entry) => entry.id));
    return DASHBOARD_WIDGETS.filter((widget) => !visible.has(widget.id));
  }, [entries]);

  return {
    entries,
    hiddenWidgets,
    isCustomizing,
    setCustomizing,
    moveWidget,
    resizeWidget,
    removeWidget,
    addWidget,
    resetLayout,
  };
}
