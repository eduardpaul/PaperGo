import { queryOptions, useQuery } from '@tanstack/react-query';
import { permissions, resourceQuery } from '@/api/queries';
import type { AccessAction, Resource } from '@/api/types';

/** Whether the caller may take an action on a resource, from its `access` (single-resource reads and updates). */
export function can(resource: Pick<Resource, 'access'> | undefined, action: AccessAction) {
  return !!resource?.access?.includes(action);
}

/**
 * The caller's actions on a resource. Screens offer only these; the server still checks every request.
 * Write is adding and editing content, publish is publishing it, and manage is settings, permissions and, on a
 * workspace, being an owner.
 */
export function useAccess(id: string | undefined) {
  const query = useQuery({ ...resourceQuery(id ?? ''), enabled: !!id });
  return {
    loaded: !!query.data,
    canWrite: can(query.data, 'write'),
    canPublish: can(query.data, 'publish'),
    canManage: can(query.data, 'manage'),
  };
}

/** Explicit grants and inheritance; readable by managers only. */
export const permissionsQuery = (id: string) =>
  queryOptions({
    queryKey: ['permissions', id],
    queryFn: () => permissions(id),
    retry: false,
  });
