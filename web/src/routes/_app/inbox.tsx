import { useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { Inbox as InboxIcon, Upload } from 'lucide-react';
import { useState } from 'react';
import { api, continuation } from '@/api/http';
import { contentTypesQuery, fieldsQuery, inboxesQuery, keys, resourceQuery } from '@/api/queries';
import type { Page as ApiPage, Resource } from '@/api/types';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { useUploads } from '@/features/documents/uploads';
import { ItemSheet } from '@/features/lists/item-sheet';
import { useFormat } from '@/lib/preferences';
import { textParam } from '@/lib/search';
import { cn } from '@/lib/utils';

interface InboxSearch {
  inbox?: string;
  item?: string;
  tab?: string;
}

export const Route = createFileRoute('/_app/inbox')({
  validateSearch: (search: Record<string, unknown>): InboxSearch => ({
    inbox: textParam(search.inbox),
    item: textParam(search.item),
    tab: textParam(search.tab),
  }),
  component: InboxPage,
});

function InboxPage() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const inboxes = useQuery(inboxesQuery);
  const selected = inboxes.data?.find((inbox) => inbox.library_id === search.inbox) ?? inboxes.data?.[0];
  const library = useQuery({ ...resourceQuery(selected?.library_id ?? ''), enabled: !!selected });
  const fields = useQuery({ ...fieldsQuery(selected?.library_id ?? ''), enabled: !!selected });
  const types = useQuery({ ...contentTypesQuery(selected?.library_id ?? ''), enabled: !!selected });
  const children = useQuery({
    queryKey: ['resources', selected?.library_id, 'inbox-children'],
    enabled: !!selected,
    queryFn: async () => (await api<ApiPage<Resource>>(`/v1/resources/${selected!.library_id}/children`, { query: { $top: 50 } })).data,
  });
  const [childSnapshot, setChildSnapshot] = useState(children.data);
  const [more, setMore] = useState<Resource[]>([]);
  const [next, setNext] = useState<string | null>(continuation(children.data?.['@odata.nextLink']));
  const [loadingMore, setLoadingMore] = useState(false);
  if (children.data !== childSnapshot) {
    setChildSnapshot(children.data);
    setMore([]);
    setNext(continuation(children.data?.['@odata.nextLink']));
  }
  const format = useFormat();
  const queryClient = useQueryClient();
  const { upload } = useUploads();
  const uploadFiles = (files: FileList | null) => {
    const list = [...(files ?? [])];
    if (!selected || !list.length) return;
    const inbox = selected;
    void upload(list, {
      url: inbox.group_id ? `/v1/identity/groups/${inbox.group_id}/inbox/documents` : '/v1/identity/me/inbox/documents',
      label: inbox.group_name ? `${inbox.group_name} inbox` : 'your inbox',
      metadata: (file) => ({ name: file.name }),
      open: (itemId) => ({ to: '/inbox', search: { inbox: inbox.library_id, item: itemId } }),
    }).then((results) => {
      if (list.length === 1 && results.length === 1)
        void navigate({ search: (current) => ({ ...current, inbox: inbox.library_id, item: results[0].item.id, tab: 'details' }) });
    });
  };

  const rows = [...(children.data?.value ?? []), ...more];

  return (
    <Page>
      <PageHeader
        icon={InboxIcon}
        title="Inbox"
        description="Files waiting to be sorted."
        actions={
          selected && (
            <Button asChild variant="primary">
              <label className="cursor-pointer">
                <Upload /> Upload
                <input
                  type="file"
                  multiple
                  className="sr-only"
                  onChange={(event) => {
                    uploadFiles(event.target.files);
                    event.target.value = '';
                  }}
                />
              </label>
            </Button>
          )
        }
      />
      {inboxes.isPending ? (
        <Skeleton className="h-16" />
      ) : !inboxes.data?.length ? (
        <EmptyState icon={InboxIcon} title="No inbox yet" />
      ) : (
        <div className="grid gap-4 lg:grid-cols-[16rem_1fr]">
          <ul className="flex gap-2 overflow-auto lg:flex-col">
            {inboxes.data.map((inbox) => (
              <li key={inbox.library_id}>
                <button
                  type="button"
                  className={cn(
                    'w-full rounded-md px-3 py-2 text-left text-[13px] hover:bg-surface-muted',
                    selected?.library_id === inbox.library_id && 'bg-accent-soft font-medium text-accent',
                  )}
                  onClick={() => void navigate({ search: { inbox: inbox.library_id } })}
                >
                  <span className="block truncate">{inbox.group_name || inbox.library_name}</span>
                  <span className="text-xs text-muted">{inbox.kind === 'group' ? 'Group' : 'Personal'}</span>
                </button>
              </li>
            ))}
          </ul>
          <div>
            {selected && (
              <Link to="/w/$workspaceId/l/$listId" params={{ workspaceId: selected.workspace_id, listId: selected.library_id }} className="mb-3 inline-block text-xs font-medium text-accent hover:underline">
                Open {selected.library_name}
              </Link>
            )}
            <ul className="overflow-hidden rounded-lg border bg-surface">
              {children.isPending && <li className="p-4"><Skeleton className="h-8" /></li>}
              {rows.map((item) => (
                <li key={item.id} className="border-b last:border-0">
                  <button
                    type="button"
                    className="flex w-full items-center gap-3 px-4 py-2.5 text-left hover:bg-surface-muted"
                    onClick={() => void navigate({ search: (current) => ({ ...current, inbox: selected?.library_id, item: item.id, tab: 'details' }) })}
                  >
                    <span className="flex-1 truncate text-sm font-medium">{item.name}</span>
                    <span className="text-xs text-muted">{item.updated_at ? format.dateTime(item.updated_at) : ''}</span>
                  </button>
                </li>
              ))}
              {!children.isPending && rows.length === 0 && <EmptyState title="Nothing to file" />}
            </ul>
            {next && (
              <Button
                className="mt-3"
                disabled={loadingMore}
                onClick={() => {
                  const link = next;
                  setLoadingMore(true);
                  void api<ApiPage<Resource>>(link)
                    .then((result) => {
                      setMore((current) => [...current, ...(result.data.value ?? [])]);
                      setNext(continuation(result.data['@odata.nextLink']));
                    })
                    .finally(() => setLoadingMore(false));
                }}
              >
                {loadingMore ? 'Loading…' : 'Load more'}
              </Button>
            )}
          </div>
        </div>
      )}
      {search.item && library.data && (
        <ItemSheet
          itemId={search.item}
          collection={library.data}
          fields={fields.data ?? []}
          contentTypes={types.data ?? []}
          tab={search.tab ?? 'details'}
          onTab={(tab) => void navigate({ search: (current) => ({ ...current, tab }) })}
          onClose={() => void navigate({ search: (current) => ({ ...current, item: undefined, tab: undefined }) })}
          onMoved={() => void queryClient.invalidateQueries({ queryKey: keys.resources })}
        />
      )}
    </Page>
  );
}
