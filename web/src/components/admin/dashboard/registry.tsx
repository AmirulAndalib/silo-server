import type {
  DashboardLayoutEntry,
  DashboardWidgetDefinition,
  WidgetId,
  WidgetRangeOptions,
} from "./types";
import {
  ActiveStreamsStatWidget,
  EgressNowStatWidget,
  MoviesStatWidget,
  ProfilesActiveStatWidget,
  ShowsStatWidget,
  StorageStatWidget,
  TranscodeShareStatWidget,
  UsersStatWidget,
} from "./widgets/statTiles";
import { ConcurrentStreamsWidget } from "./widgets/ConcurrentStreamsWidget";
import { EgressWidget } from "./widgets/EgressWidget";
import { HealthStripWidget } from "./widgets/HealthStripWidget";
import { PlaybackActivityWidget } from "./widgets/PlaybackActivityWidget";
import { PlaybackReliabilityWidget } from "./widgets/PlaybackReliabilityWidget";
import { TopProfilesWidget } from "./widgets/TopProfilesWidget";
import { TopTitlesWidget } from "./widgets/TopTitlesWidget";
import { TraktSyncWidget } from "./widgets/TraktSyncWidget";
import { NowPlayingWidget } from "./widgets/NowPlayingWidget";
import { TranscodeNodesWidget } from "./widgets/TranscodeNodesWidget";
import { ScannerWidget } from "./widgets/ScannerWidget";
import { LibrariesWidget } from "./widgets/LibrariesWidget";
import { UsersWidget } from "./widgets/UsersWidget";
import { ScanActivityWidget } from "./widgets/ScanActivityWidget";
import { RecentErrorsWidget } from "./widgets/RecentErrorsWidget";
import { RecentActivityWidget } from "./widgets/RecentActivityWidget";

/**
 * Sampled charts read `dashboard_metric_samples`, which the sampler keeps for a
 * month, so they offer the full spread down to a single hour.
 */
const SAMPLED_RANGES: WidgetRangeOptions = {
  allowed: ["hour", "day", "week", "month"],
  default: "day",
};

/**
 * Leaderboards start at a day: an hour of watch history ranks too little to be
 * worth a chart, and the endpoint's window is measured in days anyway.
 */
const LEADERBOARD_RANGES: WidgetRangeOptions = {
  allowed: ["day", "week", "month"],
  default: "week",
};

export const DASHBOARD_WIDGETS: DashboardWidgetDefinition[] = [
  {
    id: "stat-active-streams",
    title: "Active streams",
    description: "Live count of playback sessions",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    minRows: 1,
    maxRows: 2,
    defaultRows: 1,
    Component: ActiveStreamsStatWidget,
  },
  {
    id: "stat-egress-now",
    title: "Egress now",
    description: "Egress the deployment is serving this minute",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    minRows: 1,
    maxRows: 2,
    defaultRows: 1,
    Component: EgressNowStatWidget,
  },
  {
    id: "stat-transcode-share",
    title: "Transcode share",
    description: "Share of live streams being transcoded",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    minRows: 1,
    maxRows: 2,
    defaultRows: 1,
    Component: TranscodeShareStatWidget,
  },
  {
    id: "stat-profiles-active",
    title: "Profiles · 24h",
    description: "Profiles that watched something in the last 24 hours",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    minRows: 1,
    maxRows: 2,
    defaultRows: 1,
    Component: ProfilesActiveStatWidget,
  },
  {
    id: "stat-movies",
    title: "Movies",
    description: "Total movies and movie files",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    minRows: 1,
    maxRows: 2,
    defaultRows: 1,
    Component: MoviesStatWidget,
  },
  {
    id: "stat-shows",
    title: "Shows",
    description: "Total series and episode files",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    minRows: 1,
    maxRows: 2,
    defaultRows: 1,
    Component: ShowsStatWidget,
  },
  {
    id: "stat-users",
    title: "User count",
    description: "Registered accounts on the server",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    minRows: 1,
    maxRows: 2,
    defaultRows: 1,
    Component: UsersStatWidget,
  },
  {
    id: "stat-storage",
    title: "Storage",
    description: "Used space across all libraries",
    minSpan: 2,
    maxSpan: 4,
    defaultSpan: 3,
    minRows: 1,
    maxRows: 2,
    defaultRows: 1,
    Component: StorageStatWidget,
  },
  {
    id: "health-strip",
    title: "Server health",
    description: "Version, uptime, dependencies, nodes, and 24h error count",
    minSpan: 6,
    maxSpan: 12,
    defaultSpan: 12,
    minRows: 1,
    maxRows: 2,
    defaultRows: 1,
    Component: HealthStripWidget,
  },
  {
    id: "playback-24h",
    title: "Playback activity",
    description: "Playback starts stacked by play method",
    minSpan: 6,
    maxSpan: 12,
    defaultSpan: 6,
    minRows: 2,
    maxRows: 5,
    defaultRows: 3,
    ranges: SAMPLED_RANGES,
    Component: PlaybackActivityWidget,
  },
  {
    id: "concurrent-streams-24h",
    title: "Concurrent streams",
    description: "Sampled concurrent playback sessions",
    minSpan: 4,
    maxSpan: 12,
    defaultSpan: 6,
    minRows: 2,
    maxRows: 5,
    defaultRows: 3,
    ranges: SAMPLED_RANGES,
    Component: ConcurrentStreamsWidget,
  },
  {
    id: "egress-24h",
    title: "Egress",
    description: "Sampled egress in Mbps",
    minSpan: 4,
    maxSpan: 12,
    defaultSpan: 6,
    minRows: 2,
    maxRows: 5,
    defaultRows: 3,
    ranges: SAMPLED_RANGES,
    Component: EgressWidget,
  },
  {
    id: "playback-reliability",
    title: "Playback reliability",
    description: "Sessions started, transcode starts, completion rate, profiles",
    minSpan: 4,
    maxSpan: 8,
    defaultSpan: 6,
    minRows: 2,
    maxRows: 4,
    defaultRows: 2,
    ranges: SAMPLED_RANGES,
    Component: PlaybackReliabilityWidget,
  },
  {
    id: "top-titles",
    title: "Top titles",
    description: "Most-played titles over the chosen window",
    minSpan: 4,
    maxSpan: 8,
    defaultSpan: 6,
    minRows: 2,
    maxRows: 5,
    defaultRows: 3,
    ranges: LEADERBOARD_RANGES,
    Component: TopTitlesWidget,
  },
  {
    id: "top-profiles",
    title: "Most active profiles",
    description: "Profiles with the most plays over the chosen window",
    minSpan: 4,
    maxSpan: 8,
    defaultSpan: 6,
    minRows: 2,
    maxRows: 5,
    defaultRows: 3,
    ranges: LEADERBOARD_RANGES,
    Component: TopProfilesWidget,
  },
  {
    id: "trakt-sync",
    title: "Trakt sync",
    description: "Watch provider connection and 24h sync status",
    minSpan: 6,
    maxSpan: 12,
    defaultSpan: 9,
    minRows: 1,
    maxRows: 1,
    defaultRows: 1,
    Component: TraktSyncWidget,
  },
  {
    id: "now-playing",
    title: "Now playing",
    description: "Active streams with client, method, and progress",
    minSpan: 6,
    maxSpan: 12,
    defaultSpan: 12,
    minRows: 2,
    maxRows: 8,
    defaultRows: 4,
    Component: NowPlayingWidget,
  },
  {
    id: "transcode-nodes",
    title: "Transcode nodes",
    description: "Stream node health, job load, and egress",
    minSpan: 4,
    maxSpan: 12,
    defaultSpan: 6,
    minRows: 2,
    maxRows: 6,
    defaultRows: 3,
    Component: TranscodeNodesWidget,
  },
  {
    id: "scanner",
    title: "Scanner",
    description: "Live scan progress, queue depth, and autoscan state",
    minSpan: 4,
    maxSpan: 12,
    defaultSpan: 6,
    minRows: 2,
    maxRows: 6,
    defaultRows: 3,
    Component: ScannerWidget,
  },
  {
    id: "libraries",
    title: "Libraries",
    description: "Library list with scan controls and progress",
    minSpan: 6,
    maxSpan: 12,
    defaultSpan: 7,
    minRows: 2,
    maxRows: 8,
    defaultRows: 4,
    Component: LibrariesWidget,
  },
  {
    id: "users",
    title: "Users",
    description: "Recent user accounts with role and status",
    minSpan: 4,
    maxSpan: 8,
    defaultSpan: 5,
    minRows: 2,
    maxRows: 8,
    defaultRows: 4,
    Component: UsersWidget,
  },
  {
    id: "scan-activity",
    title: "Scan activity",
    description: "Recent scan runs with trigger, status, and duration",
    minSpan: 6,
    maxSpan: 12,
    defaultSpan: 12,
    minRows: 2,
    maxRows: 6,
    defaultRows: 3,
    Component: ScanActivityWidget,
  },
  {
    id: "recent-errors",
    title: "Recent errors",
    description: "Latest error and warning lines from the operational log",
    minSpan: 6,
    maxSpan: 12,
    defaultSpan: 12,
    minRows: 2,
    maxRows: 6,
    defaultRows: 4,
    Component: RecentErrorsWidget,
  },
  {
    id: "recent-activity",
    title: "Recent activity",
    description: "Feed of recently started playback sessions",
    minSpan: 6,
    maxSpan: 12,
    defaultSpan: 12,
    minRows: 2,
    maxRows: 8,
    defaultRows: 4,
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

/**
 * The boxes of the default arrangement: live numbers first, then the charts
 * that explain them, then the operational surfaces that are only interesting
 * when something is wrong. Admins who already customized their dashboard keep
 * their stored layout — this list is only the starting point, and every widget
 * stays available from the Add-widget sheet.
 *
 * Windows are not written here; DEFAULT_LAYOUT below stamps each ranged widget
 * with the default from its own definition, so the two cannot drift apart.
 */
const DEFAULT_LAYOUT_BOXES: DashboardLayoutEntry[] = [
  { id: "stat-active-streams", span: 3, rows: 1 },
  { id: "stat-egress-now", span: 3, rows: 1 },
  { id: "stat-transcode-share", span: 3, rows: 1 },
  { id: "stat-profiles-active", span: 3, rows: 1 },
  { id: "stat-movies", span: 3, rows: 1 },
  { id: "stat-shows", span: 3, rows: 1 },
  { id: "stat-users", span: 3, rows: 1 },
  { id: "stat-storage", span: 3, rows: 1 },
  { id: "health-strip", span: 12, rows: 1 },
  { id: "playback-24h", span: 6, rows: 3 },
  { id: "concurrent-streams-24h", span: 6, rows: 3 },
  { id: "egress-24h", span: 6, rows: 3 },
  { id: "playback-reliability", span: 6, rows: 2 },
  { id: "now-playing", span: 12, rows: 4 },
  { id: "transcode-nodes", span: 6, rows: 3 },
  { id: "scanner", span: 6, rows: 3 },
  { id: "libraries", span: 7, rows: 4 },
  { id: "users", span: 5, rows: 4 },
  { id: "top-titles", span: 6, rows: 3 },
  { id: "top-profiles", span: 6, rows: 3 },
  { id: "trakt-sync", span: 9, rows: 1 },
  { id: "scan-activity", span: 12, rows: 3 },
  { id: "recent-errors", span: 12, rows: 4 },
  { id: "recent-activity", span: 12, rows: 4 },
];

/** The default arrangement, with each ranged widget on its default window. */
export const DEFAULT_LAYOUT: DashboardLayoutEntry[] = DEFAULT_LAYOUT_BOXES.map((entry) => {
  const ranges = getDashboardWidget(entry.id).ranges;
  return ranges ? { ...entry, range: ranges.default } : entry;
});
