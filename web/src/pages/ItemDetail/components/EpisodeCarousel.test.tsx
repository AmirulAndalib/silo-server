import { renderToStaticMarkup } from "react-dom/server";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import EpisodeCarousel from "./EpisodeCarousel";

const capturedMenuProps: Record<string, unknown>[] = [];
const prefetchEpisodeDetail = vi.hoisted(() => vi.fn());

vi.mock("@/hooks/queries/catalogRead", () => ({
  usePrefetchCatalogItemDetail: () => prefetchEpisodeDetail,
}));

vi.mock("@/components/MediaItemMenu", () => ({
  default: (props: Record<string, unknown>) => {
    capturedMenuProps.push(props);
    return <div />;
  },
}));

vi.mock("@/hooks/useCarouselEmbla", () => ({
  useCarouselEmbla: () => ({
    emblaApi: null,
    emblaRef: { current: null },
    canScrollPrev: false,
    canScrollNext: false,
    scrollPrev: () => {},
    scrollNext: () => {},
  }),
}));

describe("EpisodeCarousel", () => {
  it("prefetches episode details when a card shows navigation intent", async () => {
    prefetchEpisodeDetail.mockClear();

    render(
      <MemoryRouter>
        <EpisodeCarousel
          currentEpisodeNumber={2}
          episodes={[
            {
              content_id: "ep-1",
              season_number: 1,
              episode_number: 1,
              title: "Pilot",
              overview: "",
              air_date: null,
              runtime: 42,
              still_url: "",
              still_thumbhash: "",
              files: [],
            },
          ]}
        />
      </MemoryRouter>,
    );

    await userEvent.hover(screen.getAllByRole("link", { name: /Pilot/ })[0]!);

    expect(prefetchEpisodeDetail).toHaveBeenCalledWith("ep-1");
  });

  it("passes partial-progress restart eligibility to episode menus", () => {
    capturedMenuProps.length = 0;

    renderToStaticMarkup(
      <MemoryRouter>
        <EpisodeCarousel
          currentEpisodeNumber={1}
          episodes={[
            {
              content_id: "ep-1",
              season_number: 1,
              episode_number: 1,
              title: "Pilot",
              overview: "",
              air_date: null,
              runtime: 42,
              still_url: "",
              still_thumbhash: "",
              files: [],
              user_data: {
                played: false,
                position_seconds: 120,
                duration_seconds: 1800,
              },
            },
          ]}
        />
      </MemoryRouter>,
    );

    expect(capturedMenuProps[0]).toMatchObject({
      contentId: "ep-1",
      mediaType: "episode",
      hasPartialProgress: true,
    });
  });
});
