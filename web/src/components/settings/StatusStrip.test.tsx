import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { StatusStrip } from "@/components/settings/StatusStrip";

describe("StatusStrip", () => {
  it("renders every phrase with its tone and the trailing aside", () => {
    render(
      <StatusStrip
        items={[
          { tone: "ok", label: "Transcoding on" },
          { tone: "info", label: "2 transcode nodes online" },
          { tone: "warn", label: "Restart pending" },
          { tone: "muted", label: "No nodes configured" },
        ]}
        trailing="Saved 4 minutes ago"
      />,
    );

    expect(screen.getByText("Transcoding on").closest("[data-tone]")).toHaveAttribute(
      "data-tone",
      "ok",
    );
    expect(screen.getByText("2 transcode nodes online").closest("[data-tone]")).toHaveAttribute(
      "data-tone",
      "info",
    );
    expect(screen.getByText("Restart pending").closest("[data-tone]")).toHaveAttribute(
      "data-tone",
      "warn",
    );
    expect(screen.getByText("No nodes configured").closest("[data-tone]")).toHaveAttribute(
      "data-tone",
      "muted",
    );
    expect(screen.getByText("Saved 4 minutes ago")).toBeInTheDocument();
  });

  it("renders nothing when there is nothing to report", () => {
    const { container } = render(<StatusStrip items={[]} />);

    expect(container).toBeEmptyDOMElement();
  });

  it("still renders for a trailing aside alone", () => {
    render(<StatusStrip items={[]} trailing="Last check 2 min ago" />);

    expect(screen.getByText("Last check 2 min ago")).toBeInTheDocument();
  });
});
