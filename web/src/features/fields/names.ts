import { useQueries } from '@tanstack/react-query';
import { useMemo } from 'react';
import { loadResourceName, loadTermName } from '@/api/names';
import type { FieldDefinition, Resource } from '@/api/types';
import { usePeopleNames } from './people';
import { asList } from './values';

const nameStaleTime = 5 * 60_000;

/** Distinct ids held by fields of the given types, sorted so the query key stays stable. */
export function referencedIds(fields: FieldDefinition[], rows: Pick<Resource, 'values'>[], types: string[]): string[] {
  const keys = fields.filter((field) => types.includes(field.type)).map((field) => field.key);
  const ids = new Set<string>();
  for (const row of rows) for (const key of keys) for (const id of asList(row.values?.[key])) ids.add(id);
  return [...ids].sort();
}

/** Display names for the people, terms, keywords and looked-up items shown in rows. */
export function useValueNames(fields: FieldDefinition[], rows: Pick<Resource, 'values'>[]) {
  const people = usePeopleNames();
  const termIds = referencedIds(fields, rows, ['term', 'keywords']);
  const itemIds = referencedIds(fields, rows, ['lookup']);
  const terms = useTermNames(termIds);
  const items = useResourceNames(itemIds);
  return useMemo(() => new Map([...people, ...terms, ...items]), [people, terms, items]);
}

type NameResult = { data?: readonly [string, string | null] };

/** The names loaded so far, by id; ids still loading or unknown are missing. */
function namesById(results: NameResult[]): Map<string, string> {
  const names = new Map<string, string>();
  for (const result of results) if (result.data && result.data[1] !== null) names.set(result.data[0], result.data[1]);
  return names;
}

/** Names of terms and keywords. Each id is cached on its own, so more rows only read the ids not seen yet. */
export function useTermNames(ids: string[]) {
  return useQueries({
    queries: ids.map((id) => ({
      queryKey: ['names', 'terms', id],
      queryFn: async () => [id, await loadTermName(id)] as const,
      staleTime: nameStaleTime,
    })),
    combine: namesById,
  });
}

/** Names of items. Each id is cached on its own, so more rows only read the ids not seen yet. */
export function useResourceNames(ids: string[]) {
  return useQueries({
    queries: ids.map((id) => ({
      queryKey: ['resources', 'name', id],
      queryFn: async () => [id, await loadResourceName(id)] as const,
      staleTime: nameStaleTime,
    })),
    combine: namesById,
  });
}
