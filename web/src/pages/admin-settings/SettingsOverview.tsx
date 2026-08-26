import { CircleCheck } from "lucide-react";

import { useSettingsOverview } from "@/hooks/admin/useSettingsOverview";
import { HealthTile, HealthTileSkeleton, SectionCard, SectionCardSkeleton } from "./overviewCards";

const SKELETON_TILES = [0, 1];
const SKELETON_CARDS = [0, 1, 2, 3, 4, 5];

/**
 * The settings landing page: whatever needs the admin across the top, then one
 * directory of settings categories. Mounted when no `?tab=` is present.
 */
export default function SettingsOverview() {
  const { isLoading, tiles, cards } = useSettingsOverview();

  // A healthy server has nothing to show here, and says so in one line rather
  // than in a wall of green tiles.
  const attentionTiles = tiles.filter((tile) => tile.state === "warn" || tile.state === "off");

  return (
    <div className="w-full space-y-10">
      <header className="max-w-3xl space-y-3">
        <h1 className="page-title text-[clamp(2.25rem,4vw,3.5rem)]">Settings</h1>
        <p className="text-muted-foreground text-sm leading-relaxed sm:text-base">
          Configure the server, media processing, integrations, and the defaults your household
          starts with.
        </p>
      </header>

      <section aria-label="Server health">
        {isLoading ? (
          <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
            {SKELETON_TILES.map((index) => (
              <HealthTileSkeleton key={index} />
            ))}
          </div>
        ) : attentionTiles.length === 0 ? (
          <div className="border-border/60 bg-card/35 inline-flex items-center gap-2 rounded-xl border px-3.5 py-2.5">
            <CircleCheck className="size-4 text-emerald-500" aria-hidden="true" />
            <p className="text-sm font-medium">Everything is configured.</p>
          </div>
        ) : (
          <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
            {attentionTiles.map((tile) => (
              <HealthTile key={tile.id} tile={tile} />
            ))}
          </div>
        )}
      </section>

      <section aria-labelledby="settings-groups-heading" className="space-y-5">
        <div className="space-y-1.5">
          <h2 id="settings-groups-heading" className="text-xl font-semibold tracking-tight">
            Settings groups
          </h2>
          <p className="text-muted-foreground text-sm">
            Each group shows the sections you’ll find inside.
          </p>
        </div>
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          {isLoading
            ? SKELETON_CARDS.map((index) => <SectionCardSkeleton key={index} />)
            : cards.map((card) => <SectionCard key={card.id} card={card} />)}
        </div>
      </section>
    </div>
  );
}
