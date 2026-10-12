import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { resourceNames, termNames } from '@/api/names';
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
  return useMemo(() => new Map([...people, ...(terms ?? []), ...(items ?? [])]), [people, terms, items]);
}

export function useTermNames(ids: string[]) {
  return useQuery({
    queryKey: ['names', 'terms', ids],
    queryFn: () => termNames(ids),
    enabled: ids.length > 0,
    staleTime: nameStaleTime,
  }).data;
}

export function useResourceNames(ids: string[]) {
  return useQuery({
    queryKey: ['resources', 'names', ids],
    queryFn: () => resourceNames(ids),
    enabled: ids.length > 0,
    staleTime: nameStaleTime,
  }).data;
}
