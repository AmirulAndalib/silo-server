import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, expect, it, vi } from "vitest";

import { getPerson } from "@/api/v2/people";
import { catalogKeys, personKeys } from "@/hooks/queries/keys";
import PersonDetail from "./PersonDetail";

vi.mock("@/api/v2/people", () => ({ getPerson: vi.fn() }));
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ user: null }) }));
vi.mock("@/hooks/useIsActingAdmin", () => ({ useIsActingAdmin: () => false }));
vi.mock("@/hooks/queries/catalog", () => ({ useCatalogWindow: () => ({ isLoading: false }) }));
vi.mock("@/components/ItemGrid", () => ({ default: () => null }));

afterEach(() => vi.clearAllMocks());

it("refreshes cached cast after a person read observes a background photo update", async () => {
  const id = "9007199254740993";
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 120_000 } },
  });
  const person = {
    id: Number(id),
    name: "Actor",
    bio: "Biography",
    birth_date: "1979-08-01",
    photo_url: "https://images.example.test/old.jpg",
  };
  const itemKey = catalogKeys.itemDetail("movie", 12);
  const item = {
    content_id: "movie",
    cast: [{ person_id: id, photo_url: person.photo_url }],
    crew: [],
  };
  client.setQueryData(personKeys.detail(id), person);
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/person/${id}`]}>
        <Routes>
          <Route path="/person/:id" element={<PersonDetail />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(screen.getByRole("img", { name: "Actor" })).toHaveAttribute("src", person.photo_url);

  // The cached item is fresh when a subsequent poll sees the completed refresh.
  client.setQueryData(itemKey, item);
  const updated = { ...person, photo_url: "https://images.example.test/new.jpg" };
  vi.mocked(getPerson).mockResolvedValue(updated);
  await act(() => client.refetchQueries({ queryKey: personKeys.detail(id) }));

  await waitFor(() => {
    expect(screen.getByRole("img", { name: "Actor" })).toHaveAttribute("src", updated.photo_url);
    expect(client.getQueryState(itemKey)?.isInvalidated).toBe(true);
  });
});
