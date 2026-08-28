// @vitest-environment jsdom

import { act, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useNavigate } from "react-router";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { markNavigationDirection, resetNavigationHistory } from "@/lib/navigationHistory";

import { useNavigationDirection } from "./useNavigationDirection";

/**
 * jsdom has no `document.startViewTransition`, so React Router takes its early
 * return and no view transition runs. That is fine here: the attribute is
 * written from the popstate listener, well before the router would open one.
 *
 * MemoryRouter keeps its own history, so `window.history.state.idx` — the index
 * the hook reads — is set by hand, exactly as the browser would have.
 */
function Harness({ to }: { to?: string }) {
  useNavigationDirection();
  const navigate = useNavigate();
  if (!to) return null;
  return <button onClick={() => navigate(to)}>go</button>;
}

/** Stands in for the browser committing an entry and dispatching its popstate. */
function pop(idx: number | undefined) {
  act(() => {
    window.history.replaceState(idx === undefined ? {} : { idx }, "");
    window.dispatchEvent(new PopStateEvent("popstate"));
  });
}

function direction() {
  return document.documentElement.dataset.navigationDirection;
}

beforeEach(() => {
  resetNavigationHistory();
  window.history.replaceState({ idx: 0 }, "");
  delete document.documentElement.dataset.navigationDirection;
});

afterEach(() => {
  window.history.replaceState(null, "");
  delete document.documentElement.dataset.navigationDirection;
});

describe("useNavigationDirection", () => {
  it("stamps back when the browser commits an earlier entry", () => {
    window.history.replaceState({ idx: 2 }, "");
    render(
      <MemoryRouter>
        <Harness />
      </MemoryRouter>,
    );

    pop(1);

    expect(direction()).toBe("back");
  });

  it("stamps forward when the browser commits a later entry", () => {
    window.history.replaceState({ idx: 2 }, "");
    render(
      <MemoryRouter>
        <Harness />
      </MemoryRouter>,
    );

    pop(1);
    pop(2);

    expect(direction()).toBe("forward");
  });

  it("clears a stale direction when the pop's direction is unknowable", () => {
    render(
      <MemoryRouter>
        <Harness />
      </MemoryRouter>,
    );
    markNavigationDirection("back");

    // No router index on the entry: something outside the router pushed it.
    pop(undefined);

    expect(direction()).toBeUndefined();
  });

  it("leaves the pushing caller's direction standing when the location commits", () => {
    render(
      <MemoryRouter initialEntries={["/item/series"]}>
        <Harness to="/item/season" />
      </MemoryRouter>,
    );

    // A push stamps its direction at click time. The effect that records the
    // new location flushes before the browser captures the new snapshot, so
    // clearing there would erase the direction mid-transition.
    markNavigationDirection("forward");
    window.history.replaceState({ idx: 1 }, "");
    fireEvent.click(screen.getByRole("button", { name: "go" }));

    expect(direction()).toBe("forward");
  });

  it("tracks the committed index so the next pop has an origin to compare", () => {
    render(
      <MemoryRouter initialEntries={["/item/series"]}>
        <Harness to="/item/season" />
      </MemoryRouter>,
    );

    window.history.replaceState({ idx: 1 }, "");
    fireEvent.click(screen.getByRole("button", { name: "go" }));

    // Without the location effect recording idx 1, this pop would have no
    // origin and would fall back to the neutral, directionless transition.
    pop(0);
    expect(direction()).toBe("back");
  });

  it("stops listening once unmounted", () => {
    window.history.replaceState({ idx: 2 }, "");
    const { unmount } = render(
      <MemoryRouter>
        <Harness />
      </MemoryRouter>,
    );
    unmount();

    pop(1);

    expect(direction()).toBeUndefined();
  });
});
