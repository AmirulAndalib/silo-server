import { useSettingsOverview } from "@/hooks/admin/useSettingsOverview";
import { HealthTile, HealthTileSkeleton, SectionCard, SectionCardSkeleton } from "./overviewCards";

const SKELETON_TILES = [0, 1];
const SKELETON_CARDS = [0, 1, 2, 3, 4, 5];

/**
 * The settings landing page: whatever needs the admin across the top, then one
 * row per settings section saying what it is doing right now. Mounted when no
 * `?tab=` is present.
 */
export default function SettingsOverview() {
  const { isLoading, tiles, cards } = useSettingsOverview();

  // A healthy server has nothing to show here, and says so in one line rather
  // than in a wall of green tiles.
  const attentionTiles = tiles.filter((tile) => tile.state === "warn" || tile.state === "off");

  return (
    <div className="space-y-8">
      <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">Settings</h1>

      <section aria-label="Server health">
        {isLoading ? (
          <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
            {SKELETON_TILES.map((index) => (
              <HealthTileSkeleton key={index} />
            ))}
          </div>
        ) : attentionTiles.length === 0 ? (
          <p className="text-muted-foreground text-sm">Everything is configured.</p>
        ) : (
          <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
            {attentionTiles.map((tile) => (
              <HealthTile key={tile.id} tile={tile} />
            ))}
          </div>
        )}
      </section>

      <section aria-label="Settings sections">
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
          {isLoading
            ? SKELETON_CARDS.map((index) => <SectionCardSkeleton key={index} />)
            : cards.map((card) => <SectionCard key={card.id} card={card} />)}
        </div>
      </section>
    </div>
  );
}
