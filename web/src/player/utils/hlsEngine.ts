export type HLSEngineV3 = "native" | "hlsjs" | "unsupported";

interface HLSJSSupportV3 {
  isSupported(): boolean;
}

export type ResolvedHLSEngineV3<T extends HLSJSSupportV3> =
  | { engine: "native" | "unsupported" }
  | { engine: "hlsjs"; hlsjs: T };

export function isSafariBrowserV3(userAgent: string): boolean {
  return (
    /Safari\//i.test(userAgent) &&
    !/(?:Chrome|Chromium|CriOS|Edg|EdgiOS|OPR|OPiOS|Firefox|FxiOS)\//i.test(userAgent)
  );
}

function nativeHDRPreferred(
  dynamicRange: string | undefined,
  nativeSupported: boolean,
  preferNativeHDR: boolean,
): boolean {
  return (
    preferNativeHDR &&
    nativeSupported &&
    (dynamicRange === "dolby_vision" || dynamicRange === "hdr10")
  );
}

export function selectHLSEngineV3(
  dynamicRange: string | undefined,
  nativeSupported: boolean,
  hlsJSSupported: boolean,
  preferNativeHDR = false,
): HLSEngineV3 {
  if (nativeHDRPreferred(dynamicRange, nativeSupported, preferNativeHDR)) return "native";
  if (hlsJSSupported) return "hlsjs";
  if (nativeSupported) return "native";
  return "unsupported";
}

export async function resolveHLSEngineV3<T extends HLSJSSupportV3>(
  dynamicRange: string | undefined,
  nativeSupported: boolean,
  loadHLSJS: () => Promise<T>,
  onHLSJSUnavailable?: (error: unknown) => void,
  preferNativeHDR = false,
): Promise<ResolvedHLSEngineV3<T>> {
  if (nativeHDRPreferred(dynamicRange, nativeSupported, preferNativeHDR)) {
    return { engine: "native" };
  }

  try {
    const hlsjs = await loadHLSJS();
    const engine = selectHLSEngineV3(
      dynamicRange,
      nativeSupported,
      hlsjs.isSupported(),
      preferNativeHDR,
    );
    return engine === "hlsjs" ? { engine, hlsjs } : { engine };
  } catch (error) {
    if (nativeSupported) {
      // Falling back to the media element keeps playback alive, but the
      // preferred engine's loss (and its tuned retry/recovery behavior) must
      // not be silent — hand the cause to the caller before degrading.
      onHLSJSUnavailable?.(error);
      return { engine: "native" };
    }
    throw error;
  }
}
