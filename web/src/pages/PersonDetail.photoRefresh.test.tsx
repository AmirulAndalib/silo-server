import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, expect, it, vi } from "vitest";

import { getPerson, refreshPerson } from "@/api/v2/people";
import { v2Fixture } from "@/api/v2/testing";
import { catalogKeys, personKeys } from "@/hooks/queries/keys";
import { useAuth } from "@/hooks/useAuth";
import PersonDetail from "./PersonDetail";

vi.mock("@/api/v2/people", () => ({ getPerson: vi.fn(), refreshPerson: vi.fn() }));
vi.mock("@/hooks/useAuth", () => ({ useAuth: vi.fn(() => ({ user: null })) }));
vi.mock("@/hooks/useIsActingAdmin", () => ({ useIsActingAdmin: () => false }));
vi.mock("@/hooks/queries/catalog", () => ({ useCatalogWindow: () => ({ isLoading: false }) }));
vi.mock("@/components/ItemGrid", () => ({ default: () => null }));

const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  vi.useRealTimers();
  vi.clearAllMocks();
});

it("refreshes cached cast after a person read observes a background photo update", async () => {
  const id = "9007199254740993";
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 120_000 } },
  });
  clients.push(client);
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

it("observes a queued refresh of complete metadata after returning to the item", async () => {
  vi.useFakeTimers();
  vi.mocked(useAuth).mockReturnValue({ user: { id: 1 } } as ReturnType<typeof useAuth>);
  const id = "9007199254740993";
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 120_000 } },
  });
  clients.push(client);
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
  client.setQueryData(itemKey, item);
  vi.mocked(getPerson).mockResolvedValue(person);
  vi.mocked(refreshPerson).mockResolvedValue(
    v2Fixture<"POST /api/v2/catalog/people/{id}/refresh">({ status: "queued", person_id: id }),
  );
  const view = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/person/${id}`]}>
        <Routes>
          <Route path="/person/:id" element={<PersonDetail />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Refresh metadata" }));
    await vi.advanceTimersByTimeAsync(0);
  });
  expect(refreshPerson).toHaveBeenCalledWith(id);
  view.unmount();

  let serverItem = item;
  const returned = renderHook(
    () => useQuery({ queryKey: itemKey, queryFn: async () => serverItem }),
    {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    },
  );
  await act(() => vi.advanceTimersByTimeAsync(0));
  expect(returned.result.current.data?.cast[0]?.photo_url).toBe(person.photo_url);

  const photoUrl = "https://images.example.test/new.jpg";
  serverItem = { ...item, cast: [{ person_id: id, photo_url: photoUrl }] };
  vi.mocked(getPerson).mockResolvedValue({ ...person, photo_url: photoUrl });
  await act(() => vi.advanceTimersByTimeAsync(3_001));

  expect(getPerson).toHaveBeenCalledTimes(2);
  expect(returned.result.current.data?.cast[0]?.photo_url).toBe(photoUrl);
});
