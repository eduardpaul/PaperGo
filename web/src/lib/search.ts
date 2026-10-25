import { useQuery } from '@tanstack/react-query';
import { workflowCatalogQuery } from '@/api/queries';

export function textParam(value: unknown): string | undefined {
  return typeof value === 'string' && value.length > 0 ? value : undefined;
}

// useSearchAvailable reports whether the deployment runs search, which is
// optional; without it, listings still match names.
export function useSearchAvailable(): boolean {
  const { data } = useQuery({ ...workflowCatalogQuery, select: (catalog) => (catalog.roles ?? []).some((role) => role.key === 'search.index') });
  return data ?? false;
}

/** A page number from a URL: a positive integer, or nothing. */
export function pageParam(value: unknown): number | undefined {
  const page = typeof value === 'number' ? value : typeof value === 'string' ? Number(value) : NaN;
  return Number.isInteger(page) && page > 1 ? page : undefined;
}

/** A flag from a URL: true, or nothing. */
export function flagParam(value: unknown): true | undefined {
  return value === true || value === 'true' || value === 1 || value === '1' ? true : undefined;
}
