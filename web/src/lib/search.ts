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
