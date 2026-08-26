import { useEffect, useRef, type ComponentType } from "react";
import { ChevronLeft } from "lucide-react";
import { Link, Navigate, useParams, useSearchParams } from "react-router";

import {
  ADMIN_SETTINGS_NAV,
  resolveAdminSettingsPageID,
  type AdminSettingsSearchItem,
} from "@/lib/adminSettingsSearch";
import { useAdminServerStatus } from "@/hooks/queries/admin/settings";
import { settingsPageHref } from "@/hooks/admin/useSettingsOverview";

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

export default function AdminSettingsLayout() {
  const params = useParams();
  const [searchParams] = useSearchParams();
  const activeContentRef = useRef<HTMLDivElement>(null);
  const { data: serverStatus } = useAdminServerStatus();
  const rawPageId = params["*"]?.replace(/^\/+|\/+$/g, "") || null;
  const legacyTabId = searchParams.get("tab");
  const requestedId = rawPageId ?? legacyTabId;
  const activeId = resolveAdminSettingsPageID(requestedId);

  const active = activeId ? SETTINGS_NAV.find((item) => item.id === activeId) : undefined;
  const ActiveComponent = active?.component;

  useEffect(() => {
    if (!activeId) return;

    window.scrollTo(0, 0);
    if (activeContentRef.current) {
      activeContentRef.current.scrollTop = 0;
    }
    activeContentRef.current?.focus({ preventScroll: true });
  }, [activeId]);

  // Turn old query-string tabs and retired page ids into canonical page URLs.
  if (requestedId && !activeId) {
    return <Navigate to="/admin/settings" replace />;
  }
  if (
    activeId &&
    (rawPageId !== activeId || legacyTabId !== null || searchParams.toString() !== "")
  ) {
    return <Navigate to={settingsPageHref(activeId)} replace />;
  }

  return (
    <div className="w-full max-w-[96rem]">
      {/* One restart prompt for every settings page, so a page never adds its own. */}
      <RestartBanner restartRequired={serverStatus?.restart_required} />

      {active && ActiveComponent ? (
        <div className="mx-auto w-full max-w-5xl space-y-7">
          <Link
            to="/admin/settings"
            className="text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex w-fit items-center gap-1.5 rounded-lg pr-2 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:outline-none"
          >
            <ChevronLeft className="h-4 w-4" aria-hidden="true" />
            All settings
          </Link>

          <div
            ref={activeContentRef}
            role="region"
            aria-label={`${active.label} settings`}
            tabIndex={-1}
            className="min-w-0 focus:outline-none"
          >
            <ActiveComponent />
          </div>
        </div>
      ) : (
        <SettingsOverview />
      )}
    </div>
  );
}
