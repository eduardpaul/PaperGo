import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import {
  AlertTriangle,
  AtSign,
  Bell,
  CheckCheck,
  ClipboardCheck,
  Mail,
  MessageSquare,
  type LucideIcon,
} from 'lucide-react';
import { useState } from 'react';
import { api } from '@/api/http';
import { keys, notificationsQuery, resourceQuery, unreadNotificationsQuery } from '@/api/queries';
import type { Notification } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Alert, Skeleton } from '@/components/ui/feedback';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';
import { cn } from '@/lib/utils';

const kinds: Record<Notification['type'], { label: string; icon: LucideIcon }> = {
  message: { label: 'Message', icon: Mail },
  'task.assigned': { label: 'Task for you', icon: ClipboardCheck },
  'task.completed': { label: 'Task answered', icon: CheckCheck },
  'workflow.failed': { label: 'Workflow failed', icon: AlertTriangle },
  'comment.mentioned': { label: 'Mention', icon: AtSign },
  'comment.replied': { label: 'Reply', icon: MessageSquare },
};

/** Marks notifications read, or with no IDs all of them, and deletes them. */
export function useNotificationActions() {
  const queryClient = useQueryClient();
  const refresh = () => queryClient.invalidateQueries({ queryKey: keys.notifications });
  const markRead = useMutation({
    mutationFn: (ids?: string[]) =>
      api<{ marked: number }>('/v1/me/notifications/read', { method: 'POST', body: ids ? { ids } : {} }),
    onSuccess: refresh,
  });
  const remove = useMutation({
    mutationFn: (id: string) => api(`/v1/me/notifications/${id}`, { method: 'DELETE' }),
    onSuccess: refresh,
  });
  return { markRead, remove };
}

/** Opens what a notification links to: its task, its item in its list or library (on the Discussion tab for a comment), or its run. */
export function useOpenNotification() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  return async (notification: Notification) => {
    if (notification.task_id) {
      await navigate({ to: '/tasks', search: { view: 'all', id: notification.task_id } });
    } else if (notification.item_id && notification.workspace_id) {
      const item = await queryClient.fetchQuery(resourceQuery(notification.item_id));
      if (!item.container_id) return;
      await navigate({
        to: '/w/$workspaceId/l/$listId',
        params: { workspaceId: notification.workspace_id, listId: item.container_id },
        search: notification.comment_id
          ? { item: notification.item_id, tab: 'discussion', comment: notification.comment_id }
          : { item: notification.item_id },
      });
    } else if (notification.run_id && notification.workspace_id) {
      await navigate({
        to: '/w/$workspaceId/settings/runs',
        params: { workspaceId: notification.workspace_id },
        search: { run: notification.run_id },
      });
    }
  };
}

/** One notification: what it is, its title and message, who and when. */
export function NotificationRow({ notification, onOpen }: { notification: Notification; onOpen: () => void }) {
  const format = useFormat();
  const kind = kinds[notification.type] ?? kinds.message;
  const unread = !notification.read_at;
  return (
    <button
      type="button"
      onClick={onOpen}
      className="flex w-full items-start gap-3 rounded-md px-3 py-2 text-left hover:bg-surface-muted"
    >
      <kind.icon
        className={cn('mt-0.5 size-4 shrink-0', notification.type === 'workflow.failed' ? 'text-danger' : 'text-muted')}
        aria-hidden
      />
      <span className="min-w-0 flex-1">
        <span className={cn('block truncate text-[13px]', unread && 'font-semibold')}>
          {notification.title}
          {notification.count > 1 && <span className="ml-1 text-xs font-normal text-muted">×{notification.count}</span>}
        </span>
        {notification.body && (
          <span className="line-clamp-2 block text-xs whitespace-pre-line text-muted">{notification.body}</span>
        )}
        <span className="block text-xs text-muted">
          {kind.label}
          {notification.sender ? ` · ${notification.sender}` : ''} · {format.dateTime(notification.created_at)}
        </span>
      </span>
      {unread && <span className="mt-1.5 size-2 shrink-0 rounded-full bg-accent" aria-label="Unread" />}
    </button>
  );
}

/** The top bar's bell: the unread count and the latest notifications. */
export function NotificationBell() {
  const [open, setOpen] = useState(false);
  const unread = useQuery(unreadNotificationsQuery);
  const latest = useInfiniteQuery({ ...notificationsQuery(false), enabled: open });
  const rows = latest.data?.pages[0]?.value?.slice(0, 8) ?? [];
  const { markRead } = useNotificationActions();
  const openNotification = useOpenNotification();
  const count = unread.data ?? 0;
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="relative"
          aria-label={count ? `Notifications, ${count} unread` : 'Notifications'}
        >
          <Bell />
          {count > 0 && (
            <span className="absolute top-1 right-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-accent px-1 text-[10px] font-semibold text-white">
              {count > 99 ? '99+' : count}
            </span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-96 max-w-[calc(100vw-2rem)] p-0">
        <div className="flex items-center justify-between border-b px-3 py-2">
          <p className="text-[13px] font-semibold">Notifications</p>
          <Button
            size="sm"
            variant="ghost"
            disabled={!count || markRead.isPending}
            onClick={() => markRead.mutate(undefined)}
          >
            Mark all read
          </Button>
        </div>
        <div className="max-h-[60vh] overflow-y-auto p-1">
          {latest.isLoading && <Skeleton className="m-2 h-12" />}
          {latest.error && <Alert>{problemMessage(latest.error)}</Alert>}
          {latest.isSuccess && rows.length === 0 && (
            <p className="px-3 py-6 text-center text-[13px] text-muted">Nothing new</p>
          )}
          <ul aria-label="Latest notifications">
            {rows.map((notification) => (
              <li key={notification.id}>
                <NotificationRow
                  notification={notification}
                  onOpen={() => {
                    if (!notification.read_at) markRead.mutate([notification.id]);
                    setOpen(false);
                    void openNotification(notification);
                  }}
                />
              </li>
            ))}
          </ul>
        </div>
        <div className="border-t px-3 py-2 text-center">
          <Link to="/notifications" className="text-[13px] text-accent hover:underline" onClick={() => setOpen(false)}>
            View all notifications
          </Link>
        </div>
      </PopoverContent>
    </Popover>
  );
}
