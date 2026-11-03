import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { Bell, Trash2 } from 'lucide-react';
import { notificationsQuery, unreadNotificationsQuery } from '@/api/queries';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { Alert, EmptyState, Skeleton } from '@/components/ui/feedback';
import { NotificationRow, useNotificationActions, useOpenNotification } from '@/features/notifications/notifications';
import { useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';
import { textParam } from '@/lib/search';

interface NotificationsSearch {
  view?: string;
}

export const Route = createFileRoute('/_app/notifications')({
  validateSearch: (search: Record<string, unknown>): NotificationsSearch => ({ view: textParam(search.view) }),
  component: NotificationsPage,
});

const views = ['all', 'unread', 'mentions'] as const;

function NotificationsPage() {
  const { t } = useTranslation(['notifications', 'common']);
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const view = search.view === 'unread' || search.view === 'mentions' ? search.view : 'all';
  const unreadOnly = view === 'unread';
  const list = useInfiniteQuery(notificationsQuery(unreadOnly, view === 'mentions' ? 'comment.mentioned' : undefined));
  const unread = useQuery(unreadNotificationsQuery);
  const rows = list.data?.pages.flatMap((page) => page.value ?? []) ?? [];
  const { markRead, remove } = useNotificationActions();
  const openNotification = useOpenNotification();
  return (
    <Page>
      <PageHeader
        title={t('title')}
        description={t('page.description')}
        icon={Bell}
        actions={
          <Button size="sm" disabled={!unread.data || markRead.isPending} onClick={() => markRead.mutate(undefined)}>
            {t('markAllRead')}
          </Button>
        }
      />
      <div className="mb-4 flex flex-wrap gap-2" role="tablist" aria-label={t('page.views')}>
        {views.map((value) => ({
          value,
          label:
            value === 'unread'
              ? unread.data
                ? t('page.unreadCount', { unread: unread.data })
                : t('unread')
              : t(value === 'all' ? 'page.all' : 'page.mentions'),
        })).map((v) => {
          const selected = v.value === view;
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
        <EmptyState
          icon={Bell}
          title={
            unreadOnly
              ? t('page.allRead')
              : view === 'mentions'
                ? t('page.noMentions')
                : t('page.none')
          }
        />
      )}
      <ul className="flex flex-col divide-y rounded-lg border bg-surface empty:hidden" aria-label={t('title')}>
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
              aria-label={t('page.delete', { title: notification.title })}
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
          {list.isFetchingNextPage ? t('common:status.loading') : t('page.loadMore')}
        </Button>
      )}
    </Page>
  );
}
