import { queryOptions, useQuery } from '@tanstack/react-query';
import { permissions } from '@/api/queries';

/**
 * Resources do not carry the caller's access level. Reading permissions needs manage access, so a successful read
 * tells the screen to offer settings and management actions. The server stays the authority for every write.
 */
export const permissionsQuery = (id: string) =>
  queryOptions({
    queryKey: ['permissions', id],
    queryFn: () => permissions(id),
    retry: false,
    staleTime: 60_000,
  });

export function useCanManage(id: string | undefined) {
  const query = useQuery({ ...permissionsQuery(id ?? ''), enabled: !!id });
  return { canManage: query.isSuccess, checked: !query.isPending || !id };
}
