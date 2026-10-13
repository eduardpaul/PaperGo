import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { Briefcase, ChevronRight, Inbox } from 'lucide-react';
import { countChildren, homeQuery, meQuery, workspacesQuery } from '@/api/queries';
import { Page } from '@/components/page';
import { useHasScope } from '@/features/admin/common';
import { Card, CardHeader, CardTitle } from '@/components/ui/card';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { useFormat } from '@/lib/preferences';
import { useNow } from '@/lib/use-now';

export const Route = createFileRoute('/_app/')({ component: HomePage });

function greeting(hour: number) {
  if (hour < 12) return hour < 5 ? 'Good evening' : 'Good morning';
  if (hour < 18) return 'Good afternoon';
  return 'Good evening';
}

function HomePage() {
  const format = useFormat();
  const now = useNow();
  const { data: me } = useQuery(meQuery);
  const firstName = (me?.display_name ?? '').split(' ')[0];

  return (
    <Page>
      <header className="mb-6">
        <p className="text-[13px] text-muted">
          {format.weekday(now)}, {format.date(now)}
        </p>
        <h1 className="mt-0.5 text-2xl font-semibold tracking-tight">
          {greeting(format.hour(now))}
          {firstName ? `, ${firstName}` : ''}
        </h1>
      </header>
      <div className="grid gap-4 lg:grid-cols-2">
        <InboxCard />
        <WorkspacesCard />
      </div>
    </Page>
  );
}

function InboxCard() {
  const home = useQuery(homeQuery);
  const count = useQuery({
    queryKey: ['resources', home.data?.inbox_library_id, 'count'],
    enabled: !!home.data?.inbox_library_id,
    queryFn: () => countChildren(home.data!.inbox_library_id),
  });
  const waiting = count.data ?? 0;
  return (
    <Card>
      <Link to="/inbox" className="flex items-center gap-3 rounded-lg px-4 py-3 hover:bg-surface-muted/60">
        <span className="rounded-md bg-accent-soft p-2 text-accent">
          <Inbox className="size-4" />
        </span>
        <span className="flex-1">
          <span className="block text-sm font-semibold">Inbox</span>
          <span className="text-xs text-muted">{waiting ? `${waiting} to file` : 'Nothing to file'}</span>
        </span>
        <ChevronRight className="size-4 text-muted" />
      </Link>
    </Card>
  );
}

function WorkspacesCard() {
  const { data, isPending } = useQuery(workspacesQuery);
  const canCreate = useHasScope('workspace.create');
  const workspaces = data?.filter((workspace) => !workspace.personal_owner_id) ?? [];
  return (
    <Card>
      <CardHeader>
        <Briefcase className="size-4 text-muted" />
        <CardTitle className="flex-1">Workspaces</CardTitle>
        <Link to="/w" className="text-xs font-medium text-accent hover:underline">
          All
        </Link>
      </CardHeader>
      {isPending ? (
        <div className="p-4">
          <Skeleton className="h-9" />
        </div>
      ) : workspaces.length ? (
        <ul className="p-1.5">
          {workspaces.slice(0, 6).map((workspace) => (
            <li key={workspace.id}>
              <Link to="/w/$workspaceId" params={{ workspaceId: workspace.id }} className="flex items-center gap-2 rounded-md px-2.5 py-2 text-[13px] hover:bg-surface-muted">
                <span className="flex-1 truncate font-medium">{workspace.name}</span>
                <ChevronRight className="size-4 text-muted" />
              </Link>
            </li>
          ))}
        </ul>
      ) : (
        <EmptyState icon={Briefcase} title="No workspaces yet">
          {canCreate ? (
            <>
              <Link to="/w" search={{ create: true }} className="font-medium text-accent hover:underline">
                Create a workspace
              </Link>{' '}
              for a team or project.
            </>
          ) : (
            'Ask an administrator to add you to a workspace.'
          )}
        </EmptyState>
      )}
    </Card>
  );
}
