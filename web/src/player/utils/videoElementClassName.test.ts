import { describe, expect, it } from "vitest";
import { videoElementClassName } from "./videoElementClassName";

describe("videoElementClassName", () => {
  // object-contain is the fix for issue #209: without it the video defaults to
  // object-fit: fill and stretches, skewing the video and its subtitle overlays
  // (VTT/PGS/ASS all anchor to the contained video area) whenever the player box
  // is not the video's aspect ratio.
  it("applies object-contain in the inline/fullscreen layout", () => {
    const cls = videoElementClassName(false);
    expect(cls).toContain("object-contain");
    expect(cls).toContain("absolute");
    expect(cls).toContain("inset-0");
    expect(cls).toContain("h-full");
    expect(cls).toContain("w-full");
  });

  it("applies object-contain in the detached/floating layout", () => {
    const cls = videoElementClassName(true);
    expect(cls).toContain("object-contain");
    expect(cls).toContain("h-full");
    expect(cls).toContain("w-full");
    // The detached player sizes itself inside an aspect-video container, so it
    // must not pin to the fullscreen frame with absolute inset-0.
    expect(cls).not.toContain("absolute");
    expect(cls).not.toContain("inset-0");
  });
});
