import { createFileRoute } from '@tanstack/react-router';
import { RelationshipTypesCatalog } from '@/features/admin/relationship-types';
export const Route = createFileRoute('/_app/admin/relationship-types')({ component: RelationshipTypesCatalog });
