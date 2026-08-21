import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { SettingField } from "./SettingField";

vi.mock("@/components/ui/select", () => ({
  Select: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SelectContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SelectItem: ({ children, disabled }: { children: ReactNode; disabled?: boolean }) => (
    <button disabled={disabled}>{children}</button>
  ),
  SelectTrigger: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  SelectValue: () => null,
}));

describe("SettingField", () => {
  it("renders unavailable select options as disabled", () => {
    render(
      <SettingField
        label="User DB Backend"
        type="select"
        options={[
          { value: "postgres", label: "PostgreSQL" },
          { value: "sqlite", label: "SQLite (TBD)", disabled: true },
        ]}
        value="postgres"
        onChange={vi.fn()}
      />,
    );

    expect(screen.getByRole("button", { name: "PostgreSQL" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "SQLite (TBD)" })).toBeDisabled();
  });

  it("shows the restart and default chips after the label", () => {
    render(
      <SettingField
        label="FFmpeg path"
        value="/usr/bin/ffmpeg"
        onChange={vi.fn()}
        restartRequired
        isDefault
      />,
    );

    expect(screen.getByLabelText("Takes effect after a server restart")).toBeInTheDocument();
    expect(screen.getByText("Default")).toBeInTheDocument();
  });

  it("omits both chips by default", () => {
    render(<SettingField label="FFmpeg path" value="" onChange={vi.fn()} />);

    expect(screen.queryByLabelText("Takes effect after a server restart")).not.toBeInTheDocument();
    expect(screen.queryByText("Default")).not.toBeInTheDocument();
  });

  it("marks an edited row as dirty for the accent rail", () => {
    const { container, rerender } = render(
      <SettingField label="Server name" value="Silo" onChange={vi.fn()} />,
    );
    expect(container.querySelector('[data-dirty="true"]')).toBeNull();

    rerender(<SettingField label="Server name" value="Silo 2" onChange={vi.fn()} dirty />);
    expect(container.querySelector('[data-dirty="true"]')).not.toBeNull();
  });

  it("renders a status line under the description", () => {
    render(
      <SettingField
        label="Hardware acceleration"
        type="toggle"
        value="true"
        onChange={vi.fn()}
        description="Offload video encoding to the GPU."
        status={<span>Detected VA-API on renderD128</span>}
      />,
    );

    expect(screen.getByText("Offload video encoding to the GPU.")).toBeInTheDocument();
    expect(screen.getByText("Detected VA-API on renderD128")).toBeInTheDocument();
  });

  it("keeps describing the control with its description", () => {
    render(
      <SettingField
        label="Mark watched at"
        type="number"
        value="90"
        onChange={vi.fn()}
        hint="Percent of runtime before Silo marks an item finished."
      />,
    );

    const input = screen.getByLabelText("Mark watched at");
    const describedBy = input.getAttribute("aria-describedby");
    expect(describedBy).toBeTruthy();
    expect(document.getElementById(describedBy!)).toHaveTextContent(
      "Percent of runtime before Silo marks an item finished.",
    );
  });
});
