// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const useSettingsFormMock = vi.fn();

vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: (...args: unknown[]) => useSettingsFormMock(...args),
}));

vi.mock("@/hooks/useRestartKeys", () => ({
  useRestartKeys: () => new Set<string>(),
}));

vi.mock("@/hooks/useBranding", () => ({
  useBranding: () => ({
    storageAvailable: true,
    wordmarkUrl: null,
    markUrl: null,
    faviconUrl: null,
    loginBgUrl: null,
  }),
}));

vi.mock("@/components/admin/BrandingAssetField", () => ({
  BrandingAssetField: ({ label }: { label: string }) => <div>{label}</div>,
}));

vi.mock("@/components/theme/TokenEditor", () => ({
  TokenEditor: ({ onSetVar }: { onSetVar: (token: "primary", value: string) => void }) => (
    <button type="button" onClick={() => onSetVar("primary", "#112233")}>
      Set primary token
    </button>
  ),
}));

vi.mock("@/components/theme/RawCssEditor", () => ({
  RawCssEditor: ({ value, onChange }: { value: string; onChange: (css: string) => void }) => (
    <textarea
      aria-label="Custom CSS editor"
      value={value}
      onChange={(event) => onChange(event.target.value)}
    />
  ),
}));

vi.mock("@/components/theme/ThemePreviewCard", () => ({
  ThemePreviewCard: () => null,
}));

vi.mock("@/components/overlays/OverlayPreviewCard", () => ({
  OverlayPreviewCard: () => null,
}));

import AppearanceSettings from "./AppearanceSettings";

function makeForm(values: Record<string, string> = {}) {
  const staged: Record<string, string> = { ...values };
  return {
    isLoading: false,
    getValue: (key: string) => staged[key] ?? "",
    setValue: vi.fn((key: string, value: string) => {
      staged[key] = value;
    }),
    isDirty: () => false,
    dirtyCount: 0,
    save: vi.fn(),
    discard: vi.fn(),
    isSaving: false,
    restartRequired: false,
  };
}

let form: ReturnType<typeof makeForm>;

describe("AppearanceSettings", () => {
  beforeEach(() => {
    localStorage.clear();
    form = makeForm();
    useSettingsFormMock.mockReset();
    useSettingsFormMock.mockImplementation(() => form);
  });

  it("renders every field group heading", () => {
    render(<AppearanceSettings />);

    for (const heading of ["Logos & Icons", "Colors & Theme", "Card Overlays"]) {
      expect(screen.getByRole("group", { name: heading })).toBeInTheDocument();
    }
  });

  it("stages the union of appearance keys and leaves identity to General", () => {
    render(<AppearanceSettings />);

    const keys = useSettingsFormMock.mock.calls[0]?.[0]?.keys as string[];
    expect(keys).toEqual(
      expect.arrayContaining([
        "branding.accent_color",
        "branding.default_theme",
        "ui.admin_theme_vars",
        "ui.admin_custom_css",
        "theme.catalog_url",
        "overlays.enabled",
        "defaults.card_overlays",
      ]),
    );
    expect(keys).not.toContain("branding.server_name");
    expect(keys).not.toContain("branding.login_subtitle");
  });

  it("stages the accent color and its theme tokens instead of saving immediately", () => {
    render(<AppearanceSettings />);

    fireEvent.click(screen.getByRole("button", { name: "Use accent #10b981" }));

    expect(form.save).not.toHaveBeenCalled();
    expect(form.setValue).toHaveBeenCalledWith("branding.accent_color", "#10b981");
    expect(form.setValue).toHaveBeenCalledWith(
      "ui.admin_theme_vars",
      JSON.stringify({ primary: "#10b981", ring: "#10b981", "sidebar-primary": "#10b981" }),
    );
  });

  it("keeps the token editor, custom CSS and theme list behind one advanced disclosure", () => {
    render(<AppearanceSettings />);

    expect(screen.queryByRole("button", { name: "Set primary token" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Advanced · 3 settings/ }));

    expect(screen.getByRole("button", { name: "Set primary token" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Custom CSS editor" })).toBeInTheDocument();
    expect(screen.getByLabelText("Community theme list")).toBeInTheDocument();
  });

  it("stages sanitized CSS while the editor keeps showing what was typed", () => {
    render(<AppearanceSettings />);

    fireEvent.click(screen.getByRole("button", { name: /Advanced · 3 settings/ }));
    const editor = screen.getByRole("textbox", { name: "Custom CSS editor" });
    fireEvent.change(editor, {
      target: { value: '@import "https://example.invalid/x.css"; .card { color: red; }' },
    });

    expect(form.save).not.toHaveBeenCalled();
    expect(form.setValue).toHaveBeenCalledWith(
      "ui.admin_custom_css",
      "/* [blocked @import] */ .card { color: red; }",
    );
    expect(editor).toHaveValue('@import "https://example.invalid/x.css"; .card { color: red; }');
  });
});
