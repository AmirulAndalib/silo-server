import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { RateLimitConfig } from "@/api/types";
import SecurityAccessSettings from "./SecurityAccessSettings";

const useSettingsFormMock = vi.fn();
const rateLimitConfigMock = vi.fn();
const updateRateLimitMock = vi.fn();
const serverStatusMock = vi.fn();

vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: (...args: unknown[]) => useSettingsFormMock(...args),
}));

vi.mock("@/hooks/useRestartKeys", () => ({
  useRestartKeys: () => new Set<string>(["auth.access_token_expiry"]),
}));

vi.mock("@/hooks/queries/admin/rateLimits", () => ({
  useRateLimitConfig: () => rateLimitConfigMock(),
  useUpdateRateLimitConfig: () => updateRateLimitMock(),
}));

vi.mock("@/hooks/queries/admin/settings", () => ({
  useAdminServerStatus: () => serverStatusMock(),
}));

const SERVER_CONFIG: RateLimitConfig = {
  enabled: true,
  backend: "memory",
  global_requests_per_second: 1000,
  tiers: {
    standard: { requests_per_second: 20, requests_per_minute: 1200, burst: 20 },
    elevated: { requests_per_second: 100, requests_per_minute: 6000, burst: 100 },
  },
  ip_requests_per_second: 120,
  ip_requests_per_minute: 6000,
  ip_burst: 120,
  auth_endpoints: {
    login: { requests_per_minute: 20, burst: 10 },
    signup: { requests_per_minute: 10, burst: 6 },
    setup: { requests_per_minute: 10, burst: 6 },
    device_start: { requests_per_minute: 20, burst: 10 },
    device_lookup: { requests_per_minute: 60, burst: 20 },
    device_poll: { requests_per_minute: 120, burst: 30 },
    autoscan_webhook: { requests_per_minute: 60, burst: 30 },
  },
  active: true,
  active_backend: "memory",
};

function makeForm(overrides: Record<string, unknown> = {}) {
  return {
    isLoading: false,
    getValue: () => "",
    setValue: vi.fn(),
    isDirty: () => false,
    dirtyCount: 0,
    save: vi.fn(),
    discard: vi.fn(),
    isSaving: false,
    restartRequired: false,
    sensitiveConfigured: [],
    sensitiveManagedByEnv: [],
    ...overrides,
  };
}

describe("SecurityAccessSettings", () => {
  beforeEach(() => {
    localStorage.clear();
    useSettingsFormMock.mockReset();
    useSettingsFormMock.mockReturnValue(makeForm());
    rateLimitConfigMock.mockReturnValue({ data: SERVER_CONFIG, isLoading: false });
    updateRateLimitMock.mockReturnValue({ mutate: vi.fn(), isPending: false, data: undefined });
    serverStatusMock.mockReturnValue({ data: undefined });
  });

  it("renders every field group", () => {
    render(<SecurityAccessSettings />);

    for (const heading of ["Sign-in sessions", "Network", "Rate limiting"]) {
      expect(screen.getByRole("group", { name: heading })).toBeInTheDocument();
    }
  });

  it("summarises the tab in the status strip under the title", () => {
    render(<SecurityAccessSettings />);

    expect(screen.getByRole("heading", { name: "Security & Access" })).toBeInTheDocument();
    expect(screen.getByText("Sign-in lasts the default")).toBeInTheDocument();
    expect(screen.getByText("Trusted proxies: private ranges only")).toBeInTheDocument();
    expect(screen.getByText("Rate limiting on · this server only")).toBeInTheDocument();
  });

  it("keeps the token and proxy keys on the batched settings form", () => {
    render(<SecurityAccessSettings />);

    expect(useSettingsFormMock.mock.calls[0]?.[0]?.keys).toEqual([
      "auth.access_token_expiry",
      "auth.refresh_token_expiry",
      "clientip.trusted_proxies",
    ]);
  });

  it("shows only the rate limiting switch until Advanced is opened", async () => {
    render(<SecurityAccessSettings />);

    expect(screen.getByRole("switch", { name: /Enable rate limiting/i })).toBeInTheDocument();
    expect(screen.queryByText("Per client address")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /Advanced/i }));

    expect(screen.getByText("Per client address")).toBeInTheDocument();
    expect(screen.getByText("Standard API keys")).toBeInTheDocument();
    expect(screen.getByText("Sign in")).toBeInTheDocument();
  });

  it("marks an edited rate-limit row dirty and counts it in the Advanced disclosure", async () => {
    render(<SecurityAccessSettings />);

    await userEvent.click(screen.getByRole("button", { name: /Advanced/i }));

    const rpsInput = screen.getByLabelText("Whole-server requests per second") as HTMLInputElement;
    await userEvent.clear(rpsInput);
    await userEvent.type(rpsInput, "500");

    expect(rpsInput.closest('[data-dirty="true"]')).not.toBeNull();
    expect(screen.getByRole("button", { name: /1 changed/i })).toBeInTheDocument();

    // An untouched row stays clean.
    expect(screen.getByText("Per client address").closest('[data-dirty="true"]')).toBeNull();
  });

  it("counts an edited rate limit toward the shared save bar and saves both writers", async () => {
    const save = vi.fn();
    const mutate = vi.fn();
    useSettingsFormMock.mockReturnValue(makeForm({ dirtyCount: 1, save }));
    updateRateLimitMock.mockReturnValue({ mutate, isPending: false, data: undefined });

    render(<SecurityAccessSettings />);

    await userEvent.click(screen.getByRole("switch", { name: /Enable rate limiting/i }));
    expect(screen.getByText("2 unsaved changes")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /Save Changes/i }));

    expect(mutate).toHaveBeenCalledWith(expect.objectContaining({ enabled: false }));
    expect(save).toHaveBeenCalled();
  });

  it("warns when the running limiter disagrees with the saved backend", () => {
    rateLimitConfigMock.mockReturnValue({
      data: { ...SERVER_CONFIG, backend: "redis", active_backend: "memory" },
      isLoading: false,
    });

    render(<SecurityAccessSettings />);

    expect(screen.getByText("Restart required")).toBeInTheDocument();
    expect(
      screen.getByText("The running rate limiter is not using the saved backend."),
    ).toBeInTheDocument();
  });

  it("leaves the limiter banner to the shell when a server restart is already owed", () => {
    rateLimitConfigMock.mockReturnValue({
      data: { ...SERVER_CONFIG, backend: "redis", active_backend: "memory" },
      isLoading: false,
    });
    serverStatusMock.mockReturnValue({ data: { restart_required: true } });

    render(<SecurityAccessSettings />);

    // Two fixed bottom banners would sit on top of each other.
    expect(screen.queryByText("Restart required")).not.toBeInTheDocument();
  });
});
