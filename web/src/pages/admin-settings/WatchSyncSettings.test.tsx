import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import WatchSyncSettings from "./WatchSyncSettings";

const mocks = vi.hoisted(() => ({
  toastInfo: vi.fn(),
  toastSuccess: vi.fn(),
  updateSettings: vi.fn(),
}));

let sensitiveConfigured: string[] = ["watchsync.trakt.client_id", "watchsync.trakt.client_secret"];

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
  useAdminServerStatus: () => ({ data: undefined }),
}));

vi.mock("sonner", () => ({
  toast: {
    info: mocks.toastInfo,
    success: mocks.toastSuccess,
  },
}));

describe("WatchSyncSettings", () => {
  beforeEach(() => {
    localStorage.clear();
    sensitiveConfigured = ["watchsync.trakt.client_id", "watchsync.trakt.client_secret"];
    for (const mock of Object.values(mocks)) mock.mockReset();
    mocks.updateSettings.mockResolvedValue({ values: {}, restart_required: false });
  });

  it("heads the page and lists both providers", () => {
    render(<WatchSyncSettings />);

    expect(screen.getByRole("heading", { level: 1, name: "Watch sync" })).toBeInTheDocument();
    expect(
      screen.queryByText("Keep watch history in sync with Trakt and Simkl."),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("group", { name: "Trakt" })).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "Simkl" })).toBeInTheDocument();
  });

  it("registers only the watch sync app credentials with the settings form", () => {
    render(<WatchSyncSettings />);

    expect(useSettingsFormMock).toHaveBeenCalledWith({
      keys: [
        "watchsync.trakt.client_id",
        "watchsync.trakt.client_secret",
        "watchsync.simkl.client_id",
        "watchsync.simkl.client_secret",
      ],
    });
  });

  it("derives tile state from the stored credentials", () => {
    render(<WatchSyncSettings />);

    expect(screen.getByRole("group", { name: "Trakt" })).toHaveAttribute("data-state", "connected");
    expect(screen.getByRole("group", { name: "Simkl" })).toHaveAttribute(
      "data-state",
      "not_connected",
    );
    expect(screen.getByText("Connected")).toBeInTheDocument();
    expect(screen.getByText("Not connected")).toBeInTheDocument();
    // The state word is the only signal; the tile no longer repeats it as a
    // "credentials stored" line underneath.
    expect(screen.queryByText("App credentials stored")).not.toBeInTheDocument();
  });

  it("marks a half-configured provider as partly set up", () => {
    sensitiveConfigured = ["watchsync.simkl.client_id"];

    render(<WatchSyncSettings />);

    expect(screen.getByText("Partly set up")).toBeInTheDocument();
  });

  it("expands one tile in place and collapses it again", async () => {
    const user = userEvent.setup();
    render(<WatchSyncSettings />);

    expect(screen.queryByLabelText("Client ID")).not.toBeInTheDocument();

    await user.click(
      within(screen.getByRole("group", { name: "Simkl" })).getByRole("button", { name: "Connect" }),
    );

    const simkl = screen.getByRole("group", { name: "Simkl" });
    expect(simkl).toHaveAttribute("data-expanded", "true");
    expect(within(simkl).getByLabelText("Client ID")).toBeInTheDocument();
    expect(within(simkl).getByLabelText("Client secret")).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "Trakt" })).not.toHaveAttribute("data-expanded");

    await user.click(within(simkl).getByRole("button", { name: "Close" }));

    expect(screen.queryByLabelText("Client ID")).not.toBeInTheDocument();
  });

  it("saves only the credentials that were typed", async () => {
    const user = userEvent.setup();
    render(<WatchSyncSettings />);

    await user.click(
      within(screen.getByRole("group", { name: "Simkl" })).getByRole("button", { name: "Connect" }),
    );
    const simkl = screen.getByRole("group", { name: "Simkl" });
    await user.type(within(simkl).getByLabelText("Client ID"), "simkl-id");
    await user.click(within(simkl).getByRole("button", { name: "Save" }));

    expect(mocks.updateSettings).toHaveBeenCalledWith({ "watchsync.simkl.client_id": "simkl-id" });
  });

  it("clears a connected provider behind a confirmation", async () => {
    const user = userEvent.setup();
    render(<WatchSyncSettings />);

    await user.click(
      within(screen.getByRole("group", { name: "Trakt" })).getByRole("button", { name: "Manage" }),
    );
    await user.click(
      within(screen.getByRole("group", { name: "Trakt" })).getByRole("button", {
        name: "Clear credentials",
      }),
    );

    expect(screen.getByText("Clear Trakt credentials?")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Clear" }));

    expect(mocks.updateSettings).toHaveBeenCalledWith({
      "watchsync.trakt.client_id": "",
      "watchsync.trakt.client_secret": "",
    });
  });
});
