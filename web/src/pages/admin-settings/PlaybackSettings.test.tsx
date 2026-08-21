import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

import PlaybackSettings from "./PlaybackSettings";

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;
if (!window.HTMLElement.prototype.hasPointerCapture) {
  window.HTMLElement.prototype.hasPointerCapture = () => false;
}
if (!window.HTMLElement.prototype.scrollIntoView) {
  window.HTMLElement.prototype.scrollIntoView = () => {};
}

const useSettingsFormMock = vi.fn();
const useHWAccelDetectionMock = vi.fn();
const useAdminServerStatusMock = vi.fn();

vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: (...args: unknown[]) => useSettingsFormMock(...args),
}));

vi.mock("@/hooks/useRestartKeys", () => ({
  useRestartKeys: () => new Set<string>(["playback.ffmpeg_path"]),
}));

vi.mock("@/hooks/queries/admin/system", () => ({
  useHWAccelDetection: (...args: unknown[]) => useHWAccelDetectionMock(...args),
}));

vi.mock("@/hooks/queries/admin/settings", () => ({
  useAdminServerStatus: () => useAdminServerStatusMock(),
}));

function makeForm(values: Record<string, string>, dirty: string[] = []) {
  const dirtyKeys = new Set(dirty);
  return {
    isLoading: false,
    getValue: (key: string) => values[key] ?? "",
    setValue: vi.fn(),
    isDirty: (key: string) => dirtyKeys.has(key),
    dirtyCount: dirtyKeys.size,
    dirtyKeys: [...dirtyKeys],
    save: vi.fn(),
    discard: vi.fn(),
    isSaving: false,
    restartRequired: false,
  };
}

function parse(markup: string): HTMLElement {
  const container = document.createElement("div");
  container.innerHTML = markup;
  return container;
}

function labelled(container: HTMLElement, text: string): Element {
  const label = Array.from(container.querySelectorAll("label")).find(
    (candidate) => candidate.textContent === text,
  );
  const control = label?.htmlFor ? container.querySelector(`[id="${label.htmlFor}"]`) : null;
  if (!control) throw new Error(`no control rendered for label: ${text}`);
  return control;
}

/** Opens both advanced disclosures via their persisted state. */
function expandAdvanced() {
  localStorage.setItem("silo.admin.advanced.playback.transcoding", "true");
  localStorage.setItem("silo.admin.advanced.playback.downloads", "true");
}

const TONE_MAP_LABEL = "Convert HDR colors on the CPU when the GPU cannot";

beforeEach(() => {
  localStorage.clear();
  useSettingsFormMock.mockReset();
  useHWAccelDetectionMock.mockReset();
  useHWAccelDetectionMock.mockReturnValue({ data: undefined, isLoading: false });
  useAdminServerStatusMock.mockReset();
  useAdminServerStatusMock.mockReturnValue({ data: undefined });
});

describe("PlaybackSettings layout", () => {
  it("renders every field group heading", () => {
    useSettingsFormMock.mockReturnValue(makeForm({ "playback.hw_accel": "none" }));

    const container = parse(renderToStaticMarkup(<PlaybackSettings />));
    const headings = Array.from(container.querySelectorAll("[role=group]")).map((group) => {
      const labelId = group.getAttribute("aria-labelledby");
      return labelId ? (container.querySelector(`[id="${labelId}"]`)?.textContent ?? "") : "";
    });

    expect(headings).toEqual(["Transcoding", "Watch behavior", "Downloads"]);
  });

  it("summarises the tab in the status strip under the title", () => {
    useSettingsFormMock.mockReturnValue(
      makeForm({ "playback.hw_accel": "none", "playback.transcode_enabled": "true" }),
    );

    const container = parse(renderToStaticMarkup(<PlaybackSettings />));

    expect(container.textContent).toContain("Playback");
    expect(container.textContent).toContain("Transcoding on");
    expect(container.textContent).toContain("Software encoding");
  });

  it("shows restart pending from the server status even without a save this session", () => {
    useSettingsFormMock.mockReturnValue(
      makeForm({ "playback.hw_accel": "none", "playback.transcode_enabled": "true" }),
    );
    useAdminServerStatusMock.mockReturnValue({ data: { restart_required: true } });

    const container = parse(renderToStaticMarkup(<PlaybackSettings />));

    expect(container.textContent).toContain("Restart pending");
  });

  it("manages both the playback and download key families in one form", () => {
    useSettingsFormMock.mockReturnValue(makeForm({ "playback.hw_accel": "none" }));

    renderToStaticMarkup(<PlaybackSettings />);
    const keys: string[] = useSettingsFormMock.mock.calls[0]?.[0]?.keys ?? [];

    expect(keys).toContain("playback.transcode_enabled");
    expect(keys).toContain("download.enabled");
    expect(keys).toContain("download.artifact_max_bytes");
    // Hidden tier: still saved and readable through the API, no UI.
    expect(keys).not.toContain("playback.chapter_thumbnail_node_capacity");
  });

  it("keeps advanced settings collapsed until they are opened", () => {
    useSettingsFormMock.mockReturnValue(makeForm({ "playback.hw_accel": "none" }));

    const container = parse(renderToStaticMarkup(<PlaybackSettings />));

    expect(container.textContent).toContain("Transcoding");
    expect(container.textContent).not.toContain("FFmpeg path");
    expect(container.textContent).not.toContain("Server bandwidth");
  });

  it("force-opens an advanced section holding a dirty field", () => {
    useSettingsFormMock.mockReturnValue(
      makeForm({ "playback.hw_accel": "none" }, ["download.artifact_dir"]),
    );

    const container = parse(renderToStaticMarkup(<PlaybackSettings />));

    expect(container.textContent).toContain("Prepared file directory");
    expect(container.textContent).not.toContain("FFmpeg path");
  });

  it("marks restart-required fields from the restart key list", () => {
    expandAdvanced();
    useSettingsFormMock.mockReturnValue(makeForm({ "playback.hw_accel": "none" }));

    const container = parse(renderToStaticMarkup(<PlaybackSettings />));
    const badges = container.querySelectorAll("[aria-label='Takes effect after a server restart']");

    expect(badges).toHaveLength(1);
  });
});

describe("PlaybackSettings CPU tone mapping", () => {
  it("includes the setting and renders it off by default", () => {
    expandAdvanced();
    useSettingsFormMock.mockReturnValue(
      makeForm({
        "playback.hw_accel": "none",
        "playback.chapter_thumbnail_hdr_policy": "best_effort",
      }),
    );

    const container = parse(renderToStaticMarkup(<PlaybackSettings />));

    expect(useSettingsFormMock.mock.calls[0]?.[0]?.keys).toContain(
      "playback.chapter_thumbnail_software_tone_map_enabled",
    );
    const toggle = labelled(container, TONE_MAP_LABEL);
    expect(toggle).toHaveAttribute("aria-checked", "false");
    expect(toggle).not.toHaveAttribute("disabled");
  });

  it("offers VideoToolbox hardware acceleration", async () => {
    useSettingsFormMock.mockReturnValue(makeForm({ "playback.hw_accel": "auto" }));

    render(<PlaybackSettings />);
    await userEvent.click(screen.getByRole("combobox", { name: "Hardware Acceleration" }));

    expect(screen.getByRole("option", { name: "VideoToolbox (macOS)" })).toBeInTheDocument();
  });

  it("disables the toggle while HDR chapter thumbnails are disabled", () => {
    expandAdvanced();
    useSettingsFormMock.mockReturnValue(
      makeForm({
        "playback.hw_accel": "none",
        "playback.chapter_thumbnail_hdr_policy": "disabled",
        "playback.chapter_thumbnail_software_tone_map_enabled": "true",
      }),
    );

    const container = parse(renderToStaticMarkup(<PlaybackSettings />));
    const toggle = labelled(container, TONE_MAP_LABEL);

    expect(toggle).toHaveAttribute("aria-checked", "true");
    expect(toggle).toHaveAttribute("disabled");
  });
});

describe("PlaybackSettings transcode tone mapping", () => {
  beforeEach(expandAdvanced);

  it("registers independent hardware and software settings disabled by default", () => {
    useSettingsFormMock.mockReturnValue(makeForm({ "playback.hw_accel": "auto" }));

    const container = parse(renderToStaticMarkup(<PlaybackSettings />));
    const keys = useSettingsFormMock.mock.calls[0]?.[0]?.keys as string[];

    expect(keys).toContain("playback.transcode_hardware_tone_map_enabled");
    expect(keys).toContain("playback.transcode_software_tone_map_enabled");
    expect(labelled(container, "Enable Hardware HDR Tone Mapping")).toHaveAttribute(
      "aria-checked",
      "false",
    );
    expect(labelled(container, "Enable Software HDR Tone Mapping")).toHaveAttribute(
      "aria-checked",
      "false",
    );
  });

  it("keeps the two policies independent", () => {
    useSettingsFormMock.mockReturnValue(
      makeForm({
        "playback.hw_accel": "qsv",
        "playback.transcode_hardware_tone_map_enabled": "true",
        "playback.transcode_software_tone_map_enabled": "false",
      }),
    );

    const container = parse(renderToStaticMarkup(<PlaybackSettings />));

    expect(labelled(container, "Enable Hardware HDR Tone Mapping")).toHaveAttribute(
      "aria-checked",
      "true",
    );
    expect(labelled(container, "Enable Software HDR Tone Mapping")).toHaveAttribute(
      "aria-checked",
      "false",
    );
  });

  it("keeps hardware tone mapping configurable for remote executors when local acceleration is off", () => {
    useSettingsFormMock.mockReturnValue(
      makeForm({
        "playback.hw_accel": "none",
        "playback.transcode_hardware_tone_map_enabled": "true",
      }),
    );

    const toggle = labelled(
      parse(renderToStaticMarkup(<PlaybackSettings />)),
      "Enable Hardware HDR Tone Mapping",
    );

    expect(toggle).toHaveAttribute("aria-checked", "true");
    expect(toggle).not.toHaveAttribute("disabled");
  });

  it("keeps hardware tone mapping configurable when one detected executor is software-only", () => {
    useHWAccelDetectionMock.mockReturnValue({
      data: {
        resolved: "none",
        nodes: [
          { node_url: "http://software-node", resolved: "none" },
          { node_url: "http://gpu-node", resolved: "qsv" },
        ],
      },
      isLoading: false,
    });
    useSettingsFormMock.mockReturnValue(
      makeForm({
        "playback.hw_accel": "auto",
        "playback.transcode_hardware_tone_map_enabled": "true",
      }),
    );

    const toggle = labelled(
      parse(renderToStaticMarkup(<PlaybackSettings />)),
      "Enable Hardware HDR Tone Mapping",
    );

    expect(toggle).toHaveAttribute("aria-checked", "true");
    expect(toggle).not.toHaveAttribute("disabled");
  });
});
