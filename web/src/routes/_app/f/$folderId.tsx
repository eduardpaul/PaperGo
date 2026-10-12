import { createFileRoute } from '@tanstack/react-router';
import { SmartFolderPage } from '@/features/smart-folders/folder-page';

export const Route = createFileRoute('/_app/f/$folderId')({
  component: function SmartFolderRoute() {
    const { folderId } = Route.useParams();
    return <SmartFolderPage folderId={folderId} />;
  },
});
