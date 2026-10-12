import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import {
  ChevronRight,
  FilePlus,
  FolderOpen,
  FolderPlus,
  MoreHorizontal,
  Pencil,
  Plus,
  Search,
  Settings,
  Trash2,
  Upload,
  X,
} from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { api, continuation } from '@/api/http';
import { batch } from '@/api/batch';
import { listingRequest } from '@/api/requests';
import { contentTypesQuery, fieldsQuery, keys, resourceQuery, viewsQuery } from '@/api/queries';
import type { DocumentUpload, Page, Resource } from '@/api/types';
import { Page as Screen, PageHeader } from '@/components/page';
import { FormDialog } from '@/components/form-dialog';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { EmptyState, Skeleton, Spinner } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/menu';
import { Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { OperationProgress } from '@/features/admin/operation-progress';
import { uploadDocument } from '@/features/documents/transfer';
import { FieldEditor } from '@/features/fields/editor';
import { usePeopleNames } from '@/features/fields/people';
import { valuesPatch } from '@/features/fields/values';
import { ConfirmDialog } from '@/features/settings/section';
import { cn } from '@/lib/utils';
import { can } from './access';
import { CollectionIcon } from './collection-icon';
import { systemFields } from './filter-editor';
import { ItemSheet } from './item-sheet';
import { ItemsBoard, ItemsCalendar, ItemsGallery } from './items-layouts';
import { ItemsTable } from './items-table';
import { BulkEditDialog } from './operations';
import { fieldsForType } from './schema';

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
  total?: number;
  select?: string[];
}

/** Columns shown when no saved view picks them: the name, the first few columns of the default type, the last change. */
const defaultColumnCount = 4;

export function CollectionPage({
  workspaceId,
  listId,
  search,
  onSearch,
  onMoved,
}: {
  workspaceId: string;
  listId: string;
  search: CollectionSearch;
  onSearch: (patch: Partial<CollectionSearch>, replace?: boolean) => void;
  onMoved: (resource: Resource) => void;
}) {
  const queryClient = useQueryClient();
  const names = usePeopleNames();
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
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [operation, setOperation] = useState('');
  const [creating, setCreating] = useState<'item' | 'folder' | null>(null);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const rows = [...(listing.data?.items ?? []), ...more];
  const fieldList = fields.data ?? [];
  const typeList = types.data ?? [];
  const columns = (
    view?.columns?.length
      ? view.columns
      : [
          '$name',
          ...fieldsForType(fieldList, typeList)
            .slice(0, defaultColumnCount)
            .map((field) => field.key),
          '$modified_at',
        ]
  ).filter((column) => column !== '$id');
  const refresh = () => queryClient.invalidateQueries({ queryKey: keys.resources });
  const open = (resource: Resource) =>
    resource.kind === 'folder'
      ? onSearch({ folder: resource.id, item: undefined, tab: undefined })
      : onSearch({ item: resource.id, tab: undefined });

  const upload = useMutation({
    mutationFn: async (files: File[]) => {
      const done: DocumentUpload[] = [];
      for (const file of files) done.push(await uploadDocument(`/v1/resources/${listId}/documents`, file, { folder_id: search.folder }));
      return done;
    },
    onSuccess: async (results) => {
      await refresh();
      const duplicates = results.flatMap((result) => result.duplicates ?? []);
      if (duplicates.length)
        toast.warning(`Already stored elsewhere: ${duplicates.map((duplicate) => duplicate.name).join(', ')}`);
      if (results.length === 1) onSearch({ item: results[0].item.id, tab: undefined });
      else toast.success(`${results.length} files uploaded`);
    },
    onError: refresh,
  });

  const remove = useMutation({
    meta: { silent: true },
    mutationFn: () =>
      batch(
        [...selected.values()].map((resource) => ({
          id: resource.id,
          method: 'DELETE',
          url: `/v1/resources/${resource.id}${resource.kind === 'folder' ? '?recursive=true' : ''}`,
          headers: { 'If-Match': `"${resource.version}"` },
        })),
      ),
    onSuccess: async () => {
      setConfirmingDelete(false);
      await refresh();
      toast.success('Moved to the recycle bin');
    },
    onError: refresh,
  });

  async function loadMore() {
    if (!next || !listing.data) return;
    setLoadingMore(true);
    try {
      const page = listing.data.post
        ? (await api<Page<Resource>>(next, { method: 'POST', body: { include_total: true, select: listing.data.select } }))
            .data
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
        <EmptyState title="This list is not available">It was deleted, or you do not have access to it.</EmptyState>
      </Screen>
    );
  }
  const current = collection.data;
  const library = current.kind === 'library';
  const noun = library ? 'library' : 'list';
  const folders = current.allow_folders !== false;
  // Adding goes into the open folder, which can have its own permissions.
  const location = search.folder ? crumbs.data?.at(-1) : current;
  const canAdd = can(location, 'write');
  const canEdit = can(current, 'write');
  const canManage = can(current, 'manage');
  const items = [...selected.values()].filter((resource) => resource.kind === 'item');
  const startUpload = () => fileInput.current?.click();
  const uploadFiles = (files: FileList | null | undefined) => {
    const list = [...(files ?? [])];
    if (list.length) upload.mutate(list);
  };
  const hasFiles = (event: React.DragEvent) => library && canAdd && event.dataTransfer.types.includes('Files');

  return (
    <Screen wide className="max-w-[1600px]">
      <PageHeader
        title={
          <span className="flex items-center gap-2">
            <CollectionIcon collection={current} className="size-5 shrink-0 text-accent" />
            <span className="truncate">{current.name}</span>
          </span>
        }
        description={current.description}
        actions={
          <>
            {folders && canAdd && (
              <Button onClick={() => setCreating('folder')}>
                <FolderPlus /> New folder
              </Button>
            )}
            {canAdd &&
              (library ? (
                <Button variant="primary" disabled={upload.isPending} onClick={startUpload}>
                  {upload.isPending ? <Spinner className="text-accent-foreground" /> : <Upload />} Upload
                </Button>
              ) : (
                <Button variant="primary" onClick={() => setCreating('item')}>
                  <Plus /> New item
                </Button>
              ))}
            {(canEdit || canManage) && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" aria-label={`More actions for this ${noun}`}>
                  <MoreHorizontal />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                {library && canAdd && (
                  <DropdownMenuItem onSelect={() => setCreating('item')}>
                    <FilePlus /> New item without a file
                  </DropdownMenuItem>
                )}
                {canEdit && (
                  <DropdownMenuItem asChild>
                    <Link to="/w/$workspaceId/l/$listId/recycle-bin" params={{ workspaceId, listId }}>
                      <Trash2 /> Recycle bin
                    </Link>
                  </DropdownMenuItem>
                )}
                {canManage && (
                  <>
                    {canEdit && <DropdownMenuSeparator />}
                    <DropdownMenuItem asChild>
                      <Link to="/w/$workspaceId/l/$listId/settings/views" params={{ workspaceId, listId }}>
                        <Pencil /> Edit views
                      </Link>
                    </DropdownMenuItem>
                    <DropdownMenuItem asChild>
                      <Link to="/w/$workspaceId/l/$listId/settings" params={{ workspaceId, listId }}>
                        <Settings /> {library ? 'Library settings' : 'List settings'}
                      </Link>
                    </DropdownMenuItem>
                  </>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
            )}
            <input
              ref={fileInput}
              type="file"
              multiple
              className="sr-only"
              tabIndex={-1}
              aria-hidden
              accept="application/pdf,image/tiff,image/jpeg,image/png,image/webp"
              onChange={(event) => {
                uploadFiles(event.target.files);
                event.target.value = '';
              }}
            />
          </>
        }
      />

      <div className="mb-3 flex flex-wrap items-center gap-2">
        {(views.data?.length ?? 0) > 0 && (
          <div role="tablist" aria-label="Views" className="flex max-w-full overflow-x-auto rounded-md bg-surface-muted p-0.5">
            {views.data?.map((entry) => (
              <ViewTab key={entry.id} active={entry.id === viewId} onClick={() => onSearch({ view: entry.id, q: undefined })}>
                {entry.name}
              </ViewTab>
            ))}
            <ViewTab active={!viewId} onClick={() => onSearch({ view: 'none', q: undefined })}>
              All items
            </ViewTab>
          </div>
        )}
        <SearchBox key={search.q ?? ''} initial={search.q ?? ''} noun={noun} onSearch={(q) => onSearch({ q }, true)} />
      </div>

      {search.folder && (
        <nav aria-label="Folders" className="mb-3 flex flex-wrap items-center gap-1 text-[13px]">
          <Link
            to="/w/$workspaceId/l/$listId"
            params={{ workspaceId, listId }}
            search={{ view: search.view }}
            className="text-muted hover:text-foreground"
          >
            {current.name}
          </Link>
          {crumbs.data?.map((crumb, index) => (
            <span key={crumb.id} className="flex items-center gap-1">
              <ChevronRight className="size-3.5 text-muted" />
              {index === crumbs.data.length - 1 ? (
                <span aria-current="page" className="flex items-center gap-1 font-medium">
                  <FolderOpen className="size-4 text-muted" /> {crumb.name}
                </span>
              ) : (
                <button
                  type="button"
                  className="text-muted hover:text-foreground"
                  onClick={() => onSearch({ folder: crumb.id, item: undefined })}
                >
                  {crumb.name}
                </button>
              )}
            </span>
          ))}
        </nav>
      )}

      {search.q && (
        <p className="mb-3 flex items-center gap-2 text-[13px] text-muted">
          Results for “{search.q}” in {search.folder ? 'this folder' : `this ${noun}`}
          <Button size="sm" variant="ghost" onClick={() => onSearch({ q: undefined }, true)}>
            <X /> Clear search
          </Button>
        </p>
      )}

      {selected.size > 0 && (
        <div className="mb-3 flex flex-wrap items-center gap-2 rounded-lg bg-accent-soft px-3 py-2 text-[13px]">
          <span className="font-medium">{selected.size} selected</span>
          <Button size="sm" disabled={items.length === 0} onClick={() => setBulkEditing(true)}>
            <Pencil /> Edit a field
          </Button>
          <Button size="sm" disabled={remove.isPending} onClick={() => setConfirmingDelete(true)}>
            <Trash2 /> Delete
          </Button>
          <Button size="sm" variant="ghost" className="ml-auto" onClick={() => setSelected(new Map())}>
            <X /> Clear selection
          </Button>
        </div>
      )}

      <QueryFeedback query={views} />
      <div
        className={cn('relative rounded-lg', dragging && 'ring-2 ring-accent ring-offset-2')}
        onDragOver={(event) => {
          if (!hasFiles(event)) return;
          event.preventDefault();
          setDragging(true);
        }}
        onDragLeave={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false);
        }}
        onDrop={(event) => {
          if (!hasFiles(event)) return;
          event.preventDefault();
          setDragging(false);
          uploadFiles(event.dataTransfer.files);
        }}
      >
        {dragging && (
          <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-lg bg-accent-soft/80 text-sm font-medium text-accent">
            Drop to upload to {search.folder ? (crumbs.data?.at(-1)?.name ?? 'this folder') : current.name}
          </div>
        )}
        {listing.isPending ? (
          <div className="space-y-2">
            <Skeleton className="h-10" />
            <Skeleton className="h-10" />
            <Skeleton className="h-10" />
          </div>
        ) : listing.isError ? (
          <QueryFeedback query={listing} />
        ) : rows.length === 0 ? (
          <Card>
            <EmptyState
              icon={search.q ? Search : search.folder ? FolderOpen : undefined}
              title={search.q ? 'Nothing matches your search' : search.folder ? 'This folder is empty' : `This ${noun} is empty`}
            >
              {search.q ? (
                <Button className="mt-2" onClick={() => onSearch({ q: undefined }, true)}>
                  Clear search
                </Button>
              ) : !canAdd ? null : library ? (
                <>
                  <p className="mb-3">Drag files here, or upload them.</p>
                  <Button variant="primary" onClick={startUpload}>
                    <Upload /> Upload files
                  </Button>
                </>
              ) : (
                <Button variant="primary" className="mt-2" onClick={() => setCreating('item')}>
                  <Plus /> Add the first item
                </Button>
              )}
            </EmptyState>
          </Card>
        ) : layout === 'board' ? (
          <ItemsBoard
            items={rows}
            fields={fieldList.filter((field) => view?.columns?.includes(field.key))}
            groupBy={fieldList.find((field) => field.key === view?.query?.group_by)}
            readOnly={!canEdit}
            onOpen={open}
          />
        ) : layout === 'calendar' ? (
          <ItemsCalendar
            items={rows}
            dateField={[...systemFields, ...fieldList].find((field) => field.key === view?.query?.group_by)}
            onOpen={open}
          />
        ) : layout === 'gallery' ? (
          <ItemsGallery items={rows} fields={fieldList.filter((field) => view?.columns?.includes(field.key))} onOpen={open} />
        ) : (
          <ItemsTable
            rows={rows}
            columns={columns}
            fields={fieldList}
            publishing={!!current.publishing_enabled}
            names={names}
            selectable={canEdit}
            selected={selected}
            activeId={search.item}
            onToggle={toggle}
            onToggleAll={(checked) => setSelected(new Map(checked ? rows.map((row) => [row.id, row]) : []))}
            onOpen={open}
          />
        )}
      </div>
      {rows.length > 0 && (
        <div className="mt-3 flex items-center gap-3 text-xs text-muted">
          <span>
            {listing.data?.total !== undefined ? `${rows.length} of ${listing.data.total}` : `${rows.length}${next ? '+' : ''}`}{' '}
            {rows.length === 1 && listing.data?.total === undefined ? 'entry' : 'entries'}
          </span>
          {next && (
            <Button size="sm" disabled={loadingMore} onClick={() => void loadMore()}>
              {loadingMore && <Spinner />} Load more
            </Button>
          )}
        </div>
      )}
      {operation && <OperationProgress id={operation} label="Field update on selected items" />}
      {bulkEditing && (
        <BulkEditDialog
          collectionId={listId}
          ids={items.map((item) => item.id)}
          fields={fieldList}
          onClose={() => setBulkEditing(false)}
          onStarted={setOperation}
        />
      )}
      <ConfirmDialog
        open={confirmingDelete}
        onOpenChange={setConfirmingDelete}
        title={`Delete ${selected.size === 1 ? `“${[...selected.values()][0]?.name}”` : `${selected.size} entries`}?`}
        description={
          [...selected.values()].some((resource) => resource.kind === 'folder')
            ? 'Folders are deleted with everything in them. You can restore them from the recycle bin.'
            : 'You can restore them from the recycle bin.'
        }
        confirm="Delete"
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => remove.mutate()}
      />
      {creating && (
        <CreateDialog
          kind={creating}
          collection={current}
          parentId={search.folder || listId}
          onClose={() => setCreating(null)}
          onCreated={(created) => {
            setCreating(null);
            void refresh();
            open(created);
          }}
        />
      )}
      {search.item && (
        <ItemSheet
          itemId={search.item}
          collection={current}
          fields={fieldList}
          contentTypes={typeList}
          tab={search.tab ?? 'details'}
          onTab={(tab) => onSearch({ tab: tab === 'details' ? undefined : tab }, true)}
          onClose={() => onSearch({ item: undefined, tab: undefined })}
          onMoved={onMoved}
        />
      )}
    </Screen>
  );
}

function ViewTab({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={cn(
        'rounded px-2.5 py-1 text-xs font-medium whitespace-nowrap text-muted hover:text-foreground',
        active && 'bg-surface text-foreground shadow-xs',
      )}
    >
      {children}
    </button>
  );
}

/** Searches as the person types (after a short pause); Escape or the clear button empties it. */
function SearchBox({ initial, noun, onSearch }: { initial: string; noun: string; onSearch: (q: string | undefined) => void }) {
  const [value, setValue] = useState(initial);
  const latest = useRef(onSearch);
  useEffect(() => {
    latest.current = onSearch;
  });
  useEffect(() => {
    const trimmed = value.trim();
    if (trimmed === initial.trim()) return;
    const timer = setTimeout(() => latest.current(trimmed || undefined), 350);
    return () => clearTimeout(timer);
  }, [value, initial]);
  return (
    <form
      role="search"
      className="relative ml-auto w-full sm:max-w-xs"
      onSubmit={(event) => {
        event.preventDefault();
        onSearch(value.trim() || undefined);
      }}
    >
      <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted" />
      <Input
        value={value}
        onChange={(event) => setValue(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Escape' && value) {
            event.preventDefault();
            setValue('');
          }
        }}
        placeholder={`Search this ${noun}…`}
        aria-label={`Search this ${noun}`}
        className="px-8"
      />
      {value && (
        <button
          type="button"
          aria-label="Clear search"
          className="absolute top-1/2 right-2 -translate-y-1/2 rounded p-0.5 text-muted hover:bg-surface-muted hover:text-foreground"
          onClick={() => setValue('')}
        >
          <X className="size-3.5" />
        </button>
      )}
    </form>
  );
}

/** A new folder, or a new item with the columns of its content type. */
function CreateDialog({
  kind,
  collection,
  parentId,
  onClose,
  onCreated,
}: {
  kind: 'item' | 'folder';
  collection: Resource;
  parentId: string;
  onClose: () => void;
  onCreated: (resource: Resource) => void;
}) {
  const fields = useQuery(fieldsQuery(collection.id));
  const types = useQuery(contentTypesQuery(collection.id));
  const [name, setName] = useState('');
  const [type, setType] = useState('');
  const [values, setValues] = useState<Record<string, unknown>>({});
  const typeList = types.data ?? [];
  const defaultType = typeList.find((entry) => entry.is_default) ?? typeList[0];
  const chosenType = type || defaultType?.id || '';
  const visible = kind === 'item' ? fieldsForType(fields.data ?? [], typeList, chosenType) : [];
  const create = useMutation({
    mutationFn: async () =>
      (
        await api<Resource>(`/v1/resources/${parentId}/children`, {
          method: 'POST',
          body: {
            kind,
            name: name.trim(),
            tags: [],
            values: kind === 'item' ? (valuesPatch(visible, {}, values) ?? {}) : {},
            content_type_id: kind === 'item' && type ? type : undefined,
          },
        })
      ).data,
    onSuccess: onCreated,
  });
  return (
    <FormDialog
      open
      title={kind === 'folder' ? 'New folder' : 'New item'}
      submitLabel={kind === 'folder' ? 'Create folder' : 'Create item'}
      pending={create.isPending}
      submitDisabled={!name.trim()}
      onClose={onClose}
      onSubmit={() => name.trim() && create.mutate()}
    >
      <Label htmlFor="draft-name">Name</Label>
      <Input id="draft-name" value={name} onChange={(event) => setName(event.target.value)} required autoFocus />
      {kind === 'item' && typeList.length > 1 && (
        <>
          <Label htmlFor="draft-type">Content type</Label>
          <Select id="draft-type" value={chosenType} onChange={(event) => setType(event.target.value)}>
            {typeList.map((entry) => (
              <option key={entry.id} value={entry.id}>
                {entry.name}
              </option>
            ))}
          </Select>
        </>
      )}
      {visible.map((field) => (
        <FieldEditor
          key={field.id}
          field={field}
          value={values[field.key]}
          onChange={(value) => setValues((current) => ({ ...current, [field.key]: value }))}
        />
      ))}
    </FormDialog>
  );
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
    total: page.total,
    select,
  };
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
