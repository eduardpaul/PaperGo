import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { Bell, Trash2 } from 'lucide-react';
import { notificationsQuery, unreadNotificationsQuery } from '@/api/queries';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { Alert, EmptyState, Skeleton } from '@/components/ui/feedback';
import { NotificationRow, useNotificationActions, useOpenNotification } from '@/features/notifications/notifications';
import { problemMessage } from '@/lib/errors';
import { textParam } from '@/lib/search';

interface NotificationsSearch {
  view?: string;
}

export const Route = createFileRoute('/_app/notifications')({
  validateSearch: (search: Record<string, unknown>): NotificationsSearch => ({ view: textParam(search.view) }),
  component: NotificationsPage,
});

function NotificationsPage() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const unreadOnly = search.view === 'unread';
  const list = useInfiniteQuery(notificationsQuery(unreadOnly));
  const unread = useQuery(unreadNotificationsQuery);
  const rows = list.data?.pages.flatMap((page) => page.value ?? []) ?? [];
  const { markRead, remove } = useNotificationActions();
  const openNotification = useOpenNotification();
  return (
    <Page>
      <PageHeader
        title="Notifications"
        description="What happened that concerns you: tasks, messages and failed workflows."
        icon={Bell}
        actions={
          <Button size="sm" disabled={!unread.data || markRead.isPending} onClick={() => markRead.mutate(undefined)}>
            Mark all read
          </Button>
        }
      />
      <div className="mb-4 flex flex-wrap gap-2" role="tablist" aria-label="Notification views">
        {[
          { value: 'all', label: 'All' },
          { value: 'unread', label: unread.data ? `Unread (${unread.data})` : 'Unread' },
        ].map((v) => {
          const selected = (v.value === 'unread') === unreadOnly;
          return (
            <Button
              key={v.value}
              role="tab"
              aria-selected={selected}
              size="sm"
              variant={selected ? 'primary' : 'secondary'}
              onClick={() => navigate({ search: { view: v.value === 'all' ? undefined : v.value } })}
            >
              {v.label}
            </Button>
          );
        })}
      </div>
      {list.isLoading && <Skeleton className="h-16" />}
      {list.error && <Alert>{problemMessage(list.error)}</Alert>}
      {list.isSuccess && rows.length === 0 && (
        <EmptyState icon={Bell} title={unreadOnly ? 'You have read everything' : 'No notifications yet'} />
      )}
      <ul className="flex flex-col divide-y rounded-lg border bg-surface empty:hidden" aria-label="Notifications">
        {rows.map((notification) => (
          <li key={notification.id} className="flex items-center gap-1 pr-2">
            <div className="min-w-0 flex-1">
              <NotificationRow
                notification={notification}
                onOpen={() => {
                  if (!notification.read_at) markRead.mutate([notification.id]);
                  void openNotification(notification);
                }}
              />
            </div>
            <Button
              variant="ghost"
              size="icon"
              aria-label={`Delete ${notification.title}`}
              disabled={remove.isPending}
              onClick={() => remove.mutate(notification.id)}
            >
              <Trash2 />
            </Button>
          </li>
        ))}
      </ul>
      {list.hasNextPage && (
        <Button className="mt-3" disabled={list.isFetching} onClick={() => void list.fetchNextPage()}>
          {list.isFetchingNextPage ? 'Loading…' : 'Load more'}
        </Button>
      )}
    </Page>
  );
}
