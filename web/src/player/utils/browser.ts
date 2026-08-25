export function isFirefoxUserAgent(userAgent: string): boolean {
  return /firefox/i.test(userAgent) && !/seamonkey/i.test(userAgent);
}
