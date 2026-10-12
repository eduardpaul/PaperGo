import { createFileRoute } from '@tanstack/react-router';
import { ContentTypeCatalog } from '@/features/admin/content-types';
export const Route = createFileRoute('/_app/admin/content-types')({ component: ContentTypeCatalog });
