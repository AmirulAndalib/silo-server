import type React from "react";

export type WidgetId =
  | "stat-active-streams"
  | "stat-movies"
  | "stat-shows"
  | "stat-users"
  | "stat-storage"
  | "trakt-sync"
  | "now-playing"
  | "libraries"
  | "users"
  | "recent-activity";

export interface DashboardWidgetDefinition {
  id: WidgetId;
  title: string;
  description: string;
  minSpan: number;
  maxSpan: number;
  defaultSpan: number;
  Component: React.ComponentType;
}

export interface DashboardLayoutEntry {
  id: WidgetId;
  span: number;
}
