import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { RestartBanner, SaveBar } from "./SaveBar";

function renderBar(props: Partial<Parameters<typeof SaveBar>[0]> = {}) {
  return render(
    <SaveBar
      dirtyCount={2}
      onSave={vi.fn()}
      onDiscard={vi.fn()}
      isSaving={false}
      restartRequired={false}
      {...props}
    />,
  );
}

describe("SaveBar", () => {
  it("stays hidden while the tab is clean", () => {
    const { container } = renderBar({ dirtyCount: 0 });

    expect(container).toBeEmptyDOMElement();
  });

  it("counts the staged changes and offers both actions", async () => {
    const onSave = vi.fn();
    const onDiscard = vi.fn();
    renderBar({ dirtyCount: 3, onSave, onDiscard });

    expect(screen.getByText("3 unsaved changes")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Discard" }));
    await userEvent.click(screen.getByRole("button", { name: "Save Changes" }));
    expect(onDiscard).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it("uses the singular form for one change", () => {
    renderBar({ dirtyCount: 1 });

    expect(screen.getByText("1 unsaved change")).toBeInTheDocument();
  });

  it("says how many staged changes need a restart", () => {
    renderBar({ dirtyCount: 4, restartRequired: true, restartCount: 2 });

    expect(screen.getByText("2 changes need a restart")).toBeInTheDocument();
  });

  it("drops the restart line when none of the staged changes need one", () => {
    renderBar({ dirtyCount: 4, restartRequired: true, restartCount: 0 });

    expect(screen.queryByText(/need a restart/)).not.toBeInTheDocument();
  });

  it("falls back to the tab's restart flag when no count is known", () => {
    renderBar({ dirtyCount: 1, restartRequired: true });

    expect(screen.getByText("Some changes need a restart")).toBeInTheDocument();
  });

  it("disables saving while a save is in flight", () => {
    renderBar({ isSaving: true });

    expect(screen.getByRole("button", { name: "Saving..." })).toBeDisabled();
  });

  it("renders no restart prompt of its own", () => {
    renderBar({ dirtyCount: 2, restartRequired: true });

    expect(screen.queryByText("Restart required")).not.toBeInTheDocument();
  });
});

describe("RestartBanner", () => {
  it("stays hidden until a restart is owed", () => {
    const { container } = render(<RestartBanner />);

    expect(container).toBeEmptyDOMElement();
  });

  it("prompts for a restart and can be deferred", async () => {
    render(<RestartBanner restartRequired description="FFmpeg path takes effect on restart." />);

    expect(screen.getByText("Restart required")).toBeInTheDocument();
    expect(screen.getByText("FFmpeg path takes effect on restart.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Restart server/ })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Later" }));
    expect(screen.queryByText("Restart required")).not.toBeInTheDocument();
  });

  it("comes back when a new restart reason arrives", async () => {
    const { rerender } = render(<RestartBanner restartRequired />);

    await userEvent.click(screen.getByRole("button", { name: "Later" }));
    expect(screen.queryByText("Restart required")).not.toBeInTheDocument();

    rerender(<RestartBanner restartRequired={false} />);
    rerender(<RestartBanner restartRequired />);
    expect(screen.getByText("Restart required")).toBeInTheDocument();
  });
});
