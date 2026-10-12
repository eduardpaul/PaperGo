import { createFileRoute } from '@tanstack/react-router';
import { TermStore } from '@/features/admin/taxonomy';
export const Route = createFileRoute('/_app/admin/terms')({ component: TermStore });
