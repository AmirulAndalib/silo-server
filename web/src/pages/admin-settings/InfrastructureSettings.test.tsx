import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import InfrastructureSettings from "./InfrastructureSettings";
import { OPSLOG_BUCKET_POLICIES_KEY } from "./logRetentionPolicy";

const useSettingsFormMock = vi.fn();
const useCheckAdminSettingsConnectionMock = vi.fn();

vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: (...args: unknown[]) => useSettingsFormMock(...args),
}));

vi.mock("@/hooks/useRestartKeys", () => ({
  useRestartKeys: () => new Set<string>(["redis.url"]),
}));

vi.mock("@/hooks/queries/admin/settings", () => ({
  useCheckAdminSettingsConnection: (...args: unknown[]) =>
    useCheckAdminSettingsConnectionMock(...args),
}));

useCheckAdminSettingsConnectionMock.mockReturnValue({ isPending: false, mutateAsync: vi.fn() });

type FormOverrides = Partial<Record<string, unknown>>;

function mockForm(overrides: FormOverrides = {}) {
  const form = {
    isLoading: false,
    getValue: (key: string) => (key === "s3.public_url_auth" ? "presigned" : ""),
    setValue: vi.fn(),
    resetValue: vi.fn(),
    dirtyCount: 0,
    dirtyKeys: [],
    isDirty: () => false,
    save: vi.fn(),
    discard: vi.fn(),
    isSaving: false,
    restartRequired: false,
    sensitiveConfigured: [],
    sensitiveManagedByEnv: [],
    sensitiveStatusReady: true,
    sensitiveStatusError: false,
    buildConnectionCheckRequest: vi.fn(),
    ...overrides,
  };
  useSettingsFormMock.mockReturnValue(form);
  return form;
}

describe("InfrastructureSettings", () => {
  it("renders every field group heading", () => {
    mockForm();

    const markup = renderToStaticMarkup(<InfrastructureSettings />);

    for (const heading of [
      "Redis",
      "Public storage",
      "Private storage",
      "Database",
      "Server logs",
    ]) {
      expect(markup).toContain(heading);
    }
  });

  it("manages the merged database, storage and log keys in one form", () => {
    mockForm();

    renderToStaticMarkup(<InfrastructureSettings />);

    const calls = useSettingsFormMock.mock.calls as [{ keys: string[] }][];
    const keys = calls[calls.length - 1]?.[0].keys ?? [];
    expect(keys).toEqual(expect.arrayContaining(["redis.url", "database.max_connections"]));
    expect(keys).toEqual(
      expect.arrayContaining(["s3.public_bucket", "s3.private_bucket", OPSLOG_BUCKET_POLICIES_KEY]),
    );
    // The disabled Litestream storage tab is gone; its keys keep working through the API.
    expect(keys.filter((key) => key.startsWith("s3.user_db_"))).toEqual([]);
  });

  it("shows only essential controls until Advanced is opened", () => {
    mockForm();

    const markup = renderToStaticMarkup(<InfrastructureSettings />);

    expect(markup).toContain("Use Redis");
    expect(markup).toContain("Endpoint");
    expect(markup).toContain("Bucket");
    expect(markup).toContain("Check Connection");
    // Advanced, so not rendered while collapsed.
    expect(markup).not.toContain("Region");
    expect(markup).not.toContain("Maximum Postgres connections");
    expect(markup).not.toContain("Maximum log entries");
    // Removed entirely.
    expect(markup).not.toContain("User DB");
    expect(markup).not.toContain("Not currently in use");
  });

  it("opens an Advanced section while one of its fields is unsaved", () => {
    mockForm({ isDirty: (key: string) => key === "database.max_connections", dirtyCount: 1 });

    const markup = renderToStaticMarkup(<InfrastructureSettings />);

    expect(markup).toContain("Maximum Postgres connections");
  });

  it("warns about the artwork cache when a public storage identity field is edited", () => {
    mockForm({ isDirty: (key: string) => key === "s3.public_bucket", dirtyCount: 1 });

    const markup = renderToStaticMarkup(<InfrastructureSettings />);

    expect(markup).toContain("Storage location change");
    expect(markup).toContain("will not change artwork cache records");
  });

  it("requires an explicit action before replacing a configured credential", async () => {
    const form = mockForm({
      sensitiveConfigured: ["s3.public_access_key", "s3.public_secret_key"],
      dirtyCount: 1,
    });

    render(<InfrastructureSettings />);

    const publicGroup = within(screen.getByRole("group", { name: "Public storage" }));
    expect(publicGroup.queryByLabelText("Access Key")).not.toBeInTheDocument();
    await userEvent.click(publicGroup.getByRole("button", { name: "Replace Access Key" }));
    expect(publicGroup.getByLabelText("Access Key")).toHaveAttribute("type", "password");

    await userEvent.click(publicGroup.getByRole("button", { name: "Keep saved Access Key" }));
    expect(form.resetValue).toHaveBeenCalledWith("s3.public_access_key");
    expect(publicGroup.queryByLabelText("Access Key")).not.toBeInTheDocument();
  });

  it("keeps a credential replacement open when saving fails", async () => {
    mockForm({
      sensitiveConfigured: ["s3.private_access_key"],
      dirtyCount: 1,
      save: vi.fn().mockRejectedValue(new Error("save failed")),
    });

    render(<InfrastructureSettings />);

    const privateGroup = within(screen.getByRole("group", { name: "Private storage" }));
    await userEvent.click(privateGroup.getByRole("button", { name: "Replace Access Key" }));
    await userEvent.click(screen.getByRole("button", { name: "Save Changes" }));

    await waitFor(() =>
      expect(privateGroup.getByLabelText("Access Key")).toHaveAttribute("type", "password"),
    );
  });

  it("fails closed when protected credential status cannot be loaded", () => {
    mockForm({ sensitiveStatusReady: false, sensitiveStatusError: true });

    render(<InfrastructureSettings />);

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Protected credential status is unavailable",
    );
    expect(screen.queryByLabelText("Access Key")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Secret Key")).not.toBeInTheDocument();
  });

  it("stages bucket override edits through the shared save model", async () => {
    const setValue = vi.fn();
    mockForm({
      setValue,
      isDirty: (key: string) => key === OPSLOG_BUCKET_POLICIES_KEY,
      dirtyCount: 1,
      getValue: (key: string) => {
        if (key === "s3.public_url_auth") return "presigned";
        if (key === OPSLOG_BUCKET_POLICIES_KEY) {
          return JSON.stringify([
            {
              component: "metadata",
              level: "info",
              retention_days: 1,
              max_rows: 100,
              max_size_mb: 8,
            },
          ]);
        }
        return "";
      },
    });

    render(<InfrastructureSettings />);

    await userEvent.click(screen.getByRole("button", { name: /Remove metadata rule/ }));

    expect(setValue).toHaveBeenCalledWith(OPSLOG_BUCKET_POLICIES_KEY, "[]");
  });
});
