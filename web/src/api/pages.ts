import { useInfiniteQuery } from '@tanstack/react-query';
import { api, continuation } from './http';
import type { Page } from './types';

export async function allPages<T>(path: string, query: Record<string, string | number | undefined> = {}): Promise<T[]> {
  const items: T[] = [];
  let next: string | null = path;
  let first = true;
  while (next) {
    const page: Page<T> = (await api<Page<T>>(next, { query: first ? { $top: 200, ...query } : undefined })).data;
    items.push(...(page.value ?? []));
    next = continuation(page['@odata.nextLink']);
    first = false;
  }
  return items;
}

export function useApiPages<T>(
  key: readonly unknown[],
  path: string,
  options: { query?: Record<string, string | number | undefined>; enabled?: boolean; body?: unknown } = {},
) {
  const result = useInfiniteQuery({
    queryKey: [...key, path, options.query ?? {}, options.body ?? null],
    initialPageParam: path,
    enabled: options.enabled,
    queryFn: async ({ pageParam }) =>
      (
        await api<Page<T>>(pageParam, {
          method: options.body === undefined ? 'GET' : 'POST',
          body: options.body,
          query: pageParam === path ? { $top: 50, ...options.query } : undefined,
        })
      ).data,
    getNextPageParam: (page) => continuation(page['@odata.nextLink']) ?? undefined,
  });
  return { ...result, items: result.data?.pages.flatMap((page) => page.value ?? []) ?? [] };
}
