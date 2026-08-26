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

    const input = screen.getByLabelText("Secret key");
    expect(input).toHaveAttribute("type", "password");
    expect(input).toHaveAttribute("placeholder", "Not configured");
    await user.type(input, "abc");
    expect(onChange).toHaveBeenLastCalledWith("abc");
  });

  it("shows a saved secret as a masked, editable input", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness configured onChange={onChange} />);

    // No Replace step: the input is live and the mask stands in for the value.
    const input = screen.getByLabelText("Secret key");
    expect(input).toHaveAttribute("type", "password");
    expect(input).toHaveAttribute("placeholder", "••••••••••••");
    expect(
      screen.getByText("Type to replace the saved value; leave blank to keep it."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Replace/ })).not.toBeInTheDocument();

    await user.type(input, "new-secret");
    expect(onChange).toHaveBeenLastCalledWith("new-secret");
  });

  it("delegates emptying the input to onKeep so the parent's draft stays authoritative", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const onKeep = vi.fn();
    render(<Harness configured onChange={onChange} onKeep={onKeep} />);

    const input = screen.getByLabelText("Secret key");
    await user.type(input, "x");
    onChange.mockClear();

    // Deleting back to empty means "keep the saved secret" — the parent
    // reverts its draft instead of staging "" (which would clear on save).
    await user.clear(input);
    expect(onKeep).toHaveBeenCalled();
    expect(onChange).not.toHaveBeenCalledWith("");
  });

  it("clears its own draft when no onKeep is supplied", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness configured onChange={onChange} />);

    const input = screen.getByLabelText("Secret key");
    await user.type(input, "x");
    onChange.mockClear();

    await user.clear(input);
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("does not treat an empty input as a keep while nothing is saved", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const onKeep = vi.fn();
    render(<Harness configured={false} onChange={onChange} onKeep={onKeep} />);

    const input = screen.getByLabelText("Secret key");
    await user.type(input, "x");
    await user.clear(input);
    expect(onKeep).not.toHaveBeenCalled();
    expect(onChange).toHaveBeenCalledWith("");
  });
});
