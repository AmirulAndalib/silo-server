import { describe, expect, it } from "vitest";

import { selectHLSEngineV3 } from "./hlsEngine";

describe("selectHLSEngineV3", () => {
  it.each(["dolby_vision", "hdr10"])(
    "prefers native HLS for %s when both engines are available",
    (dynamicRange) => {
      expect(selectHLSEngineV3(dynamicRange, true, true)).toBe("native");
    },
  );

  it("keeps hls.js first for SDR", () => {
    expect(selectHLSEngineV3("sdr", true, true)).toBe("hlsjs");
  });

  it("falls back to native when hls.js is unavailable", () => {
    expect(selectHLSEngineV3("sdr", true, false)).toBe("native");
  });

  it("rejects an unavailable transport", () => {
    expect(selectHLSEngineV3("dolby_vision", false, false)).toBe("unsupported");
  });
});
