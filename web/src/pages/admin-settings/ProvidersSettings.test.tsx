import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ProvidersSettings from "./ProvidersSettings";

const mocks = vi.hoisted(() => ({
  checkConnection: vi.fn(),
  toastError: vi.fn(),
  toastInfo: vi.fn(),
  toastSuccess: vi.fn(),
  updateProvider: vi.fn(),
  testProvider: vi.fn(),
  updateSettings: vi.fn(),
}));

let sensitiveConfigured: string[] = ["mdblist.api_key"];

const useSettingsFormMock = vi.fn((_options?: { keys: string[] }) => ({
  isLoading: false,
  getValue: () => "",
  setValue: vi.fn(),
  resetValue: vi.fn(),
  dirtyCount: 0,
  dirtyKeys: [],
  isDirty: vi.fn(() => false),
  save: vi.fn(),
  discard: vi.fn(),
  isSaving: false,
  restartRequired: false,
  sensitiveConfigured,
  sensitiveManagedByEnv: [],
  sensitiveStatusReady: true,
  sensitiveStatusError: false,
  buildConnectionCheckRequest: vi.fn(() => ({ values: {}, dirty_keys: [] })),
}));

vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: (options: { keys: string[] }) => useSettingsFormMock(options),
}));

vi.mock("@/hooks/useRestartKeys", () => ({
  useRestartKeys: () => new Set<string>(),
}));

vi.mock("@/hooks/queries/admin/settings", () => ({
  useUpdateServerSettings: () => ({ mutateAsync: mocks.updateSettings, isPending: false }),
  useCheckAdminSettingsConnection: () => ({
    mutateAsync: mocks.checkConnection,
    isPending: false,
  }),
}));

vi.mock("@/hooks/queries/admin/subtitles", () => ({
  useSubtitleProviders: () => ({
    data: {
      providers: [
        {
          provider_name: "subdl",
          enabled: false,
          has_api_key: false,
          has_credentials: false,
          updated_at: "",
        },
        {
          provider_name: "opensubtitles",
          enabled: true,
          has_api_key: false,
          has_credentials: true,
          updated_at: "",
        },
        {
          provider_name: "subsource",
          enabled: false,
          has_api_key: true,
          has_credentials: false,
          updated_at: "",
        },
      ],
    },
    isLoading: false,
  }),
  useUpdateSubtitleProvider: () => ({ mutate: mocks.updateProvider, isPending: false }),
  useTestSubtitleProvider: () => ({ mutate: mocks.testProvider, isPending: false }),
}));

vi.mock("sonner", () => ({
  toast: {
    error: mocks.toastError,
    info: mocks.toastInfo,
    success: mocks.toastSuccess,
  },
}));

describe("ProvidersSettings", () => {
  beforeEach(() => {
    localStorage.clear();
    sensitiveConfigured = ["mdblist.api_key"];
    for (const mock of Object.values(mocks)) mock.mockReset();
  });

  it("heads the page and both provider groups", () => {
    render(<ProvidersSettings />);

    expect(
      screen.getByRole("heading", { level: 2, name: "Subtitles & Metadata" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Where Silo fetches subtitles, artwork, and descriptions."),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("group", { name: "Subtitle providers" })).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "Metadata providers" })).toBeInTheDocument();
    expect(screen.queryByText("Searched in order, top to bottom")).not.toBeInTheDocument();
  });

  it("shows one tile per provider, in search order", () => {
    render(<ProvidersSettings />);

    const tiles = ["OpenSubtitles", "SubDL", "SubSource", "MDBList"].map((name) =>
      screen.getByRole("group", { name }),
    );
    for (const tile of tiles) expect(tile).toBeInTheDocument();
    expect(tiles[0]?.compareDocumentPosition(tiles[1] as Node)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
  });

  it("derives each tile state from the stored credentials", () => {
    render(<ProvidersSettings />);

    const openSubtitles = screen.getByRole("group", { name: "OpenSubtitles" });
    expect(openSubtitles).toHaveAttribute("data-state", "connected");
    expect(within(openSubtitles).getByText("Connected")).toBeInTheDocument();
    // The state word is the only signal: no "credentials stored" line repeating it.
    expect(within(openSubtitles).queryByText(/credentials stored/)).not.toBeInTheDocument();

    const subdl = screen.getByRole("group", { name: "SubDL" });
    expect(subdl).toHaveAttribute("data-state", "not_connected");
    expect(within(subdl).getByText("Not connected")).toBeInTheDocument();
    expect(within(subdl).getByRole("button", { name: "Connect" })).toBeInTheDocument();

    // Configured but switched off: not searched, so not "connected".
    const subsource = screen.getByRole("group", { name: "SubSource" });
    expect(subsource).toHaveAttribute("data-state", "not_connected");
    expect(within(subsource).getByText("Connected · off")).toBeInTheDocument();

    // MDBList's credential is a server setting, read from sensitive status.
    const mdblist = screen.getByRole("group", { name: "MDBList" });
    expect(mdblist).toHaveAttribute("data-state", "connected");
    expect(within(mdblist).getByRole("button", { name: "Manage" })).toBeInTheDocument();
  });

  it("counts a missing MDBList key as not connected", () => {
    sensitiveConfigured = [];

    render(<ProvidersSettings />);

    expect(screen.getByRole("group", { name: "MDBList" })).toHaveAttribute(
      "data-state",
      "not_connected",
    );
  });

  it("expands one tile in place and collapses it again", async () => {
    const user = userEvent.setup();
    render(<ProvidersSettings />);

    expect(screen.queryByLabelText("API key")).not.toBeInTheDocument();

    await user.click(
      within(screen.getByRole("group", { name: "SubDL" })).getByRole("button", { name: "Connect" }),
    );

    const subdl = screen.getByRole("group", { name: "SubDL" });
    expect(subdl).toHaveAttribute("data-expanded", "true");
    expect(subdl).toHaveAttribute("data-state", "editing");
    expect(within(subdl).getByLabelText("API key")).toBeInTheDocument();
    expect(within(subdl).getByRole("button", { name: "Test connection" })).toBeInTheDocument();
    // Only one panel is open at a time.
    expect(screen.getByRole("group", { name: "SubSource" })).not.toHaveAttribute("data-expanded");

    await user.click(within(subdl).getByRole("button", { name: "Close" }));

    expect(screen.getByRole("group", { name: "SubDL" })).not.toHaveAttribute("data-expanded");
    expect(screen.queryByLabelText("API key")).not.toBeInTheDocument();
  });

  it("swaps the expanded panel when another tile is opened", async () => {
    const user = userEvent.setup();
    render(<ProvidersSettings />);

    await user.click(
      within(screen.getByRole("group", { name: "SubDL" })).getByRole("button", { name: "Connect" }),
    );
    await user.click(
      within(screen.getByRole("group", { name: "MDBList" })).getByRole("button", {
        name: "Manage",
      }),
    );

    expect(screen.getByRole("group", { name: "SubDL" })).not.toHaveAttribute("data-expanded");
    expect(screen.getByRole("group", { name: "MDBList" })).toHaveAttribute("data-expanded", "true");
  });

  it("saves a subtitle provider from its own panel", async () => {
    const user = userEvent.setup();
    render(<ProvidersSettings />);

    await user.click(
      within(screen.getByRole("group", { name: "SubDL" })).getByRole("button", { name: "Connect" }),
    );
    const subdl = screen.getByRole("group", { name: "SubDL" });
    await user.type(within(subdl).getByLabelText("API key"), "key-123");
    await user.click(within(subdl).getByRole("button", { name: "Save" }));

    expect(mocks.updateProvider).toHaveBeenCalledWith(
      { provider: "subdl", config: { enabled: false, api_key: "key-123" } },
      expect.anything(),
    );
  });

  it("surfaces a failed provider test on the tile itself", async () => {
    const user = userEvent.setup();
    mocks.testProvider.mockImplementation((_vars, options) => {
      options.onSuccess({ success: false, error: "401 — key rejected" });
    });
    render(<ProvidersSettings />);

    await user.click(
      within(screen.getByRole("group", { name: "SubSource" })).getByRole("button", {
        name: "Manage",
      }),
    );
    await user.click(
      within(screen.getByRole("group", { name: "SubSource" })).getByRole("button", {
        name: "Test connection",
      }),
    );
    await user.click(
      within(screen.getByRole("group", { name: "SubSource" })).getByRole("button", {
        name: "Close",
      }),
    );

    const subsource = screen.getByRole("group", { name: "SubSource" });
    expect(subsource).toHaveAttribute("data-state", "error");
    expect(within(subsource).getByRole("button", { name: "Fix" })).toBeInTheDocument();
    expect(within(subsource).getByText("401 — key rejected")).toBeInTheDocument();
  });

  it("points metadata plugins at the plugins page instead of faking tiles", () => {
    render(<ProvidersSettings />);

    expect(screen.getByRole("link", { name: "Plugins" })).toHaveAttribute("href", "/admin/plugins");
    expect(screen.queryByRole("group", { name: "TMDB" })).not.toBeInTheDocument();
  });
});
