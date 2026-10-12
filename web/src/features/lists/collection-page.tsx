import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Folder, FolderPlus, Plus, Search, Settings, Trash2, Upload } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { api, continuation } from '@/api/http';
import { batch } from '@/api/batch';
import { OperationProgress } from '@/features/admin/operation-progress';
import { BulkEditDialog, CollectionOperations } from './operations';
import { listingRequest } from '@/api/requests';
import { contentTypesQuery, fieldsQuery, keys, resourceQuery, viewsQuery } from '@/api/queries';
import type { FieldDefinition, Page, Resource } from '@/api/types';
import { Page as Screen, PageHeader } from '@/components/page';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { uploadDocument } from '@/features/documents/transfer';
import { FieldDisplay } from '@/features/fields/display';
import { FieldEditor } from '@/features/fields/editor';
import { valuesPatch } from '@/features/fields/values';
import { useFormat } from '@/lib/preferences';
import { CollectionSettings } from './collection-settings';
import { ItemSheet } from './item-sheet';
import { ItemsBoard, ItemsCalendar, ItemsGallery } from './items-layouts';
import { systemFields } from './filter-editor';
import { QueryFeedback } from '@/features/admin/common';
import { columnLabel, fieldsForType, publishState } from './schema';

export interface CollectionSearch {
  folder?: string;
  view?: string;
  item?: string;
  tab?: string;
  q?: string;
}

interface Listing {
  items: Resource[];
  next: string | null;
  post: boolean;
  select?: string[];
}

export function CollectionPage({
  workspaceId,
  listId,
  search,
  onSearch,
  onLeave,
  onMoved,
}: {
  workspaceId: string;
  listId: string;
  search: CollectionSearch;
  onSearch: (patch: Partial<CollectionSearch>) => void;
  onLeave: () => void;
  onMoved: (resource: Resource) => void;
}) {
  const format = useFormat();
  const queryClient = useQueryClient();
  const collection = useQuery(resourceQuery(listId));
  const fields = useQuery(fieldsQuery(listId));
  const types = useQuery(contentTypesQuery(listId));
  const views = useQuery(viewsQuery(listId));
  const crumbs = useQuery({
    queryKey: ['resources', listId, 'crumbs', search.folder],
    enabled: !!search.folder,
    queryFn: () => folderCrumbs(search.folder!, listId),
  });
  const viewId = search.view === 'none' ? undefined : (search.view ?? views.data?.find((view) => view.is_default)?.id);
  const view = views.data?.find((entry) => entry.id === viewId);
  const layout = !search.folder && !search.q ? (view?.layout ?? 'table') : 'table';
  const projection =
    layout === 'table'
      ? undefined
      : [...new Set(['$name', ...(view?.columns ?? []), ...(view?.query?.group_by ? [view.query.group_by] : [])])];
  const listing = useQuery({
    queryKey: ['resources', listId, 'rows', search.folder ?? '', viewId ?? '', view, search.q ?? '', projection],
    enabled: !!collection.data && views.isSuccess,
    queryFn: () => loadListing(listId, search.folder, search.q ? undefined : viewId, search.q, projection),
  });
  const [snapshot, setSnapshot] = useState(listing.data);
  const [more, setMore] = useState<Resource[]>([]);
  const [next, setNext] = useState<string | null>(listing.data?.next ?? null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [selected, setSelected] = useState<Map<string, Resource>>(new Map());
  if (listing.data !== snapshot) {
    setSnapshot(listing.data);
    setMore([]);
    setNext(listing.data?.next ?? null);
    setSelected(new Map());
  }
  const [bulkEditing, setBulkEditing] = useState(false);
  const [operation, setOperation] = useState('');
  const [settings, setSettings] = useState(false);
  const [creating, setCreating] = useState<'item' | 'folder' | null>(null);
  const [draftName, setDraftName] = useState('');
  const [draftType, setDraftType] = useState('');
  const [draftValues, setDraftValues] = useState<Record<string, unknown>>({});

  const rows = [...(listing.data?.items ?? []), ...more];
  const columns = (view?.columns?.length ? view.columns : ['$name', '$modified_at', '$tags']).filter(
    (column) => column !== '$id',
  );
  const fieldList = fields.data ?? [];
  const parentId = search.folder || listId;
  const draftFields = fieldsForType(fieldList, types.data ?? [], draftType);

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: keys.resources });
  };

  const create = useMutation({
    mutationFn: () =>
      api<Resource>(`/v1/resources/${parentId}/children`, {
        method: 'POST',
        body: {
          kind: creating,
          name: draftName.trim(),
          tags: [],
          values: creating === 'item' ? (valuesPatch(draftFields, {}, draftValues) ?? {}) : {},
          content_type_id: creating === 'item' && draftType ? draftType : undefined,
        },
      }),
    onSuccess: async (result) => {
      await refresh();
      setCreating(null);
      setDraftName('');
      setDraftValues({});
      if (result.data.kind === 'item') onSearch({ item: result.data.id, tab: 'details' });
      else onSearch({ folder: result.data.id, item: undefined });
    },
  });

  const upload = useMutation({
    mutationFn: (file: File) => uploadDocument(`/v1/resources/${listId}/documents`, file, { folder_id: search.folder }),
    onSuccess: async (result) => {
      await refresh();
      const duplicates = result.duplicates ?? [];
      if (duplicates.length)
        toast.warning(`Possible duplicates: ${duplicates.map((duplicate) => duplicate.name).join(', ')}`);
      onSearch({ item: result.item.id, tab: 'details' });
    },
  });

  const remove = useMutation({
    mutationFn: async () => {
      await batch(
        [...selected.values()].map((resource) => ({
          id: resource.id,
          method: 'DELETE',
          url: `/v1/resources/${resource.id}${resource.kind === 'folder' ? '?recursive=true' : ''}`,
          headers: { 'If-Match': `"${resource.version}"` },
        })),
      );
    },
    onSuccess: async () => {
      setSelected(new Map());
      await refresh();
    },
    onError: refresh,
  });

  async function loadMore() {
    if (!next || !listing.data) return;
    setLoadingMore(true);
    try {
      const page = listing.data.post
        ? (
            await api<Page<Resource>>(next, {
              method: 'POST',
              body: { include_total: true, select: listing.data.select },
            })
          ).data
        : (await api<Page<Resource>>(next)).data;
      setMore((current) => [...current, ...(page.value ?? [])]);
      setNext(continuation(page['@odata.nextLink']));
    } finally {
      setLoadingMore(false);
    }
  }

  function toggle(resource: Resource, checked: boolean) {
    setSelected((current) => {
      const next = new Map(current);
      if (checked) next.set(resource.id, resource);
      else next.delete(resource.id);
      return next;
    });
  }

  if (collection.isPending) {
    return (
      <Screen wide>
        <Skeleton className="h-10 w-64" />
      </Screen>
    );
  }
  if (!collection.data) {
    return (
      <Screen>
        <EmptyState title="Collection not found" />
      </Screen>
    );
  }
  const current = collection.data;

  return (
    <Screen wide>
      <PageHeader
        title={current.name}
        description={current.description || (current.kind === 'library' ? 'Library' : 'List')}
        actions={
          <>
            {current.kind === 'library' && (
              <Button asChild>
                <label className="cursor-pointer">
                  <Upload /> Upload
                  <input
                    type="file"
                    className="sr-only"
                    disabled={upload.isPending}
                    onChange={(event) => {
                      const file = event.target.files?.[0];
                      if (file) upload.mutate(file);
                      event.target.value = '';
                    }}
                  />
                </label>
              </Button>
            )}
            {current.allow_folders !== false && (
              <Button onClick={() => setCreating('folder')}>
                <FolderPlus /> Folder
              </Button>
            )}
            <Button variant="primary" onClick={() => setCreating('item')}>
              <Plus /> Item
            </Button>
            <Button asChild>
              <Link to="/w/$workspaceId/l/$listId/recycle-bin" params={{ workspaceId, listId }}>
                Recycle bin
              </Link>
            </Button>
            <Button onClick={() => setSettings(true)} aria-label="Collection settings">
              <Settings />
            </Button>
          </>
        }
      />
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Link
          to="/w/$workspaceId/l/$listId"
          params={{ workspaceId, listId }}
          search={{ view: search.view, q: search.q }}
          className="text-xs text-muted hover:text-foreground"
        >
          {current.name}
        </Link>
        {crumbs.data?.map((crumb) => (
          <span key={crumb.id} className="flex items-center gap-2 text-xs">
            <span className="text-muted">/</span>
            <button
              type="button"
              className="text-muted hover:text-foreground"
              onClick={() => onSearch({ folder: crumb.id, item: undefined })}
            >
              {crumb.name}
            </button>
          </span>
        ))}
      </div>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Button size="sm" variant={!viewId ? 'primary' : 'secondary'} onClick={() => onSearch({ view: 'none' })}>
          All
        </Button>
        {views.data?.map((entry) => (
          <Button
            key={entry.id}
            size="sm"
            variant={entry.id === viewId ? 'primary' : 'secondary'}
            onClick={() => onSearch({ view: entry.id })}
          >
            {entry.name}
          </Button>
        ))}
        <Button size="sm" asChild>
          <Link to="/w/$workspaceId/l/$listId/views" params={{ workspaceId, listId }}>
            Manage views
          </Link>
        </Button>
        <QueryBox key={search.q ?? ''} initial={search.q ?? ''} onSearch={(q) => onSearch({ q })} />
      </div>
      {!!search.q && viewId && (
        <p className="mb-2 text-xs text-muted">
          Search looks through the folder’s children. Clear it to return to the saved view.
        </p>
      )}
      {selected.size > 0 && (
        <div className="mb-3 flex items-center gap-2">
          <span className="text-[13px]">{selected.size} selected</span>
          <Button
            size="sm"
            disabled={[...selected.values()].every((item) => item.kind !== 'item')}
            onClick={() => setBulkEditing(true)}
          >
            Edit selected items
          </Button>
          <Button size="sm" variant="danger" disabled={remove.isPending} onClick={() => remove.mutate()}>
            <Trash2 /> Delete
          </Button>
        </div>
      )}
      <QueryFeedback query={views} />
      <QueryFeedback query={listing} />
      {layout !== 'table' && (
        <p className="mb-2 text-xs text-muted">
          Showing {rows.length} loaded entries{next ? '. Load more to include additional entries.' : '.'}
        </p>
      )}
      {layout === 'board' ? (
        <ItemsBoard
          items={rows}
          fields={fieldList.filter((field) => view?.columns?.includes(field.key))}
          groupBy={fieldList.find((field) => field.key === view?.query?.group_by)}
          onOpen={(item) =>
            item.kind === 'folder'
              ? onSearch({ folder: item.id, item: undefined })
              : onSearch({ item: item.id, tab: 'details' })
          }
        />
      ) : layout === 'calendar' ? (
        <ItemsCalendar
          items={rows}
          dateField={[...systemFields, ...fieldList].find((field) => field.key === view?.query?.group_by)}
          onOpen={(item) =>
            item.kind === 'folder'
              ? onSearch({ folder: item.id, item: undefined })
              : onSearch({ item: item.id, tab: 'details' })
          }
        />
      ) : layout === 'gallery' ? (
        <ItemsGallery
          items={rows}
          fields={fieldList.filter((field) => view?.columns?.includes(field.key))}
          onOpen={(item) =>
            item.kind === 'folder'
              ? onSearch({ folder: item.id, item: undefined })
              : onSearch({ item: item.id, tab: 'details' })
          }
        />
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-surface">
          <table className="w-full text-left text-[13px]">
            <thead className="border-b text-xs text-muted">
              <tr>
                <th className="w-10 px-3 py-2" />
                {columns.map((column) => (
                  <th key={column} className="px-3 py-2 font-medium">
                    {columnLabel(column, fieldList)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {listing.isPending && (
                <tr>
                  <td colSpan={columns.length + 1} className="px-3 py-4">
                    <Skeleton className="h-8" />
                  </td>
                </tr>
              )}
              {rows.map((resource) => {
                const state = publishState(resource, !!current.publishing_enabled);
                return (
                  <tr key={resource.id} className="border-b last:border-0 hover:bg-surface-muted/50">
                    <td className="px-3 py-2">
                      <Checkbox
                        checked={selected.has(resource.id)}
                        onChange={(event) => toggle(resource, event.target.checked)}
                        aria-label={`Select ${resource.name}`}
                      />
                    </td>
                    {columns.map((column) => (
                      <td key={column} className="max-w-xs px-3 py-2 align-top">
                        {column === '$name' ? (
                          <button
                            type="button"
                            className="inline-flex items-center gap-2 text-left font-medium hover:underline"
                            onClick={() =>
                              resource.kind === 'folder'
                                ? onSearch({ folder: resource.id, item: undefined })
                                : onSearch({ item: resource.id, tab: 'details' })
                            }
                          >
                            {resource.kind === 'folder' && <Folder className="size-4 text-muted" />}
                            <span className="truncate">{resource.name}</span>
                            {state && <Badge tone={state === 'Published' ? 'success' : 'warning'}>{state}</Badge>}
                          </button>
                        ) : (
                          <Cell
                            column={column}
                            resource={resource}
                            fields={fieldList}
                            formatDate={(value) => format.dateTime(value)}
                          />
                        )}
                      </td>
                    ))}
                  </tr>
                );
              })}
            </tbody>
          </table>
          {!listing.isPending && rows.length === 0 && <EmptyState icon={Folder} title="Nothing here" />}
        </div>
      )}
      {next && (
        <Button className="mt-3" disabled={loadingMore} onClick={() => void loadMore()}>
          {loadingMore ? 'Loading…' : 'Load more'}
        </Button>
      )}
      {operation && <OperationProgress id={operation} label="Bulk field update" />}
      <CollectionOperations collectionId={listId} />
      {bulkEditing && (
        <BulkEditDialog
          collectionId={listId}
          ids={[...selected.values()].filter((item) => item.kind === 'item').map((item) => item.id)}
          fields={fieldList}
          onClose={() => setBulkEditing(false)}
          onStarted={setOperation}
        />
      )}
      {creating && (
        <FormDialog
          open
          title={creating === 'folder' ? 'New folder' : 'New item'}
          submitLabel="Create"
          pending={create.isPending}
          onClose={() => setCreating(null)}
          onSubmit={() => draftName.trim() && create.mutate()}
        >
          <Label htmlFor="draft-name">Name</Label>
          <Input
            id="draft-name"
            value={draftName}
            onChange={(event) => setDraftName(event.target.value)}
            required
            autoFocus
          />
          {creating === 'item' && (types.data?.length ?? 0) > 0 && (
            <>
              <Label htmlFor="draft-type">Content type</Label>
              <Select id="draft-type" value={draftType} onChange={(event) => setDraftType(event.target.value)}>
                <option value="">Default</option>
                {types.data?.map((type) => (
                  <option key={type.id} value={type.id}>
                    {type.name}
                  </option>
                ))}
              </Select>
            </>
          )}
          {creating === 'item' &&
            draftFields.map((field) => (
              <FieldEditor
                key={field.id}
                field={field}
                value={draftValues[field.key]}
                onChange={(value) => setDraftValues((current) => ({ ...current, [field.key]: value }))}
              />
            ))}
        </FormDialog>
      )}
      {settings && (
        <CollectionSettings
          key={current.version}
          collection={current}
          open
          onClose={() => setSettings(false)}
          onDeleted={onLeave}
        />
      )}
      {search.item && (
        <ItemSheet
          itemId={search.item}
          collection={current}
          fields={fieldList}
          contentTypes={types.data ?? []}
          tab={search.tab ?? 'details'}
          onTab={(tab) => onSearch({ tab })}
          onClose={() => onSearch({ item: undefined, tab: undefined })}
          onMoved={onMoved}
        />
      )}
    </Screen>
  );
}

function Cell({
  column,
  resource,
  fields,
  formatDate,
}: {
  column: string;
  resource: Resource;
  fields: FieldDefinition[];
  formatDate: (value: string) => string;
}) {
  if (column === '$tags') return <span className="text-muted">{(resource.tags ?? []).join(', ') || '—'}</span>;
  if (column === '$modified_at' || column === '$created_at') {
    const value = column === '$modified_at' ? resource.updated_at : resource.created_at;
    return <span className="text-muted">{value ? formatDate(value) : '—'}</span>;
  }
  if (column === '$modified_by' || column === '$created_by') {
    return (
      <span className="text-muted">
        {(column === '$modified_by' ? resource.updated_by : resource.created_by) || '—'}
      </span>
    );
  }
  const field = fields.find((entry) => entry.key === column);
  if (!field) return <span className="text-muted">—</span>;
  return <FieldDisplay field={field} value={resource.values?.[column]} />;
}

async function loadListing(
  collectionId: string,
  folder: string | undefined,
  viewId: string | undefined,
  q: string | undefined,
  select?: string[],
): Promise<Listing> {
  const request = listingRequest(collectionId, folder, viewId, q);
  const page = (
    await api<Page<Resource>>(request.path, {
      method: request.method,
      query: request.query,
      body: request.method === 'POST' && select ? { include_total: true, select } : request.body,
    })
  ).data;
  return {
    items: page.value ?? [],
    next: continuation(page['@odata.nextLink']),
    post: request.method === 'POST',
    select,
  };
}

function QueryBox({ initial, onSearch }: { initial: string; onSearch: (q: string | undefined) => void }) {
  const [value, setValue] = useState(initial);
  return (
    <form
      className="relative ml-auto min-w-48 flex-1 sm:max-w-xs"
      onSubmit={(event) => {
        event.preventDefault();
        const trimmed = value.trim();
        onSearch(trimmed || undefined);
      }}
    >
      <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted" />
      <Input
        value={value}
        onChange={(event) => setValue(event.target.value)}
        placeholder="Search this collection"
        aria-label="Search this collection"
        className="pl-7"
      />
    </form>
  );
}

async function folderCrumbs(folderId: string, collectionId: string): Promise<Resource[]> {
  const chain: Resource[] = [];
  const seen = new Set<string>();
  let id: string | undefined = folderId;
  while (id && id !== collectionId && !seen.has(id)) {
    seen.add(id);
    const loaded: Resource = (await api<Resource>(`/v1/resources/${id}`)).data;
    chain.unshift(loaded);
    id = loaded.parent_id;
  }
  return chain;
}
