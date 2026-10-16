import { createFileRoute } from '@tanstack/react-router';
import { SmartFolderPage } from '@/features/smart-folders/folder-page';

export const Route = createFileRoute('/_app/f/$folderId')({
  validateSearch: (search: Record<string, unknown>): { edit?: boolean } => ({ edit: search.edit === true || search.edit === 'true' ? true : undefined }),
  component: function SmartFolderRoute() {
    const { folderId } = Route.useParams();
    return <SmartFolderPage folderId={folderId} edit={Route.useSearch().edit} />;
  },
});
