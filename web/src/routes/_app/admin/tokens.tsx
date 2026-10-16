import { createFileRoute } from '@tanstack/react-router';
import { AccessTokens } from '@/features/admin/access-tokens';
export const Route = createFileRoute('/_app/admin/tokens')({ component: AccessTokens });
