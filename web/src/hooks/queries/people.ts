import {
  type Query,
  type QueryClient,
  QueryObserver,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { toast } from "sonner";

import { adminRefreshPerson, adminUpdatePerson } from "@/api/v2/people";
import type { ItemDetail, Person, UpdatePersonRequest } from "@/api/types";
import { getPerson, refreshPerson, searchPeople, type PersonRefreshResult } from "@/api/v2/people";

import { personKeys } from "./keys";
import { isItemDetailQueryKey } from "./mediaSurfaceRefresh";

function isPersonItemDetail(query: Query, personId: string) {
  const item = query.state.data as ItemDetail | undefined;
  return (
    !!item &&
    isItemDetailQueryKey(query.queryKey, item.content_id) &&
    (item.cast?.some((credit) => credit.person_id === personId) ||
      item.crew?.some((credit) => credit.person_id === personId))
  );
}

export function invalidatePersonItemDetails(queryClient: QueryClient, personId: string) {
  return queryClient.invalidateQueries({
    predicate: (query) => isPersonItemDetail(query, personId),
  });
}

const queuedRefreshObservers = new WeakMap<QueryClient, Map<string, () => void>>();

function observeQueuedPersonRefresh(queryClient: QueryClient, id: string) {
  const refreshes = queuedRefreshObservers.get(queryClient) ?? new Map<string, () => void>();
  queuedRefreshObservers.set(queryClient, refreshes);
  refreshes.get(id)?.();

  const queryKey = personKeys.detail(id);
  const startedAt = Date.now();
  let photoUrl = queryClient.getQueryData<Person>(queryKey)?.photo_url;
  let refreshOnResume = false;
  const observer = new QueryObserver(queryClient, {
    queryKey,
    queryFn: ({ signal }) => getPerson(id, { signal }),
    staleTime: 0,
    retry: false,
    // Queue wait and photo caching can outlast the worker's per-person timeout.
    refetchInterval: () => (Date.now() - startedAt < 30_000 ? 3_000 : 30_000),
  });
  const query = observer.getCurrentQuery();
  const unsubscribeCache = queryClient.getQueryCache().subscribe((event) => {
    if (event.type === "removed" && event.query === query) stop();
    else if (
      event.type === "removed" ||
      (event.query === query &&
        (event.type === "observerAdded" || event.type === "observerRemoved")) ||
      (event.type === "updated" && event.query !== query)
    ) {
      updateObservation();
    }
  });
  function stop() {
    unsubscribeCache();
    observer.destroy();
    refreshes.delete(id);
  }
  function updateObservation() {
    const observing = observer.hasListeners();
    const needed =
      query.getObserversCount() > (observing ? 1 : 0) ||
      queryClient
        .getQueryCache()
        .getAll()
        .some((item) => isPersonItemDetail(item, id));
    if (needed && !observing) {
      observer.subscribe((result) => {
        // Presigned URLs can rotate before the job finishes; this is not completion.
        if (
          result.isSuccess &&
          !result.isFetching &&
          (refreshOnResume || result.data.photo_url !== photoUrl)
        ) {
          refreshOnResume = false;
          photoUrl = result.data.photo_url;
          void invalidatePersonItemDetails(queryClient, id);
        }
      });
    } else if (!needed && observing) {
      // Pause during navigation. Resume when an uncached related detail arrives;
      // the cache listener is disposed when this inactive person query expires.
      // That detail may have fetched its old photo before the person was updated.
      refreshOnResume = true;
      observer.destroy();
    }
  }
  refreshes.set(id, stop);
  updateObservation();
}

export function usePersonSearch(query: string, limit = 20, enabled = true) {
  const normalizedQuery = query.trim();

  return useQuery({
    queryKey: personKeys.search(normalizedQuery, limit),
    queryFn: ({ signal }) => searchPeople(normalizedQuery, limit, { signal }),
    enabled: enabled && normalizedQuery.length > 0,
    staleTime: 5 * 60 * 1000,
  });
}

type RefreshPersonResult =
  | {
      mode: "admin";
      person: Person;
    }
  | {
      mode: "queued";
      response: PersonRefreshResult;
    };

export function useRefreshPerson(id: string | undefined, isAdmin: boolean) {
  const queryClient = useQueryClient();

  return useMutation({
    retry: false,
    onMutate: () =>
      queryClient.getQueryCache().find({ queryKey: personKeys.detail(id!), exact: true }),
    mutationFn: async (): Promise<RefreshPersonResult> => {
      if (!id) {
        throw new Error("Person ID is required");
      }

      if (isAdmin) {
        return {
          mode: "admin",
          person: await adminRefreshPerson(id),
        };
      }

      return {
        mode: "queued",
        response: await refreshPerson(id),
      };
    },
    onSuccess: async (result, _variables, personQuery) => {
      if (result.mode === "admin" && id) {
        queryClient.setQueryData(personKeys.detail(id), result.person);
        await Promise.all([
          queryClient.invalidateQueries({ queryKey: personKeys.detail(id) }),
          invalidatePersonItemDetails(queryClient, id),
        ]);
        toast.success("Person metadata refreshed");
        return;
      }

      if (result.mode === "queued" && personQuery) {
        const refreshedId = result.response.person_id;
        const current = queryClient.getQueryCache().find({
          queryKey: personKeys.detail(refreshedId),
          exact: true,
        });
        if (current === personQuery) observeQueuedPersonRefresh(queryClient, refreshedId);
      }
      toast.success("Person refresh queued");
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Refresh failed");
    },
  });
}

export function useUpdatePersonMetadata(id: string | undefined) {
  const queryClient = useQueryClient();

  return useMutation({
    retry: false,
    mutationFn: (data: UpdatePersonRequest) => {
      if (!id) {
        throw new Error("Person ID is required");
      }

      return adminUpdatePerson(id, data);
    },
    onSuccess: async (updatedPerson) => {
      if (!id) {
        return;
      }

      queryClient.setQueryData(personKeys.detail(id), updatedPerson);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: personKeys.detail(id) }),
        invalidatePersonItemDetails(queryClient, id),
      ]);
      toast.success("Person metadata saved");
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to save metadata");
    },
  });
}
