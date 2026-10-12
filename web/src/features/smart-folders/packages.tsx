import { useQuery, useQueryClient } from '@tanstack/react-query';
import { keys, permissions, resourceQuery } from '@/api/queries';
import { PackageTransfer } from '@/features/admin/package-transfer';
import { useHasScope } from '@/features/admin/common';

export function SmartFolderPackages({ workspaceId }: { workspaceId: string }) {
  const client = useQueryClient();
  const workspace = useQuery(resourceQuery(workspaceId));
  const access = useQuery({
    queryKey: ['permissions', workspaceId],
    queryFn: () => permissions(workspaceId),
    retry: false,
  });
  const canWrite = useHasScope('list.write');
  if (!access.data) return null;
  return (
    <PackageTransfer
      title="Smart folders"
      path={`/v1/workspaces/${workspaceId}/smart-folders`}
      version={workspace.data?.version}
      canImport={canWrite && !!access.data && !!workspace.data}
      onImported={async () => {
        await client.invalidateQueries({ queryKey: keys.smartFolders });
        await client.invalidateQueries({ queryKey: keys.resources });
      }}
    />
  );
}
