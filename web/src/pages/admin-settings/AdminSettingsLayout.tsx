import { useEffect, useRef, type ComponentType } from "react";
import { ChevronLeft, LayoutDashboard } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Link, useSearchParams } from "react-router";

import {
  ADMIN_SETTINGS_NAV,
  resolveAdminSettingsTabID,
  type AdminSettingsSearchItem,
} from "@/lib/adminSettingsSearch";
import { cn } from "@/lib/utils";
import { useAdminServerStatus } from "@/hooks/queries/admin/settings";
import {
  settingsTabHref,
  useSettingsOverview,
  type SectionStatus,
  type SettingsOverviewTabID,
} from "@/hooks/admin/useSettingsOverview";

import GeneralSettings from "./GeneralSettings";
import AppearanceSettings from "./AppearanceSettings";
import SecurityAccessSettings from "./SecurityAccessSettings";
import LibraryMetadataSettings from "./LibraryMetadataSettings";
import PlaybackSettings from "./PlaybackSettings";
import ProvidersSettings from "./ProvidersSettings";
import WatchSyncSettings from "./WatchSyncSettings";
import AISettings from "./AISettings";
import NotificationsAdminSettings from "./NotificationsAdminSettings";
import CompatibilityProxiesSettings from "./CompatibilityProxiesSettings";
import InfrastructureSettings from "./InfrastructureSettings";
import SettingsOverview from "./SettingsOverview";
import { RestartBanner } from "./SaveBar";
import "@/styles/admin-settings.css";

interface SettingsNav extends AdminSettingsSearchItem {
  component: ComponentType;
}

const SETTINGS_COMPONENTS: Record<string, ComponentType> = {
  general: GeneralSettings,
  appearance: AppearanceSettings,
  security: SecurityAccessSettings,
  library: LibraryMetadataSettings,
  playback: PlaybackSettings,
  providers: ProvidersSettings,
  "watch-sync": WatchSyncSettings,
  ai: AISettings,
  notifications: NotificationsAdminSettings,
  compatibility: CompatibilityProxiesSettings,
  infrastructure: InfrastructureSettings,
};

function settingsComponent(id: string) {
  const component = SETTINGS_COMPONENTS[id];
  if (!component) {
    throw new Error(`Missing admin settings component for ${id}`);
  }
  return component;
}

const SETTINGS_NAV: SettingsNav[] = ADMIN_SETTINGS_NAV.map((item) => ({
  ...item,
  component: settingsComponent(item.id),
}));

interface RailItemProps {
  label: string;
  icon: LucideIcon;
  href: string;
  active: boolean;
  status?: SectionStatus;
}

/**
 * One row of the settings rail: icon, label, and — only when a section needs
 * the admin — an amber dot on the right. The active row is an accent bar and
 * nothing else, so the rail stays quiet next to the page it frames.
 */
function SettingsRailItem({ label, icon: Icon, href, active, status }: RailItemProps) {
  const needsAttention = status === "warn";

  return (
    <li>
      <Link
        to={href}
        aria-current={active ? "page" : undefined}
        className={cn(
          "relative flex w-full items-center gap-2.5 rounded-lg py-[7px] pr-2.5 pl-3 text-[13.5px] transition-colors duration-150",
          active
            ? "text-foreground font-medium"
            : "text-muted-foreground hover:text-foreground hover:bg-foreground/[0.035]",
        )}
      >
        {active ? (
          <span
            aria-hidden="true"
            className="absolute top-[7px] bottom-[7px] -left-[10px] w-[2px] rounded-r-sm"
            style={{ background: "var(--settings-accent)" }}
          />
        ) : null}
        <Icon
          aria-hidden="true"
          className={cn("h-4 w-4 flex-none", active && "text-[var(--settings-accent)]")}
        />
        <span className="min-w-0 flex-1 truncate">{label}</span>
        {needsAttention ? (
          <>
            <span aria-hidden="true" className="h-1.5 w-1.5 flex-none rounded-full bg-amber-500" />
            <span className="sr-only">Needs attention</span>
          </>
        ) : null}
      </Link>
    </li>
  );
}

export default function AdminSettingsLayout() {
  const [searchParams, setSearchParams] = useSearchParams();
  const activeContentRef = useRef<HTMLDivElement>(null);
  const { data: serverStatus } = useAdminServerStatus();
  const { sectionStatus } = useSettingsOverview();
  const rawActiveId = searchParams.get("tab");
  const activeId = resolveAdminSettingsTabID(rawActiveId);

  const active = activeId ? SETTINGS_NAV.find((item) => item.id === activeId) : undefined;
  const ActiveComponent = active?.component;

  // Rewrite a legacy `?tab=` id to the tab that absorbed it so the address bar,
  // and anything the admin copies out of it, names a tab that still exists.
  useEffect(() => {
    if (!activeId || activeId === rawActiveId) return;
    setSearchParams({ tab: activeId }, { replace: true });
  }, [activeId, rawActiveId, setSearchParams]);

  useEffect(() => {
    if (!active) return;

    window.scrollTo(0, 0);
    if (activeContentRef.current) {
      activeContentRef.current.scrollTop = 0;
    }
    activeContentRef.current?.focus({ preventScroll: true });
  }, [active]);

  return (
    <div className="w-full max-w-[96rem]">
      {/* One restart prompt for every settings tab, so a tab never adds its own. */}
      <RestartBanner restartRequired={serverStatus?.restart_required} />

      {active && ActiveComponent ? (
        <div className="space-y-5">
          <h1 className="sr-only">Settings</h1>
          <Link
            to="/admin/settings"
            className="text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex w-fit items-center gap-1.5 rounded-lg pr-2 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:outline-none lg:hidden"
          >
            <ChevronLeft className="h-4 w-4" aria-hidden="true" />
            All settings
          </Link>

          <div className="surface-panel flex min-h-[500px] flex-col overflow-hidden rounded-[1.8rem] border-0 lg:flex-row">
            <nav
              aria-label="Admin settings sections"
              className="border-border hidden border-r lg:block lg:w-[15.5rem] lg:flex-shrink-0"
            >
              <div className="px-4 py-5">
                <ul className="list-none space-y-0.5">
                  <SettingsRailItem
                    label="Overview"
                    icon={LayoutDashboard}
                    href="/admin/settings"
                    active={!activeId}
                  />
                  {SETTINGS_NAV.map((item) => (
                    <SettingsRailItem
                      key={item.id}
                      label={item.label}
                      icon={item.icon}
                      href={settingsTabHref(item.id)}
                      active={item.id === active.id}
                      status={sectionStatus[item.id as SettingsOverviewTabID] ?? "ok"}
                    />
                  ))}
                </ul>
              </div>
            </nav>

            <div
              ref={activeContentRef}
              role="region"
              aria-label={`${active.label} settings`}
              tabIndex={-1}
              className="min-w-0 flex-1 overflow-y-auto p-4 focus:outline-none sm:p-6 lg:p-8"
            >
              <ActiveComponent />
            </div>
          </div>
        </div>
      ) : (
        <SettingsOverview />
      )}
    </div>
  );
}
