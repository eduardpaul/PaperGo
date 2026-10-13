import { useQuery, useQueryClient } from '@tanstack/react-query';
import { keys, resourceQuery } from '@/api/queries';
import { PackageTransfer } from '@/features/admin/package-transfer';
import { useHasScope } from '@/features/admin/common';
import { can } from '@/features/lists/access';

export function SmartFolderPackages({ workspaceId }: { workspaceId: string }) {
  const client = useQueryClient();
  const workspace = useQuery(resourceQuery(workspaceId));
  const canWrite = useHasScope('list.write');
  if (!can(workspace.data, 'manage')) return null;
  return (
    <PackageTransfer
      title="Smart folders"
      path={`/v1/workspaces/${workspaceId}/smart-folders`}
      version={workspace.data?.version}
      canImport={canWrite}
      onImported={async () => {
        await client.invalidateQueries({ queryKey: keys.smartFolders });
        await client.invalidateQueries({ queryKey: keys.resources });
      }}
    />
  );
}
