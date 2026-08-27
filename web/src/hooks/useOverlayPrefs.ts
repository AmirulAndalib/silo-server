import { useMemo, useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/client";
import {
  effectiveSettingsQueryKey,
  isDefinitiveSettingMutationRejection,
  useEffectiveSettings,
  useSetSettingValue,
  type EffectiveSettingsMap,
} from "@/hooks/queries/settingValues";
import type { SettingIdentity } from "@/hooks/queries/settingValues";
import { SETTING_KEYS, type SettingKey } from "@/lib/settingsContract";
import { settingsKeys } from "@/hooks/queries/keys";
import { storage } from "@/utils/storage";
import { parseOverlayPrefs, serializeOverlayPrefs, type CardOverlayPrefs } from "@/lib/overlays";
import {
  normalizeCardQuickActionMode,
  type EnabledCardQuickActionMode,
} from "@/lib/cardQuickActions";

/** Card overlay preferences are profile-wide in the contract (no device scope). */
const PROFILE_SCOPE: SettingIdentity = { scope: "profile" };

const OVERLAY_KEYS = [
  SETTING_KEYS.UI_CARD_OVERLAYS,
  SETTING_KEYS.UI_CARD_QUICK_ACTIONS,
  SETTING_KEYS.UI_CARD_QUICK_ACTIONS_ENABLED,
] as const;

interface OverlayConfig {
  enabled: boolean;
  defaults?: string;
  quick_actions_enabled?: boolean;
  quick_actions_default?: string;
}

// The overlay-badge kill switch and server-wide defaults live in
// server_settings, not the user-settings contract, so this endpoint stays
// alongside the canonical values API.
function useOverlayConfig() {
  return useQuery({
    queryKey: settingsKeys.overlayConfig(),
    queryFn: () => api<OverlayConfig>("/settings/overlay-config"),
    staleTime: 60_000,
  });
}

export function useOverlayPrefs() {
  // The effective endpoint requires a profile header; without one the user
  // has no stored preference and the admin defaults apply on their own.
  const profileId = storage.get(storage.KEYS.PROFILE_ID);
  const hasProfile = Boolean(profileId);
  const { data: effective, isLoading: userLoading } = useEffectiveSettings({
    keys: OVERLAY_KEYS,
    enabled: hasProfile,
  });
  const { data: config, isLoading: configLoading } = useOverlayConfig();
  const { mutate: setSettingValue } = useSetSettingValue();
  const queryClient = useQueryClient();
  const effectiveQueryKey = useMemo(
    () => effectiveSettingsQueryKey({ keys: OVERLAY_KEYS, profileId: profileId ?? undefined }),
    // The active profile is part of effectiveSettingsQueryKey. Recompute the
    // target when profile selection changes rather than writing into the
    // previous profile's cache entry.
    [profileId],
  );

  const setProfileValue = useCallback(
    (key: SettingKey, value: unknown) => {
      queryClient.setQueryData<EffectiveSettingsMap>(effectiveQueryKey, (current) => ({
        ...current,
        [key]: { key, value, source: "profile", scope: "profile" },
      }));
      setSettingValue(
        { key, value, identity: PROFILE_SCOPE },
        {
          // The shared mutation invalidates on success and ambiguous errors.
          // A definitive rejection never reached storage, so refetch instead of
          // restoring a snapshot: rapid successive writes make any snapshot an
          // unpersisted optimistic value, while a refetch reconciles the cache
          // with what the server actually stored.
          onError: (error) => {
            if (isDefinitiveSettingMutationRejection(error)) {
              void queryClient.invalidateQueries({ queryKey: effectiveQueryKey });
            }
          },
        },
      );
    },
    [effectiveQueryKey, queryClient, setSettingValue],
  );

  // The contract default is null — "no preference expressed" — which is what
  // lets the server-wide admin default apply; a stored value wins outright.
  const userValue = effective?.[SETTING_KEYS.UI_CARD_OVERLAYS]?.value ?? null;
  const quickActionUserValue = effective?.[SETTING_KEYS.UI_CARD_QUICK_ACTIONS]?.value ?? null;
  const quickActionsEnabledUserValue =
    effective?.[SETTING_KEYS.UI_CARD_QUICK_ACTIONS_ENABLED]?.value;

  const prefs = useMemo(() => {
    // User setting takes priority; fall back to admin defaults
    const source = userValue ?? config?.defaults ?? null;
    return parseOverlayPrefs(source);
  }, [userValue, config?.defaults]);

  // Admin kill switch: if disabled server-wide, return null prefs
  const enabled = config?.enabled !== false;
  // Quick actions are inherit-with-override, not a policy gate: the server
  // setting is only the default for profiles that have not chosen, and an
  // explicit profile choice wins in either direction. Absent server config
  // (including while it loads) means off.
  const quickActionsDefaultEnabled = config?.quick_actions_enabled === true;
  const quickActionsEnabled =
    typeof quickActionsEnabledUserValue === "boolean"
      ? quickActionsEnabledUserValue
      : quickActionsDefaultEnabled;
  const configuredQuickActionMode = normalizeCardQuickActionMode(
    quickActionUserValue ?? config?.quick_actions_default,
  );

  const setPrefs = useCallback(
    (next: CardOverlayPrefs) => {
      // Avoid a network round-trip and downstream re-render cascade when
      // the user toggles a control to its current value. Comparison goes
      // through the parser so key ordering in the stored JSON is irrelevant.
      if (
        userValue != null &&
        serializeOverlayPrefs(parseOverlayPrefs(userValue)) === serializeOverlayPrefs(next)
      ) {
        return;
      }
      setProfileValue(SETTING_KEYS.UI_CARD_OVERLAYS, next);
    },
    [userValue, setProfileValue],
  );

  const setQuickActionMode = useCallback(
    (next: EnabledCardQuickActionMode) => {
      // Compare against the mode the control displays, not a differently
      // normalized reading of the stored value: an unrecognized stored value
      // displays the admin default, which must stay selectable.
      if (quickActionUserValue != null && configuredQuickActionMode === next) return;
      setProfileValue(SETTING_KEYS.UI_CARD_QUICK_ACTIONS, next);
    },
    [configuredQuickActionMode, quickActionUserValue, setProfileValue],
  );

  const setQuickActionsEnabled = useCallback(
    (next: boolean) => {
      if (
        typeof quickActionsEnabledUserValue === "boolean" &&
        quickActionsEnabledUserValue === next
      ) {
        return;
      }
      setProfileValue(SETTING_KEYS.UI_CARD_QUICK_ACTIONS_ENABLED, next);
    },
    [quickActionsEnabledUserValue, setProfileValue],
  );

  // While either query is in flight, report null prefs instead of built-in
  // defaults: rendering defaults first would flash badges that vanish (or
  // change) the moment the user's own config or the admin kill switch loads.
  const isLoading = (hasProfile && userLoading) || configLoading;

  return {
    prefs: enabled && !isLoading ? prefs : null,
    setPrefs,
    quickActionMode:
      quickActionsEnabled && !isLoading ? configuredQuickActionMode : ("none" as const),
    quickActionPreference: configuredQuickActionMode,
    setQuickActionMode,
    quickActionsEnabled,
    setQuickActionsEnabled,
    isLoading,
    enabled,
  };
}
