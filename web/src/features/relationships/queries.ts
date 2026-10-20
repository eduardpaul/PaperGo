import { useInfiniteQuery } from '@tanstack/react-query';
import { api, continuation } from '@/api/http';
import type { Page } from '@/api/types';

/** Keep fetched pages and stale searches bounded. Never walk the entire dataset. */
export function useExplorerPages<T>(path: string, query: Record<string, string | number | undefined>, enabled = true) {
  const result = useInfiniteQuery({
    queryKey: ['relationship-explorer', path, query],
    initialPageParam: path,
    enabled,
    maxPages: 20,
    gcTime: 60_000,
    staleTime: 15_000,
    queryFn: async ({ pageParam, signal }) => (await api<Page<T>>(pageParam, {
      signal,
      query: pageParam === path ? { $top: 50, ...query } : undefined,
    })).data,
    getNextPageParam: page => continuation(page['@odata.nextLink']) ?? undefined,
  });
  return { ...result, items: result.data?.pages.flatMap(page => page.value ?? []) ?? [] };
}
