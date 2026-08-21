import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { SecretField } from "@/components/settings/SecretField";

function Harness({
  configured,
  onChange,
  onKeep,
}: {
  configured: boolean;
  onChange?: (value: string) => void;
  onKeep?: () => void;
}) {
  const [value, setValue] = useState("");
  return (
    <SecretField
      label="Secret key"
      value={value}
      configured={configured}
      onChange={(next) => {
        setValue(next);
        onChange?.(next);
      }}
      onKeep={onKeep}
    />
  );
}

describe("SecretField", () => {
  it("shows a password input when nothing is saved", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness configured={false} onChange={onChange} />);

    expect(screen.queryByText("Configured")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Keep saved Secret key" })).not.toBeInTheDocument();

    const input = screen.getByLabelText("Secret key");
    expect(input).toHaveAttribute("type", "password");
    await user.type(input, "abc");
    expect(onChange).toHaveBeenLastCalledWith("abc");
  });

  it("summarises a saved secret behind a Replace button", () => {
    render(<Harness configured />);

    expect(screen.getByText("Configured")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Replace Secret key" })).toBeInTheDocument();
    expect(screen.queryByLabelText("Secret key")).not.toBeInTheDocument();
  });

  it("reveals an input with Keep saved value after Replace", async () => {
    const user = userEvent.setup();
    render(<Harness configured />);

    await user.click(screen.getByRole("button", { name: "Replace Secret key" }));

    const input = screen.getByLabelText("Secret key");
    expect(input).toHaveAttribute("type", "password");
    expect(screen.getByText("Enter a replacement value.")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Keep saved Secret key" }));
    expect(screen.getByText("Configured")).toBeInTheDocument();
    expect(screen.queryByLabelText("Secret key")).not.toBeInTheDocument();
  });

  it("delegates the revert to onKeep so the parent's draft stays authoritative", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const onKeep = vi.fn();
    render(<Harness configured onChange={onChange} onKeep={onKeep} />);

    await user.click(screen.getByRole("button", { name: "Replace Secret key" }));
    await user.type(screen.getByLabelText("Secret key"), "x");
    onChange.mockClear();

    await user.click(screen.getByRole("button", { name: "Keep saved Secret key" }));
    expect(onKeep).toHaveBeenCalledTimes(1);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("clears its own draft when no onKeep is supplied", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness configured onChange={onChange} />);

    await user.click(screen.getByRole("button", { name: "Replace Secret key" }));
    await user.type(screen.getByLabelText("Secret key"), "x");
    onChange.mockClear();

    await user.click(screen.getByRole("button", { name: "Keep saved Secret key" }));
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("follows a controlled editing prop", () => {
    const { rerender } = render(
      <SecretField label="Secret key" value="" configured editing={false} onChange={vi.fn()} />,
    );
    expect(screen.getByText("Configured")).toBeInTheDocument();

    rerender(<SecretField label="Secret key" value="" configured editing onChange={vi.fn()} />);
    expect(screen.getByLabelText("Secret key")).toBeInTheDocument();
  });
});
