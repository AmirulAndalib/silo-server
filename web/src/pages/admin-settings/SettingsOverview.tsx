import { useMemo, useState, type FormEvent } from "react";
import { Search } from "lucide-react";
import { useNavigate } from "react-router";

import { Input } from "@/components/ui/input";
import { filterSettingsSearchGroups } from "@/components/settings/settingsSearch";
import { ADMIN_SETTINGS_GROUPS } from "@/lib/adminSettingsSearch";
import {
  settingsTabHref,
  useSettingsOverview,
  type OverviewCard,
} from "@/hooks/admin/useSettingsOverview";
import {
  HealthTile,
  HealthTileSkeleton,
  OverviewSectionTitle,
  SectionCard,
  SectionCardSkeleton,
} from "./overviewCards";

const SKELETON_TILES = [0, 1, 2, 3, 4];
const SKELETON_CARDS = [0, 1, 2, 3, 4, 5];

function normalize(value: string) {
  return value
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[^a-z0-9]+/g, " ")
    .trim();
}

/**
 * Matches a card against the query using what the card actually says — its
 * title and its live rows — so "trakt" or "no smtp" finds the section showing
 * it, not just the section whose keyword list happens to mention it.
 */
function cardMatchesQuery(card: OverviewCard, query: string): boolean {
  const tokens = normalize(query).split(/\s+/).filter(Boolean);
  if (tokens.length === 0) return true;
  const haystack = normalize(
    [card.id, card.title, ...card.rows.flatMap((row) => [row.label, row.value])].join(" "),
  );
  const words = haystack.split(/\s+/).filter(Boolean);
  return tokens.every((token) =>
    words.some((word) => word.startsWith(token) || (token.length >= 4 && word.includes(token))),
  );
}

/**
 * The settings landing page: server health across the top, then one card per
 * settings section carrying the two or three values that say what it is doing
 * right now. Mounted when no `?tab=` is present.
 */
export default function SettingsOverview() {
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const { isLoading, tiles, cards, attentionCount } = useSettingsOverview();

  const trimmedQuery = query.trim();
  const visibleCards = useMemo(
    () => (trimmedQuery ? cards.filter((card) => cardMatchesQuery(card, trimmedQuery)) : cards),
    [cards, trimmedQuery],
  );

  // Enter jumps straight to the best section: the first card whose live values
  // match, and failing that the settings index's own keyword search.
  const bestMatch = useMemo(() => {
    if (!trimmedQuery) return null;
    const topCard = visibleCards[0];
    if (topCard) return topCard.id;
    const groups = filterSettingsSearchGroups(ADMIN_SETTINGS_GROUPS, trimmedQuery);
    return groups.flatMap((group) => group.items)[0]?.id ?? null;
  }, [trimmedQuery, visibleCards]);

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (bestMatch) navigate(settingsTabHref(bestMatch));
  }

  const attentionNote =
    attentionCount > 0
      ? `${attentionCount} thing${attentionCount === 1 ? "" : "s"} need${attentionCount === 1 ? "s" : ""} you`
      : "Everything looks healthy";

  return (
    <div className="space-y-7">
      <div className="page-header gap-5">
        <div className="min-w-0 space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">Settings</h1>
          <p className="page-subtitle text-sm sm:text-base">
            Everything about this server, with its current state on the surface. Open a card to
            change it.
          </p>
        </div>
        <form
          role="search"
          onSubmit={handleSubmit}
          className="w-full sm:max-w-sm lg:w-[22rem] lg:max-w-none"
        >
          <label htmlFor="settings-overview-search" className="sr-only">
            Search settings
          </label>
          <div className="relative">
            <Search
              className="text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2"
              aria-hidden="true"
            />
            <Input
              id="settings-overview-search"
              type="search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search settings"
              className="pl-9"
            />
          </div>
        </form>
      </div>

      {trimmedQuery ? null : (
        <section aria-labelledby="settings-overview-health">
          <OverviewSectionTitle title="Server health & setup" note={attentionNote} />
          <span id="settings-overview-health" className="sr-only">
            Server health and setup
          </span>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
            {isLoading
              ? SKELETON_TILES.map((index) => <HealthTileSkeleton key={index} />)
              : tiles.map((tile) => <HealthTile key={tile.id} tile={tile} />)}
          </div>
        </section>
      )}

      <section aria-labelledby="settings-overview-sections">
        <OverviewSectionTitle
          title="Sections"
          note={
            trimmedQuery
              ? `${visibleCards.length} match${visibleCards.length === 1 ? "" : "es"}`
              : `${cards.length} areas`
          }
        />
        <span id="settings-overview-sections" className="sr-only">
          Settings sections
        </span>
        {isLoading ? (
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
            {SKELETON_CARDS.map((index) => (
              <SectionCardSkeleton key={index} />
            ))}
          </div>
        ) : visibleCards.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            No settings section matches “{trimmedQuery}”.
          </p>
        ) : (
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
            {visibleCards.map((card) => (
              <SectionCard key={card.id} card={card} />
            ))}
          </div>
        )}
      </section>
    </div>
  );
}
