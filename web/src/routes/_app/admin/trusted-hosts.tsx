import { createFileRoute } from '@tanstack/react-router';
import { TrustedHostsPage } from '@/features/admin/trusted-hosts';
export const Route = createFileRoute('/_app/admin/trusted-hosts')({ component: TrustedHostsPage });
