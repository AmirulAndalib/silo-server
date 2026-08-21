import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";

import LibraryMetadataSettings from "./LibraryMetadataSettings";

const useSettingsFormMock = vi.fn();
const useRestartKeysMock = vi.fn(() => new Set<string>());
const storageAvailableMock = vi.fn(() => true);

vi.mock("@/hooks/useBranding", () => ({
  useBranding: () => ({ storageAvailable: storageAvailableMock() }),
}));

vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: (...args: unknown[]) => useSettingsFormMock(...args),
}));

vi.mock("@/hooks/useRestartKeys", () => ({
  useRestartKeys: () => useRestartKeysMock(),
}));

vi.mock("@/hooks/queries/admin/settings", () => ({
  useCheckAdminSettingsConnection: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useCatalogSearchStatus: () => ({ data: undefined, isLoading: true }),
}));

vi.mock("@/hooks/queries/admin/markers", () => ({
  useMarkerProviders: () => ({ data: { providers: [] }, isLoading: false }),
  useUpdateMarkerProvider: () => ({ mutate: vi.fn(), isPending: false }),
  useValidateMarkerProvider: () => ({ mutate: vi.fn(), isPending: false, data: undefined }),
}));

vi.mock("@/hooks/queries/admin/tasks", () => ({
  useTasks: () => ({ data: [] }),
  useRunTask: () => ({ mutateAsync: vi.fn() }),
}));

vi.mock("@/components/realtimeEventsContext", () => ({
  useEventChannel: () => undefined,
}));

function makeForm(values: Record<string, string>, dirty: string[] = []) {
  const dirtySet = new Set(dirty);
  return {
    isLoading: false,
    getValue: (key: string) => values[key] ?? "",
    setValue: vi.fn(),
    resetValue: vi.fn(),
    isDirty: (key: string) => dirtySet.has(key),
    dirtyCount: dirtySet.size,
    save: vi.fn(),
    discard: vi.fn(),
    isSaving: false,
    restartRequired: false,
    sensitiveConfigured: [] as string[],
    buildConnectionCheckRequest: vi.fn(() => ({ values: {}, dirty_keys: [] })),
  };
}

function render(values: Record<string, string>, dirty: string[] = []) {
  useSettingsFormMock.mockReturnValue(makeForm(values, dirty));
  return renderToStaticMarkup(
    <MemoryRouter>
      <LibraryMetadataSettings />
    </MemoryRouter>,
  );
}

function text(markup: string): string {
  const container = document.createElement("div");
  container.innerHTML = markup;
  return container.textContent ?? "";
}

// The toggle is a Radix switch, so reach it through the label association
// SettingField sets up rather than by scanning the markup for "disabled".
function toggleDisabled(markup: string, label: string): boolean {
  const container = document.createElement("div");
  container.innerHTML = markup;
  const labelEl = Array.from(container.querySelectorAll("label")).find(
    (el) => el.textContent?.trim() === label,
  );
  if (!labelEl?.htmlFor) throw new Error(`no label found for ${label}`);
  const control = container.querySelector(`[id="${labelEl.htmlFor}"]`);
  if (!control) throw new Error(`no control found for ${label}`);
  return control.hasAttribute("disabled");
}

describe("LibraryMetadataSettings", () => {
  beforeEach(() => {
    localStorage.clear();
    useRestartKeysMock.mockReturnValue(new Set<string>());
    storageAvailableMock.mockReturnValue(true);
  });

  it("renders every field group heading", () => {
    const rendered = text(render({ "catalog.search.provider": "meilisearch" }));

    for (const heading of ["Metadata", "Scanning", "Intro and credits markers", "Search"]) {
      expect(rendered).toContain(heading);
    }
  });

  it("summarises the tab in the status strip under the title", () => {
    const rendered = text(render({ "catalog.search.provider": "postgres" }));

    expect(rendered).toContain("Library & Metadata");
    expect(rendered).toContain("Artwork caching off");
    expect(rendered).toContain("Markers detected here");
    expect(rendered).toContain("Search: built-in (Postgres)");
  });

  it("manages the merged key set of the three tabs it replaces", () => {
    render({});

    const calls = useSettingsFormMock.mock.calls;
    const keys: string[] = calls[calls.length - 1]?.[0]?.keys ?? [];
    expect(keys).toEqual(
      expect.arrayContaining([
        "metadata.cache_images",
        "scanner.workers",
        "matcher.workers",
        "matcher.batch_size",
        "markers.mode",
        "markers.lazy_playback",
        "catalog.search.provider",
        "catalog.search.meilisearch.url",
        "catalog.search.meilisearch.api_key",
        "catalog.search.meilisearch.semantic_ratio",
      ]),
    );
    // Hidden tier: still saved through the API, no control on this tab.
    expect(keys).not.toContain("catalog.search.meilisearch.embedder");
    expect(keys).not.toContain("catalog.search.meilisearch.binary_quantized");
    expect(keys).not.toContain("catalog.search.meilisearch.rebuild_batch_size");
  });

  it("keeps advanced settings collapsed but expands a section holding a staged edit", () => {
    expect(text(render({ "catalog.search.provider": "postgres" }))).not.toContain(
      "Scanner workers",
    );

    expect(text(render({ "catalog.search.provider": "postgres" }, ["scanner.workers"]))).toContain(
      "Scanner workers",
    );
  });

  it("hides Meilisearch connection fields until that engine is selected", () => {
    expect(text(render({ "catalog.search.provider": "postgres" }))).not.toContain(
      "Meilisearch URL",
    );

    expect(text(render({ "catalog.search.provider": "meilisearch" }))).toContain("Meilisearch URL");
  });

  it("leaves S3 image caching editable and unannotated while public storage is active", () => {
    const rendered = render({ "s3.public_bucket": "silo-public" });

    expect(text(rendered)).toContain("S3 image caching");
    expect(text(rendered)).not.toContain("Restart the server for image caching to start");
    expect(text(rendered)).not.toContain("S3 image caching needs S3 object storage");
    expect(toggleDisabled(rendered, "S3 image caching")).toBe(false);
  });

  it("keeps S3 image caching settable when the bucket is saved but not active yet", () => {
    storageAvailableMock.mockReturnValue(false);

    const rendered = render({ "s3.public_bucket": "silo-public" });

    expect(text(rendered)).toContain("Restart the server for image caching to start");
    expect(toggleDisabled(rendered, "S3 image caching")).toBe(false);
  });

  it("disables S3 image caching and links to Infrastructure when no bucket is configured", () => {
    storageAvailableMock.mockReturnValue(false);

    const rendered = render({});

    expect(text(rendered)).toContain("S3 image caching needs S3 object storage");
    expect(rendered).toContain("/admin/settings?tab=infrastructure");
    expect(toggleDisabled(rendered, "S3 image caching")).toBe(true);
  });

  it("still allows switching S3 image caching off when the bucket went away", () => {
    storageAvailableMock.mockReturnValue(false);

    const rendered = render({ "metadata.cache_images": "true" });

    expect(text(rendered)).toContain("S3 image caching needs S3 object storage");
    expect(toggleDisabled(rendered, "S3 image caching")).toBe(false);
  });

  it("marks a restart-required field with the restart badge", () => {
    useRestartKeysMock.mockReturnValue(new Set(["metadata.cache_images"]));

    expect(text(render({ "catalog.search.provider": "postgres" }))).toContain("Restart");
  });
});
