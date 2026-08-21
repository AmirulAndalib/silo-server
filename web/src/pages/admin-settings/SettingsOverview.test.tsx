import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router";
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
        action: { label: "Review", tab: "infrastructure" },
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
        attention: false,
        rows: [
          { label: "Transcoding", value: "On · VA-API", tone: "ok" },
          { label: "Downloads", value: "On · 50 Mbps" },
        ],
      },
      {
        id: "notifications",
        title: "Notifications",
        attention: true,
        rows: [
          { label: "Email", value: "No SMTP", tone: "warn" },
          { label: "Discord", value: "1 webhook", tone: "ok" },
        ],
      },
      {
        id: "watch-sync",
        title: "Watch sync",
        attention: false,
        rows: [{ label: "Trakt", value: "Connected", tone: "ok" }],
      },
    ],
    sectionStatus: {} as SettingsOverviewModel["sectionStatus"],
    attentionCount: 2,
    ...overrides,
  };
}

/** Mirrors the router location so navigation is observable in the DOM. */
function LocationProbe() {
  const location = useLocation();
  return <div data-testid="location">{`${location.pathname}${location.search}`}</div>;
}

function renderOverview() {
  return render(
    <MemoryRouter initialEntries={["/admin/settings"]}>
      <SettingsOverview />
      <LocationProbe />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  mocks.useSettingsOverview.mockReturnValue(model());
});

describe("SettingsOverview", () => {
  it("renders the page heading and lede", () => {
    renderOverview();

    expect(screen.getByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
    expect(
      screen.getByText(
        "Everything about this server, with its current state on the surface. Open a card to change it.",
      ),
    ).toBeInTheDocument();
  });

  it("renders a health tile per model entry with its state and action link", () => {
    renderOverview();

    const storage = screen.getByTestId("overview-tile-storage");
    expect(within(storage).getByText("Storage")).toBeInTheDocument();
    expect(within(storage).getByText("Healthy")).toBeInTheDocument();
    expect(within(storage).getByText("S3 · public + private")).toBeInTheDocument();
    expect(within(storage).getByRole("link")).toHaveAttribute(
      "href",
      "/admin/settings?tab=infrastructure",
    );

    const transcoding = screen.getByTestId("overview-tile-transcoding");
    expect(transcoding).toHaveAttribute("data-state", "warn");
    expect(within(transcoding).getByText("Fix")).toBeInTheDocument();
    expect(within(transcoding).getByRole("link")).toHaveAttribute(
      "href",
      "/admin/settings?tab=playback",
    );
  });

  it("counts the things needing attention in the strip caption", () => {
    renderOverview();

    expect(screen.getByText("Server health & setup")).toBeInTheDocument();
    expect(screen.getByText("2 things need you")).toBeInTheDocument();
  });

  it("renders a section card per model entry, linking to its tab", () => {
    renderOverview();

    const playback = screen.getByTestId("overview-card-playback");
    expect(playback).toHaveAttribute("href", "/admin/settings?tab=playback");
    expect(within(playback).getByText("Playback")).toBeInTheDocument();
    expect(within(playback).getByText("On · VA-API")).toBeInTheDocument();
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
    expect(notifications.className).toContain("amber");
    expect(within(notifications).getByText("No SMTP")).toBeInTheDocument();
  });

  it("shows skeletons instead of tiles and cards on first load", () => {
    mocks.useSettingsOverview.mockReturnValue(model({ isLoading: true }));
    renderOverview();

    expect(screen.queryByTestId("overview-tile-storage")).not.toBeInTheDocument();
    expect(screen.queryByTestId("overview-card-playback")).not.toBeInTheDocument();
  });

  it("filters the cards to what the query matches", async () => {
    const user = userEvent.setup();
    renderOverview();

    await user.type(screen.getByRole("searchbox", { name: "Search settings" }), "trakt");

    expect(screen.getByTestId("overview-card-watch-sync")).toBeInTheDocument();
    expect(screen.queryByTestId("overview-card-playback")).not.toBeInTheDocument();
    // The health strip steps aside so the matches are the whole page.
    expect(screen.queryByTestId("overview-tile-storage")).not.toBeInTheDocument();
  });

  it("opens the best matching tab on Enter", async () => {
    const user = userEvent.setup();
    renderOverview();

    const input = screen.getByRole("searchbox", { name: "Search settings" });
    await user.type(input, "smtp{Enter}");

    expect(screen.getByTestId("location")).toHaveTextContent("/admin/settings?tab=notifications");
  });

  it("falls back to the settings keyword index when no card matches", async () => {
    const user = userEvent.setup();
    renderOverview();

    await user.type(screen.getByRole("searchbox", { name: "Search settings" }), "ffmpeg{Enter}");

    expect(screen.getByTestId("location")).toHaveTextContent("/admin/settings?tab=playback");
  });
});
