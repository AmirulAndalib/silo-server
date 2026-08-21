import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import IntegrationsSettings from "./IntegrationsSettings";

const mocks = vi.hoisted(() => ({
  checkConnection: vi.fn(),
  discard: vi.fn(),
  save: vi.fn(),
  setValue: vi.fn(),
  toastError: vi.fn(),
  toastInfo: vi.fn(),
  toastSuccess: vi.fn(),
  updateProvider: vi.fn(),
  testProvider: vi.fn(),
  updateSettings: vi.fn(),
  resetValue: vi.fn(),
}));

const values: Record<string, string> = {
  "ai.base_url": "https://text.example.test",
  "ai.chat_model": "chat-model",
  "ai.asr_base_url": "",
  "ai.asr_model": "whisper-model",
  "ai.max_concurrent_jobs": "2",
  "subtitle_ai.base_url": "https://legacy.example.test",
  "subtitle_ai.chat_model": "legacy-chat-model",
  "subtitle_ai.max_concurrent_jobs": "3",
  "subtitle_ai.enabled": "true",
  "subtitle_ai.transcribe_enabled": "false",
  "subtitle_ai.batch_size": "40",
  "subtitle_ai.context_neighbors": "2",
  "subtitle_ai.asr_chunk_seconds": "600",
  "subtitle_ai.transcribe_quota_jobs": "0",
  "subtitle_ai.transcribe_quota_period": "day",
  "metadata_ai.enabled": "false",
  "metadata_ai.on_view": "button",
  "discord.client_id": "1234567890",
};

let dirtyCount = 0;

const useSettingsFormMock = vi.fn((_options?: { keys: string[] }) => ({
  isLoading: false,
  getValue: (key: string) => values[key] ?? "",
  setValue: mocks.setValue,
  resetValue: mocks.resetValue,
  dirtyCount,
  dirtyKeys: [],
  isDirty: vi.fn(() => false),
  save: mocks.save,
  discard: mocks.discard,
  isSaving: false,
  restartRequired: false,
  sensitiveConfigured: ["subtitle_ai.api_key", "discord.client_secret", "discord.bot_token"],
  sensitiveManagedByEnv: [],
  sensitiveStatusReady: true,
  sensitiveStatusError: false,
  buildConnectionCheckRequest: vi.fn(() => ({ values: {}, dirty_keys: [] })),
}));

vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: (options: { keys: string[] }) => useSettingsFormMock(options),
}));

vi.mock("@/hooks/useRestartKeys", () => ({
  useRestartKeys: () => new Set(["ai.max_concurrent_jobs"]),
}));

vi.mock("@/hooks/queries/admin/settings", () => ({
  useAdminServerSettings: () => ({ data: values }),
  useAdminSensitiveStatus: () => ({ data: { configured: [] } }),
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

describe("IntegrationsSettings", () => {
  beforeEach(() => {
    localStorage.clear();
    dirtyCount = 0;
    for (const mock of Object.values(mocks)) mock.mockReset();
    values["ai.base_url"] = "https://text.example.test";
    values["ai.chat_model"] = "chat-model";
    values["ai.asr_base_url"] = "";
    values["ai.asr_model"] = "whisper-model";
    values["ai.max_concurrent_jobs"] = "2";
    values["subtitle_ai.batch_size"] = "40";
    values["subtitle_ai.context_neighbors"] = "2";
    values["subtitle_ai.asr_chunk_seconds"] = "600";
  });

  it("renders every field group heading", () => {
    render(<IntegrationsSettings />);

    for (const heading of [
      "Subtitle providers",
      "Watch providers",
      "Metadata",
      "Apps",
      "AI services",
      "AI features",
    ]) {
      expect(screen.getByRole("group", { name: heading })).toBeInTheDocument();
    }
  });

  it("renders one card per integration in the merged grid", () => {
    render(<IntegrationsSettings />);

    for (const title of [
      "OpenSubtitles",
      "SubDL",
      "SubSource",
      "Trakt",
      "Simkl",
      "MDBList",
      "Discord app",
      "Text model",
      "Speech-to-text",
    ]) {
      expect(screen.getByRole("group", { name: title })).toBeInTheDocument();
    }
  });

  it("shows a status chip per provider card", () => {
    render(<IntegrationsSettings />);

    // OpenSubtitles has saved credentials, SubDL has none.
    expect(screen.getAllByText("Connected").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Not set up").length).toBeGreaterThan(0);
  });

  it("reads legacy subtitle_ai values until the modern ai keys are saved", () => {
    values["ai.base_url"] = "";
    values["ai.chat_model"] = "";

    render(<IntegrationsSettings />);

    expect(screen.getByDisplayValue("https://legacy.example.test")).toBeInTheDocument();
    expect(screen.getByDisplayValue("legacy-chat-model")).toBeInTheDocument();
  });

  it("flags a chat-only endpoint as unable to transcribe", () => {
    values["ai.base_url"] = "https://openrouter.ai/api";

    render(<IntegrationsSettings />);

    expect(screen.getByText("Endpoint cannot transcribe")).toBeInTheDocument();
  });

  it("applies a speech-to-text preset", async () => {
    const user = userEvent.setup();
    render(<IntegrationsSettings />);

    await user.click(screen.getByRole("button", { name: "Groq - fast" }));

    expect(mocks.setValue).toHaveBeenCalledWith("ai.asr_base_url", "https://api.groq.com/openai");
    expect(mocks.setValue).toHaveBeenCalledWith("ai.asr_model", "whisper-large-v3-turbo");
  });

  it("keeps AI tuning behind a collapsed advanced disclosure", async () => {
    const user = userEvent.setup();
    render(<IntegrationsSettings />);

    const toggle = screen.getByRole("button", { name: /Advanced · 6 settings/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByLabelText("Jobs running at once")).not.toBeInTheDocument();

    await user.click(toggle);

    expect(screen.getByLabelText("Jobs running at once")).toBeInTheDocument();
    // Restart-only keys carry the badge instead of hint text.
    expect(screen.getAllByLabelText("Takes effect after a server restart").length).toBe(1);
  });

  it("offers Unlimited instead of a zero sentinel for the transcription allowance", async () => {
    const user = userEvent.setup();
    render(<IntegrationsSettings />);

    await user.click(screen.getByRole("button", { name: /Advanced · 6 settings/ }));

    expect(screen.getByRole("checkbox", { name: "Unlimited" })).toBeChecked();
  });

  it.each([
    ["ai.max_concurrent_jobs", "1.5", "Max concurrent jobs must be a positive whole number."],
    ["subtitle_ai.batch_size", "2abc", "Subtitle batch size must be a positive whole number."],
    [
      "subtitle_ai.context_neighbors",
      "1.5",
      "Subtitle context lines must be zero or a positive whole number.",
    ],
    [
      "subtitle_ai.asr_chunk_seconds",
      "120seconds",
      "Transcription chunk length must be between 60 and 600 seconds.",
    ],
  ])("rejects malformed integer input for %s", async (key, malformedValue, message) => {
    const user = userEvent.setup();
    dirtyCount = 1;
    values[key] = malformedValue;
    render(<IntegrationsSettings />);

    await user.click(screen.getByRole("button", { name: "Save Changes" }));

    expect(mocks.toastError).toHaveBeenCalledWith(message);
    expect(mocks.save).not.toHaveBeenCalled();
  });

  it("runs the text model check against the staged values", async () => {
    const user = userEvent.setup();
    mocks.checkConnection.mockResolvedValue({
      success: true,
      message: "Text connection verified.",
    });
    render(<IntegrationsSettings />);

    await user.click(screen.getByRole("button", { name: "Test text model" }));

    expect(await screen.findByText("Text connection verified.")).toBeInTheDocument();
    expect(mocks.checkConnection).toHaveBeenCalledWith(
      expect.objectContaining({ kind: "ai_chat" }),
    );
  });

  it("saves a subtitle provider through its own card", async () => {
    const user = userEvent.setup();
    render(<IntegrationsSettings />);

    const subdl = screen.getByRole("group", { name: "SubDL" });
    await user.type(within(subdl).getByLabelText("API key"), "key-123");
    await user.click(within(subdl).getByRole("button", { name: "Save" }));

    expect(mocks.updateProvider).toHaveBeenCalledWith(
      { provider: "subdl", config: { enabled: false, api_key: "key-123" } },
      expect.anything(),
    );
  });

  it("keeping a saved AI key reverts the draft instead of staging an empty value", async () => {
    const user = userEvent.setup();
    render(<IntegrationsSettings />);

    const textModel = screen.getByRole("group", { name: "Text model" });
    await user.click(within(textModel).getByRole("button", { name: "Replace API key" }));
    await user.click(within(textModel).getByRole("button", { name: "Keep saved API key" }));

    // Staging "" would erase the stored key on the next Save Changes.
    expect(mocks.setValue).not.toHaveBeenCalledWith("ai.api_key", "");
    expect(mocks.resetValue).toHaveBeenCalledWith("ai.api_key");
  });

  it("saves Discord app credentials without touching the page save bar", async () => {
    const user = userEvent.setup();
    mocks.updateSettings.mockResolvedValue({ values: {}, restart_required: false });
    render(<IntegrationsSettings />);

    const discord = screen.getByRole("group", { name: "Discord app" });
    await user.click(within(discord).getByRole("button", { name: "Save" }));

    expect(mocks.updateSettings).toHaveBeenCalledWith({ "discord.client_id": "1234567890" });
    expect(mocks.save).not.toHaveBeenCalled();
  });
});
