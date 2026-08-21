import { useMemo, useState } from "react";
import { AlertTriangle, Check, RotateCcw } from "lucide-react";

import { BrandingAssetField } from "@/components/admin/BrandingAssetField";
import { OverlayPreviewCard } from "@/components/overlays/OverlayPreviewCard";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { SettingsPageHeader } from "@/components/settings/SettingsPageHeader";
import { StatusStrip, type StatusStripItem } from "@/components/settings/StatusStrip";
import { RawCssEditor } from "@/components/theme/RawCssEditor";
import { ThemePreviewCard } from "@/components/theme/ThemePreviewCard";
import { TokenEditor } from "@/components/theme/TokenEditor";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { useBranding } from "@/hooks/useBranding";
import { useRestartKeys } from "@/hooks/useRestartKeys";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { ACCENT_TOKENS, accentColorToTokens } from "@/lib/accentMapping";
import { sanitizeCss } from "@/lib/cssSanitizer";
import {
  buildDefaultPrefs,
  CATEGORY_META,
  OVERLAY_CATEGORIES,
  OVERLAY_PRESETS,
  OVERLAY_REGISTRY,
  POSITION_OPTIONS,
  PRESET_IDS,
  parseOverlayPrefs,
  serializeOverlayPrefs,
  type CardOverlayPrefs,
  type OverlayId,
  type OverlayPosition,
  type PresetId,
} from "@/lib/overlays";
import { parseVarsJson } from "@/lib/themeExport";
import type { ThemeVarOverrides } from "@/hooks/useCustomTheme";
import type { ThemeToken } from "@/lib/themeTokens";
import { THEME_IDS, THEMES, type ThemeId } from "@/lib/themes";
import { cn } from "@/lib/utils";
import { FieldGroup } from "./FieldGroup";
import { SaveBar } from "./SaveBar";
import { SettingField, SettingFieldRow } from "./SettingField";

const IMAGE_ACCEPT = "image/png,image/jpeg,image/webp";
const FAVICON_ACCEPT =
  "image/png,image/x-icon,image/vnd.microsoft.icon,image/svg+xml,image/webp,.ico";

const ACCENT_PRESETS = [
  "#4f46e5",
  "#0ea5e9",
  "#10b981",
  "#f59e0b",
  "#ef4444",
  "#ec4899",
  "#8b5cf6",
  "#64748b",
];

const ACCENT_KEY = "branding.accent_color";
const DEFAULT_THEME_KEY = "branding.default_theme";
const THEME_VARS_KEY = "ui.admin_theme_vars";
const CUSTOM_CSS_KEY = "ui.admin_custom_css";
const CATALOG_URL_KEY = "theme.catalog_url";
const OVERLAYS_ENABLED_KEY = "overlays.enabled";
const OVERLAY_DEFAULTS_KEY = "defaults.card_overlays";

/**
 * Every appearance key the tab stages, saved as one batch by the shared
 * SaveBar. Theming used to autosave each keystroke through
 * `useUpdateServerSetting`; it now shares this form so the whole tab has one
 * save model. Asset uploads keep their own upload/delete mutations because a
 * file picker has no draft to batch.
 *
 * Server name and login subtitle deliberately live on the General tab: they are
 * server identity, not look and feel.
 */
const KEYS = [
  ACCENT_KEY,
  DEFAULT_THEME_KEY,
  THEME_VARS_KEY,
  CUSTOM_CSS_KEY,
  CATALOG_URL_KEY,
  OVERLAYS_ENABLED_KEY,
  OVERLAY_DEFAULTS_KEY,
];

export default function AppearanceSettings() {
  const form = useSettingsForm({ keys: useMemo(() => KEYS, []) });
  const branding = useBranding();
  const restartKeys = useRestartKeys();

  // The CSS box shows exactly what was typed while the staged value is the
  // sanitized copy that will be saved, so stripping an external @import never
  // rewrites the text under the cursor.
  const [cssDraft, setCssDraft] = useState<string | null>(null);
  // The accent picker and the style preset write the same keys as the advanced
  // controls, so plain dirty state would pop the disclosure open on an
  // essential edit. Track use of the advanced controls themselves instead.
  const [tokensTouched, setTokensTouched] = useState(false);
  const [overlayItemsTouched, setOverlayItemsTouched] = useState(false);

  const accentColor = form.getValue(ACCENT_KEY);
  const defaultTheme = form.getValue(DEFAULT_THEME_KEY);
  const vars = parseVarsJson(form.getValue(THEME_VARS_KEY));
  const savedCss = form.getValue(CUSTOM_CSS_KEY);
  const rawCss = cssDraft ?? savedCss;
  const hasThemeOverrides = Object.keys(vars).length > 0 || savedCss.length > 0;

  // s3.public_bucket is not staged here, but getValue falls back to the full
  // settings response so the uploads can still be gated on it.
  const s3Configured = Boolean(form.getValue("s3.public_bucket"));
  const assetStorageAvailable = branding.storageAvailable;

  const overlaysEnabled = form.getValue(OVERLAYS_ENABLED_KEY) !== "false";
  const overlayPrefs = parseOverlayPrefs(
    form.getValue(OVERLAY_DEFAULTS_KEY) || serializeOverlayPrefs(buildDefaultPrefs()),
  );

  const setVars = (next: ThemeVarOverrides) => form.setValue(THEME_VARS_KEY, JSON.stringify(next));

  // Accent recolors the primary action color, focus ring, and sidebar accent
  // (ACCENT_TOKENS). It merges into the staged token overrides so a pick never
  // clobbers the advanced editor and repeated picks compound correctly.
  const applyAccent = (hex: string) => {
    setVars({ ...vars, ...accentColorToTokens(hex) });
    form.setValue(ACCENT_KEY, hex);
  };

  const clearAccent = () => {
    const next = { ...vars };
    for (const token of ACCENT_TOKENS) {
      delete next[token];
    }
    setVars(next);
    form.setValue(ACCENT_KEY, "");
  };

  const setToken = (token: ThemeToken, value: string) => {
    setTokensTouched(true);
    setVars({ ...vars, [token]: value });
  };

  const resetToken = (token: ThemeToken) => {
    setTokensTouched(true);
    const next = { ...vars };
    delete next[token];
    setVars(next);
  };

  const handleCssChange = (css: string) => {
    setTokensTouched(true);
    setCssDraft(css);
    form.setValue(CUSTOM_CSS_KEY, sanitizeCss(css));
  };

  const resetAllThemeOverrides = () => {
    setTokensTouched(true);
    setCssDraft("");
    setVars({});
    form.setValue(CUSTOM_CSS_KEY, "");
    form.setValue(ACCENT_KEY, "");
  };

  const setOverlayPrefs = (next: CardOverlayPrefs) =>
    form.setValue(OVERLAY_DEFAULTS_KEY, serializeOverlayPrefs(next));

  const updateOverlayItem = (
    id: OverlayId,
    patch: Partial<CardOverlayPrefs["items"][OverlayId]>,
  ) => {
    setOverlayItemsTouched(true);
    setOverlayPrefs({
      ...overlayPrefs,
      items: { ...overlayPrefs.items, [id]: { ...overlayPrefs.items[id], ...patch } },
    });
  };

  // Discarding has to drop the untouched CSS draft too, otherwise the box would
  // keep showing text that is no longer staged.
  const discard = () => {
    setCssDraft(null);
    form.discard();
  };

  const themeAdvancedDirty =
    tokensTouched || form.isDirty(CUSTOM_CSS_KEY) || form.isDirty(CATALOG_URL_KEY);
  // `THEME_VARS_KEY` is also written by the essential accent picker, so it only
  // counts once the advanced token editor itself was used — otherwise picking an
  // accent would report a change inside a section the admin never opened.
  const themeAdvancedChanged =
    [CUSTOM_CSS_KEY, CATALOG_URL_KEY].filter((key) => form.isDirty(key)).length +
    (tokensTouched && form.isDirty(THEME_VARS_KEY) ? 1 : 0);
  const restartCount = KEYS.filter((key) => form.isDirty(key) && restartKeys.has(key)).length;

  const themeName = (THEME_IDS as readonly string[]).includes(defaultTheme)
    ? THEMES[defaultTheme as ThemeId].label
    : null;

  const stripItems: StatusStripItem[] = [
    themeName
      ? { tone: "info", label: `Default theme: ${themeName}` }
      : { tone: "muted", label: "No default theme" },
    accentColor
      ? { tone: "info", label: `Accent ${accentColor}` }
      : { tone: "muted", label: "Default accent" },
    overlaysEnabled
      ? { tone: "ok", label: "Poster badges on" }
      : { tone: "muted", label: "Poster badges off" },
    assetStorageAvailable
      ? { tone: "ok", label: "Image uploads ready" }
      : s3Configured
        ? { tone: "warn", label: "Image uploads need a restart" }
        : { tone: "warn", label: "Image uploads need S3" },
  ];

  if (form.isLoading) return <div>Loading...</div>;

  return (
    <div className="flex h-full flex-col">
      <SettingsPageHeader
        title="Appearance"
        description="Branding and theme defaults for everyone who signs in."
        strip={<StatusStrip items={stripItems} />}
        className="mb-8"
      />

      <div className="flex-1 space-y-9">
        <FieldGroup
          label="Logos and icons"
          clarifier="Custom images replace the Silo logo, the browser tab icon, and the login background. Anything you leave empty keeps the Silo default."
        >
          {!assetStorageAvailable && (
            <div className="mt-3 flex items-start gap-3 rounded-xl border border-amber-500/20 bg-amber-500/5 p-3">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
              <p className="text-muted-foreground text-[13px] leading-relaxed">
                {s3Configured ? (
                  <>
                    The public bucket is saved, but object storage is not active in this process
                    yet. Restart the server to enable image uploads.
                  </>
                ) : (
                  <>
                    Image uploads need S3 object storage. Configure a public bucket in{" "}
                    <span className="text-foreground font-medium">Infrastructure</span> settings,
                    then restart the server.
                  </>
                )}
              </p>
            </div>
          )}

          <div className="space-y-2 py-3.5">
            <BrandingAssetField
              label="Logo (wordmark)"
              description="Wide logo shown in the expanded sidebar."
              kind="wordmark"
              currentUrl={branding.wordmarkUrl}
              accept={IMAGE_ACCEPT}
              enabled={assetStorageAvailable}
              preview="wide"
            />
            <BrandingAssetField
              label="Logo (icon)"
              description="Square mark shown in the collapsed sidebar and the installed app."
              kind="mark"
              currentUrl={branding.markUrl}
              accept={IMAGE_ACCEPT}
              enabled={assetStorageAvailable}
              preview="square"
            />
            <BrandingAssetField
              label="Favicon"
              description="Browser tab icon. PNG, ICO, or SVG."
              kind="favicon"
              currentUrl={branding.faviconUrl}
              accept={FAVICON_ACCEPT}
              enabled={assetStorageAvailable}
              preview="square"
            />
            <BrandingAssetField
              label="Login background"
              description="Full-bleed background image for the login and signup pages."
              kind="login_bg"
              currentUrl={branding.loginBgUrl}
              accept={IMAGE_ACCEPT}
              enabled={assetStorageAvailable}
              preview="wide"
            />
          </div>
        </FieldGroup>

        <FieldGroup
          label="Colors and theme"
          clarifier="What people see until they choose something else for themselves"
        >
          <SettingFieldRow
            label="Accent color"
            description="Recolors buttons, focus outlines, and the sidebar highlight for everyone. Also used as the installed app's color."
            dirty={form.isDirty(ACCENT_KEY)}
          >
            <div className="flex flex-wrap items-center justify-end gap-2 sm:max-w-[300px]">
              {ACCENT_PRESETS.map((hex) => (
                <button
                  key={hex}
                  type="button"
                  onClick={() => applyAccent(hex)}
                  aria-label={`Use accent ${hex}`}
                  className={cn(
                    "relative h-8 w-8 rounded-full border transition-transform hover:scale-110",
                    accentColor.toLowerCase() === hex.toLowerCase()
                      ? "border-foreground"
                      : "border-border",
                  )}
                  style={{ backgroundColor: hex }}
                >
                  {accentColor.toLowerCase() === hex.toLowerCase() && (
                    <Check className="absolute inset-0 m-auto h-4 w-4 text-white drop-shadow" />
                  )}
                </button>
              ))}
              <label className="border-border inline-flex h-8 cursor-pointer items-center gap-2 rounded-lg border px-2.5 text-xs font-medium">
                <input
                  type="color"
                  aria-label="Custom accent color"
                  value={accentColor || "#4f46e5"}
                  onChange={(e) => applyAccent(e.target.value)}
                  className="h-5 w-5 cursor-pointer border-0 bg-transparent p-0"
                />
                Custom
              </label>
              {accentColor && (
                <button
                  type="button"
                  onClick={clearAccent}
                  className="text-muted-foreground hover:text-destructive inline-flex items-center gap-1.5 text-xs font-medium transition-colors"
                >
                  <RotateCcw className="h-3 w-3" />
                  Reset
                </button>
              )}
            </div>
          </SettingFieldRow>

          <SettingFieldRow
            label="Default theme"
            description="The theme people see until they choose their own. Everyone can still pick a different one for themselves."
            dirty={form.isDirty(DEFAULT_THEME_KEY)}
          >
            <div className="flex flex-wrap justify-end gap-2 sm:max-w-[320px]">
              <button
                type="button"
                onClick={() => form.setValue(DEFAULT_THEME_KEY, "")}
                className={cn(
                  "rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors",
                  defaultTheme === ""
                    ? "border-foreground bg-muted/50"
                    : "border-border hover:bg-muted/30",
                )}
              >
                No default
              </button>
              {THEME_IDS.map((id) => (
                <button
                  key={id}
                  type="button"
                  onClick={() => form.setValue(DEFAULT_THEME_KEY, id)}
                  className={cn(
                    "inline-flex items-center gap-2 rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors",
                    defaultTheme === id
                      ? "border-foreground bg-muted/50"
                      : "border-border hover:bg-muted/30",
                  )}
                >
                  <span
                    className="h-3.5 w-3.5 rounded-full border border-black/10"
                    style={{ backgroundColor: THEMES[id].previewBg }}
                  >
                    <span
                      className="block h-full w-full scale-50 rounded-full"
                      style={{ backgroundColor: THEMES[id].previewAccent }}
                    />
                  </span>
                  {THEMES[id].label}
                </button>
              ))}
            </div>
          </SettingFieldRow>

          <AdvancedSection
            id="appearance.theme"
            count={3}
            changedCount={themeAdvancedChanged}
            forceOpen={themeAdvancedDirty}
          >
            <div className="space-y-3 py-3.5">
              <div className="space-y-1">
                <Label className="text-sm font-medium">Individual colors and fonts</Label>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  Change single colors, fonts, and corner rounding on top of the chosen theme.
                  Everyone sees these changes; each person can still customize further.
                </p>
              </div>
              <ThemePreviewCard vars={vars} />
              {hasThemeOverrides && (
                <div className="flex justify-end">
                  <button
                    type="button"
                    onClick={resetAllThemeOverrides}
                    className="text-muted-foreground hover:text-destructive inline-flex items-center gap-1.5 text-xs font-medium transition-colors"
                  >
                    <RotateCcw className="h-3 w-3" />
                    Reset all
                  </button>
                </div>
              )}
              <TokenEditor vars={vars} onSetVar={setToken} onResetVar={resetToken} />
            </div>

            <div className="space-y-2 py-3.5">
              <div className="space-y-1">
                <Label className="text-sm font-medium">Custom CSS</Label>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  Extra CSS applied to every page after the theme, for changes the controls above do
                  not cover. It is saved with the rest of this tab.
                </p>
              </div>
              <RawCssEditor value={rawCss} onChange={handleCssChange} />
            </div>

            <SettingField
              label="Community theme list"
              type="text"
              hint="https://example.com/themes.json"
              description="Address of the JSON file listing community themes people can browse in their own settings."
              value={form.getValue(CATALOG_URL_KEY)}
              onChange={(v) => form.setValue(CATALOG_URL_KEY, v)}
              restartRequired={restartKeys.has(CATALOG_URL_KEY)}
              dirty={form.isDirty(CATALOG_URL_KEY)}
            />
          </AdvancedSection>
        </FieldGroup>

        <FieldGroup label="Card overlays" clarifier="Small badges drawn on poster art">
          <SettingField
            label="Show badges on poster art"
            description="Badges such as resolution or rating. Turning this off hides them for everyone, whatever each person has chosen."
            type="toggle"
            value={form.getValue(OVERLAYS_ENABLED_KEY) || "true"}
            onChange={(v) => form.setValue(OVERLAYS_ENABLED_KEY, v)}
            restartRequired={restartKeys.has(OVERLAYS_ENABLED_KEY)}
            dirty={form.isDirty(OVERLAYS_ENABLED_KEY)}
          />

          <SettingFieldRow
            label="Badge style"
            description="How the badges look by default. Applies to anyone who has not changed their own overlay settings."
            dirty={form.isDirty(OVERLAY_DEFAULTS_KEY)}
            className={overlaysEnabled ? undefined : "pointer-events-none opacity-50"}
          >
            <div className="flex flex-col items-end gap-3">
              <Select
                value={overlayPrefs.preset}
                onValueChange={(v) => setOverlayPrefs({ ...overlayPrefs, preset: v as PresetId })}
              >
                <SelectTrigger className="w-[200px]" aria-label="Badge style">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PRESET_IDS.map((id) => (
                    <SelectItem key={id} value={id}>
                      {OVERLAY_PRESETS[id].label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <OverlayPreviewCard prefs={overlayPrefs} size="sm" variant="movie" />
            </div>
          </SettingFieldRow>

          <div className={cn(overlaysEnabled ? "" : "pointer-events-none opacity-50")}>
            <AdvancedSection
              id="appearance.overlays"
              title="Advanced · which badges and where"
              count={OVERLAY_REGISTRY.length}
              forceOpen={overlayItemsTouched}
            >
              {OVERLAY_CATEGORIES.map((category) => {
                const overlays = OVERLAY_REGISTRY.filter((d) => d.category === category);
                if (overlays.length === 0) return null;
                return (
                  <div key={category} className="min-w-0">
                    <p className="text-muted-foreground pt-3.5 pb-1 text-xs font-medium">
                      {CATEGORY_META[category].title}
                    </p>
                    <div className="[&>*]:border-b [&>*]:border-[color-mix(in_srgb,var(--border)_60%,transparent)] [&>*:last-child]:border-b-0">
                      {overlays.map((def) => {
                        const config = overlayPrefs.items[def.id];
                        return (
                          <SettingFieldRow
                            key={def.id}
                            label={def.label}
                            description={def.description}
                          >
                            <Select
                              value={config.position}
                              disabled={!config.enabled}
                              onValueChange={(pos) =>
                                updateOverlayItem(def.id, { position: pos as OverlayPosition })
                              }
                            >
                              <SelectTrigger
                                className="w-[130px]"
                                aria-label={`${def.label} corner`}
                              >
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {POSITION_OPTIONS.map((opt) => (
                                  <SelectItem key={opt.value} value={opt.value}>
                                    {opt.label}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                            <Switch
                              checked={config.enabled}
                              aria-label={`Show ${def.label}`}
                              onCheckedChange={(checked) =>
                                updateOverlayItem(def.id, { enabled: checked })
                              }
                            />
                          </SettingFieldRow>
                        );
                      })}
                    </div>
                  </div>
                );
              })}
            </AdvancedSection>
          </div>
        </FieldGroup>
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount}
        onSave={form.save}
        onDiscard={discard}
        isSaving={form.isSaving}
        restartRequired={form.restartRequired}
        restartCount={restartCount}
      />
    </div>
  );
}
