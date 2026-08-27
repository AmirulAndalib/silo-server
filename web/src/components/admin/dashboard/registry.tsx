import type { DashboardLayoutEntry, DashboardWidgetDefinition, WidgetId } from "./types";
import {
  ActiveStreamsStatWidget,
  MoviesStatWidget,
  ShowsStatWidget,
  StorageStatWidget,
  UsersStatWidget,
} from "./widgets/statTiles";
import { TraktSyncWidget } from "./widgets/TraktSyncWidget";
import { NowPlayingWidget } from "./widgets/NowPlayingWidget";
import { LibrariesWidget } from "./widgets/LibrariesWidget";
import { UsersWidget } from "./widgets/UsersWidget";
import { RecentActivityWidget } from "./widgets/RecentActivityWidget";

export const DASHBOARD_WIDGETS: DashboardWidgetDefinition[] = [
  {
    id: "stat-active-streams",
    title: "Active streams",
    description: "Live count of playback sessions",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    Component: ActiveStreamsStatWidget,
  },
  {
    id: "stat-movies",
    title: "Movies",
    description: "Total movies and movie files",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    Component: MoviesStatWidget,
  },
  {
    id: "stat-shows",
    title: "Shows",
    description: "Total series and episode files",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    Component: ShowsStatWidget,
  },
  {
    id: "stat-users",
    title: "User count",
    description: "Registered accounts on the server",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    Component: UsersStatWidget,
  },
  {
    id: "stat-storage",
    title: "Storage",
    description: "Used space across all libraries",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    Component: StorageStatWidget,
  },
  {
    id: "trakt-sync",
    title: "Trakt sync",
    description: "Watch provider connection and 24h sync status",
    minSpan: 6,
    maxSpan: 12,
    defaultSpan: 9,
    Component: TraktSyncWidget,
  },
  {
    id: "now-playing",
    title: "Now playing",
    description: "Active streams with client, method, and progress",
    minSpan: 6,
    maxSpan: 12,
    defaultSpan: 12,
    Component: NowPlayingWidget,
  },
  {
    id: "libraries",
    title: "Libraries",
    description: "Library list with scan controls and progress",
    minSpan: 6,
    maxSpan: 12,
    defaultSpan: 7,
    Component: LibrariesWidget,
  },
  {
    id: "users",
    title: "Users",
    description: "Recent user accounts with role and status",
    minSpan: 4,
    maxSpan: 8,
    defaultSpan: 5,
    Component: UsersWidget,
  },
  {
    id: "recent-activity",
    title: "Recent activity",
    description: "Feed of recently started playback sessions",
    minSpan: 6,
    maxSpan: 12,
    defaultSpan: 12,
    Component: RecentActivityWidget,
  },
];

const WIDGETS_BY_ID = new Map(DASHBOARD_WIDGETS.map((widget) => [widget.id, widget]));

export function getDashboardWidget(id: WidgetId): DashboardWidgetDefinition {
  const widget = WIDGETS_BY_ID.get(id);
  if (!widget) {
    throw new Error(`Unknown dashboard widget: ${id}`);
  }
  return widget;
}

export function findDashboardWidget(id: string): DashboardWidgetDefinition | undefined {
  return WIDGETS_BY_ID.get(id as WidgetId);
}

export const DEFAULT_LAYOUT: DashboardLayoutEntry[] = [
  { id: "stat-active-streams", span: 3 },
  { id: "stat-movies", span: 3 },
  { id: "stat-shows", span: 3 },
  { id: "stat-users", span: 3 },
  { id: "stat-storage", span: 3 },
  { id: "trakt-sync", span: 9 },
  { id: "now-playing", span: 12 },
  { id: "libraries", span: 7 },
  { id: "users", span: 5 },
  { id: "recent-activity", span: 12 },
];
