import { useQuery } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { resourceQuery } from '@/api/queries';
import { AccessEditor } from '@/features/lists/access-editor';
import { SettingsSection } from '@/features/settings/section';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/settings/permissions')({
  component: PermissionsPage,
});

function PermissionsPage() {
  const { t } = useTranslation('workspaces');
  const { listId } = Route.useParams();
  const collection = useQuery(resourceQuery(listId));
  if (!collection.data) return null;
  return (
    <SettingsSection
      title={t('collection.permissions')}
      description={t('collection.permissionsDescription')}
    >
      <AccessEditor resource={collection.data} />
    </SettingsSection>
  );
}
