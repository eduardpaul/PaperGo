import { useQuery } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { resourceQuery } from '@/api/queries';
import { AccessEditor } from '@/features/lists/access-editor';
import { SettingsSection } from '@/features/settings/section';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/settings/permissions')({
  component: PermissionsPage,
});

function PermissionsPage() {
  const { listId } = Route.useParams();
  const collection = useQuery(resourceQuery(listId));
  if (!collection.data) return null;
  return (
    <SettingsSection
      title="Permissions"
      description="By default everyone in the workspace has access by their workspace role. Folders and items can have their own permissions too."
    >
      <AccessEditor resource={collection.data} />
    </SettingsSection>
  );
}
