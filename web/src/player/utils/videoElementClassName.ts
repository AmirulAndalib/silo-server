/**
 * Computes the className for the player's `<video>` element.
 *
 * `object-contain` is load-bearing, not cosmetic: replaced elements default to
 * `object-fit: fill`, which stretches the decoded frame to the element box and
 * distorts the picture whenever the player box is not the video's aspect ratio.
 * The subtitle systems all anchor to the *contained* video area —
 * `useSubtitlePositionStyle` (VTT), libpgs (`aspectRatio: "contain"`), and
 * JASSUB's contain-based canvas geometry — so a stretched video makes the video
 * and its subtitles render skewed relative to each other (issue #209). Keeping
 * the video on `object-fit: contain` matches that shared assumption.
 *
 * @param isDetached - true for the floating/mini player (no absolute fill), false
 *   for the inline/fullscreen player that fills its positioned container.
 */
export function videoElementClassName(isDetached: boolean): string {
  return isDetached
    ? "h-full w-full object-contain"
    : "absolute inset-0 h-full w-full object-contain";
}
