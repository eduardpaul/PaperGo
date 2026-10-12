import { useQuery } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { meQuery } from '@/api/queries';
import { Alert, Skeleton } from '@/components/ui/feedback';
import { Label } from '@/components/ui/input';
import { problemMessage } from '@/lib/errors';

export function useHasScope(scope: string) {
  return useQuery(meQuery).data?.scopes?.includes(scope) ?? false;
}

export function Field({
  id,
  label,
  hint,
  children,
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted">{hint}</p>}
    </div>
  );
}

export function QueryFeedback({ query }: { query: { isPending: boolean; error: unknown } }) {
  if (query.error) return <Alert>{problemMessage(query.error)}</Alert>;
  return query.isPending ? <Skeleton className="h-20" /> : null;
}
