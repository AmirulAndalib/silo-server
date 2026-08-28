import { act, render, screen } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useGridLayout } from "./useGridLayout";

let notifyResize: ResizeObserverCallback | undefined;
let containerWidth = 400;

class ResizeObserverStub {
  constructor(callback: ResizeObserverCallback) {
    notifyResize = callback;
  }

  observe() {}
  disconnect() {}
}

function Harness() {
  const mounted = useRef(false);
  const { containerRef, layout } = useGridLayout({ gap: 10, textAreaHeight: 20 });

  return (
    <div>
      <output>{`${layout.columnCount}:${layout.rowHeight}`}</output>
      <div
        ref={(element) => {
          containerRef.current = element;
          if (element && !mounted.current) {
            Object.defineProperty(element, "clientWidth", {
              configurable: true,
              get: () => containerWidth,
            });
            mounted.current = true;
          }
        }}
        style={{ display: "grid", gridTemplateColumns: "100px 100px" }}
      />
    </div>
  );
}

describe("useGridLayout", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal("ResizeObserver", ResizeObserverStub);
    containerWidth = 400;
    notifyResize = undefined;
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("tracks the first resize frame immediately, then coalesces the rest", () => {
    render(<Harness />);
    expect(screen.getByRole("status")).toHaveTextContent("2:322.5");

    // The virtualizer positions absolute rows from this geometry, so deferring
    // the first frame would leave rows overlapping the already-reflowed grid
    // for the whole settle window.
    containerWidth = 360;
    act(() => notifyResize?.([], {} as ResizeObserver));
    expect(screen.getByRole("status")).toHaveTextContent("2:292.5");
  });

  it("reconciles once more after a continuous resize settles", () => {
    render(<Harness />);

    containerWidth = 360;
    act(() => notifyResize?.([], {} as ResizeObserver));
    expect(screen.getByRole("status")).toHaveTextContent("2:292.5");

    // Intermediate frames of the same gesture do not each schedule a render;
    // they collapse into one trailing reconcile at the final width.
    containerWidth = 300;
    act(() => {
      notifyResize?.([], {} as ResizeObserver);
      vi.advanceTimersByTime(80);
      notifyResize?.([], {} as ResizeObserver);
      vi.advanceTimersByTime(119);
    });
    expect(screen.getByRole("status")).toHaveTextContent("2:292.5");

    act(() => vi.advanceTimersByTime(1));
    expect(screen.getByRole("status")).toHaveTextContent("2:247.5");
  });
});
