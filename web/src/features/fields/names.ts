import { useQueries } from '@tanstack/react-query';
import { useCallback, useMemo } from 'react';
import { loadResourceName, loadTermName, unavailable, type Name } from '@/api/names';
import type { FieldDefinition, Resource } from '@/api/types';
import { useTranslation } from '@/i18n';
import { usePeopleNames } from './people';
import { asList } from './values';

const nameStaleTime = 5 * 60_000;

/** Distinct ids held by fields of the given types, sorted so the query key stays stable. */
export function referencedIds(fields: FieldDefinition[], rows: Pick<Resource, 'values' | 'keywords'>[], types: string[]): string[] {
  const keys = fields.filter((field) => types.includes(field.type)).map((field) => field.key);
  const ids = new Set<string>(types.includes('keywords') ? rows.flatMap(row => row.keywords ?? []) : []);
  for (const row of rows) for (const key of keys) for (const id of asList(row.values?.[key])) ids.add(id);
  return [...ids].sort();
}

/** Display names for the people, terms, keywords and looked-up items shown in rows. */
export function useValueNames(fields: FieldDefinition[], rows: Pick<Resource, 'values' | 'keywords'>[]) {
  const people = usePeopleNames();
  const termIds = referencedIds(fields, rows, ['term', 'keywords']);
  const itemIds = referencedIds(fields, rows, ['lookup']);
  const terms = useTermNames(termIds);
  const items = useResourceNames(itemIds);
  return useMemo(() => new Map([...people, ...terms, ...items]), [people, terms, items]);
}

type NameResult = { data?: readonly [string, Name | null] };

/**
 * The names loaded so far, by id, with unavailable ones named in the screen's language; ids still loading, or not
 * readable at all, are missing. The function stays the same while the language does, so the names do too.
 */
function useNamesById(unavailableName: 'values.unknownTerm' | 'values.unavailableItem') {
  const { t } = useTranslation('home');
  return useCallback(
    (results: NameResult[]) => {
      const names = new Map<string, string>();
      for (const result of results)
        if (result.data && result.data[1] !== null)
          names.set(result.data[0], result.data[1] === unavailable ? t(unavailableName) : result.data[1]);
      return names;
    },
    [t, unavailableName],
  );
}

/** Names of terms and keywords. Each id is cached on its own, so more rows only read the ids not seen yet. */
export function useTermNames(ids: string[]) {
  const combine = useNamesById('values.unknownTerm');
  return useQueries({
    queries: ids.map((id) => ({
      queryKey: ['names', 'terms', id],
      queryFn: async () => [id, await loadTermName(id)] as const,
      staleTime: nameStaleTime,
    })),
    combine,
  });
}

/** Names of items. Each id is cached on its own, so more rows only read the ids not seen yet. */
export function useResourceNames(ids: string[]) {
  const combine = useNamesById('values.unavailableItem');
  return useQueries({
    queries: ids.map((id) => ({
      queryKey: ['resources', 'name', id],
      queryFn: async () => [id, await loadResourceName(id)] as const,
      staleTime: nameStaleTime,
    })),
    combine,
  });
}
