export type HLSEngineV3 = "native" | "hlsjs" | "unsupported";

interface HLSJSSupportV3 {
  isSupported(): boolean;
}

export type ResolvedHLSEngineV3<T extends HLSJSSupportV3> =
  | { engine: "native" | "unsupported" }
  | { engine: "hlsjs"; hlsjs: T };

function nativeHDRPreferred(dynamicRange: string | undefined, nativeSupported: boolean): boolean {
  return nativeSupported && (dynamicRange === "dolby_vision" || dynamicRange === "hdr10");
}

export function selectHLSEngineV3(
  dynamicRange: string | undefined,
  nativeSupported: boolean,
  hlsJSSupported: boolean,
): HLSEngineV3 {
  if (nativeHDRPreferred(dynamicRange, nativeSupported)) return "native";
  if (hlsJSSupported) return "hlsjs";
  if (nativeSupported) return "native";
  return "unsupported";
}

export async function resolveHLSEngineV3<T extends HLSJSSupportV3>(
  dynamicRange: string | undefined,
  nativeSupported: boolean,
  loadHLSJS: () => Promise<T>,
): Promise<ResolvedHLSEngineV3<T>> {
  if (nativeHDRPreferred(dynamicRange, nativeSupported)) {
    return { engine: "native" };
  }

  try {
    const hlsjs = await loadHLSJS();
    const engine = selectHLSEngineV3(dynamicRange, nativeSupported, hlsjs.isSupported());
    return engine === "hlsjs" ? { engine, hlsjs } : { engine };
  } catch (error) {
    if (nativeSupported) return { engine: "native" };
    throw error;
  }
}
