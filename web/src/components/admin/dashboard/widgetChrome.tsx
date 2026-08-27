import { createContext, useContext, useMemo, type ReactNode } from "react";

import type { WidgetId, WidgetRange } from "./types";

/**
 * What a widget knows about its own frame.
 *
 * Widgets are rendered by the grid with no props — the registry stores a bare
 * component type — so the one thing they need from their placement, the window
 * the admin picked, arrives through context instead.
 */
export interface WidgetChrome {
  /** The placed widget's id, or null when rendered outside the grid. */
  id: WidgetId | null;
  range: WidgetRange;
  setRange: (range: WidgetRange) => void;
}

const WidgetChromeContext = createContext<WidgetChrome | null>(null);

/**
 * The window a widget reads outside the grid: a plain day.
 *
 * Widgets are also rendered by unit tests and could be by any future host, and
 * a chart that threw because nobody wrapped it would be a worse failure than
 * one that quietly shows the usual day.
 */
const FALLBACK_CHROME: WidgetChrome = {
  id: null,
  range: "day",
  setRange: () => {},
};

export function WidgetChromeProvider({
  id,
  range,
  setRange,
  children,
}: {
  id: WidgetId;
  range: WidgetRange | undefined;
  setRange: (id: WidgetId, range: WidgetRange) => void;
  children: ReactNode;
}) {
  const value = useMemo<WidgetChrome>(
    () => ({
      id,
      range: range ?? FALLBACK_CHROME.range,
      setRange: (next: WidgetRange) => setRange(id, next),
    }),
    [id, range, setRange],
  );
  return <WidgetChromeContext.Provider value={value}>{children}</WidgetChromeContext.Provider>;
}

/** The window this widget is showing, and how to change it. */
export function useWidgetRange(): WidgetChrome {
  return useContext(WidgetChromeContext) ?? FALLBACK_CHROME;
}
