export type HLSEngineV3 = "native" | "hlsjs" | "unsupported";

export function selectHLSEngineV3(
  dynamicRange: string | undefined,
  nativeSupported: boolean,
  hlsJSSupported: boolean,
): HLSEngineV3 {
  const nativeHDR = dynamicRange === "dolby_vision" || dynamicRange === "hdr10";
  if (nativeHDR && nativeSupported) return "native";
  if (hlsJSSupported) return "hlsjs";
  if (nativeSupported) return "native";
  return "unsupported";
}
