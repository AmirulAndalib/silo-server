import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { SettingsOverviewModel } from "@/hooks/admin/useSettingsOverview";
import SettingsOverview from "./SettingsOverview";

const mocks = vi.hoisted(() => ({
  useSettingsOverview: vi.fn(),
}));

vi.mock("@/hooks/admin/useSettingsOverview", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/hooks/admin/useSettingsOverview")>()),
  useSettingsOverview: () => mocks.useSettingsOverview(),
}));

function model(overrides: Partial<SettingsOverviewModel> = {}): SettingsOverviewModel {
  return {
    isLoading: false,
    tiles: [
      {
        id: "storage",
        label: "Storage",
        state: "ok",
        stateText: "Healthy",
        detail: "S3 · public + private",
      },
      {
        id: "transcoding",
        label: "Transcoding",
        state: "warn",
        stateText: "Restart pending",
        detail: "Saved changes apply after a restart",
        action: { label: "Fix", tab: "playback" },
      },
      {
        id: "email",
        label: "Email",
        state: "off",
        stateText: "Not set up",
        detail: "Invites and resets can't send",
        action: { label: "Set up", tab: "notifications" },
      },
    ],
    cards: [
      {
        id: "playback",
        title: "Playback",
        summary: "Transcoding on · VA-API",
        attention: false,
        inactive: false,
      },
      {
        id: "notifications",
        title: "Notifications",
        summary: "Email channel has no SMTP",
        attention: true,
        inactive: false,
      },
      {
        id: "watch-sync",
        title: "Watch sync",
        summary: "Trakt connected",
        attention: false,
        inactive: false,
      },
    ],
    sectionStatus: {} as SettingsOverviewModel["sectionStatus"],
    ...overrides,
  };
}

function renderOverview() {
  return render(
    <MemoryRouter initialEntries={["/admin/settings"]}>
      <SettingsOverview />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  mocks.useSettingsOverview.mockReturnValue(model());
});

describe("SettingsOverview", () => {
  it("renders the page heading and nothing else above the content", () => {
    renderOverview();

    expect(screen.getByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
    expect(
      screen.getByText(/Configure the server, media processing, integrations/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 2, name: "Settings categories" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
  });

  it("shows only the health tiles that need something, with their action", () => {
    renderOverview();

    expect(screen.queryByTestId("overview-tile-storage")).not.toBeInTheDocument();

    const transcoding = screen.getByTestId("overview-tile-transcoding");
    expect(transcoding).toHaveAttribute("data-state", "warn");
    expect(within(transcoding).getByText("Restart pending")).toBeInTheDocument();
    expect(within(transcoding).getByRole("link")).toHaveAttribute(
      "href",
      "/admin/settings?tab=playback",
    );

    const email = screen.getByTestId("overview-tile-email");
    expect(within(email).getByText("Set up")).toBeInTheDocument();
  });

  it("says so in one line when nothing needs attention", () => {
    mocks.useSettingsOverview.mockReturnValue(
      model({ tiles: model().tiles.filter((tile) => tile.state === "ok") }),
    );
    renderOverview();

    expect(screen.getByText("Everything is configured.")).toBeInTheDocument();
    expect(screen.queryByTestId("overview-tile-storage")).not.toBeInTheDocument();
  });

  it("renders one section card per entry with a single summary line", () => {
    renderOverview();

    const playback = screen.getByTestId("overview-card-playback");
    expect(playback).toHaveAttribute("href", "/admin/settings?tab=playback");
    expect(within(playback).getByText("Playback")).toBeInTheDocument();
    expect(
      within(playback).getByText(
        "Transcoding, hardware acceleration, watch thresholds, and downloads.",
      ),
    ).toBeInTheDocument();
    expect(within(playback).getByText("Current")).toBeInTheDocument();
    expect(within(playback).getByText("Transcoding on · VA-API")).toBeInTheDocument();
    expect(playback).not.toHaveAttribute("data-attention");

    expect(screen.getByTestId("overview-card-watch-sync")).toHaveAttribute(
      "href",
      "/admin/settings?tab=watch-sync",
    );
  });

  it("marks a card that needs attention", () => {
    renderOverview();

    const notifications = screen.getByTestId("overview-card-notifications");
    expect(notifications).toHaveAttribute("data-attention", "true");
    expect(within(notifications).getByText("Email channel has no SMTP").className).toContain(
      "amber",
    );
  });

  it("shows skeletons instead of tiles and cards on first load", () => {
    mocks.useSettingsOverview.mockReturnValue(model({ isLoading: true }));
    renderOverview();

    expect(screen.queryByTestId("overview-tile-transcoding")).not.toBeInTheDocument();
    expect(screen.queryByTestId("overview-card-playback")).not.toBeInTheDocument();
    expect(screen.queryByText("Everything is configured.")).not.toBeInTheDocument();
  });
});
