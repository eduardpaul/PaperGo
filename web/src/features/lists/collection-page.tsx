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
import { countRequest, listingQuery, listingRequest, type ApiCall } from '@/api/requests';
import { contentTypesQuery, fieldsQuery, keys, resourceQuery, viewsQuery } from '@/api/queries';
import type { Page, QuerySpec, Resource } from '@/api/types';
import { Page as Screen, PageHeader } from '@/components/page';
import { FormDialog } from '@/components/form-dialog';
import { TagInput } from '@/components/tag-input';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { EmptyState, Kbd, Skeleton, Spinner } from '@/components/ui/feedback';
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
import { useUploads } from '@/features/documents/uploads';
import { FieldEditor } from '@/features/fields/editor';
import { useResourceNames, useValueNames } from '@/features/fields/names';
import { valuesPatch } from '@/features/fields/values';
import { ConfirmDialog } from '@/features/settings/section';
import { useFormat } from '@/lib/preferences';
import { RunWorkflowAction } from '@/features/workflows/item-workflows';
import { cn } from '@/lib/utils';
import { can } from './access';
import { CollectionIcon } from './collection-icon';
import { systemFields } from './filter-editor';
import { ItemSheet } from './item-sheet';
import { ItemsBoard, ItemsCalendar, ItemsGallery } from './items-layouts';
import { ItemsTable } from './items-table';
import { BulkEditDialog } from './operations';
import { ColumnPicker } from './column-picker';
import { columnLabel, fieldsForType, parseSort, sortableColumns, sortParam } from './schema';

export interface CollectionSearch {
  folder?: string;
  view?: string;
  item?: string;
  tab?: string;
  q?: string;
  /** A column key, "-" first for descending. */
  sort?: string;
  /** Columns chosen on the page, comma separated, instead of the view's. */
  cols?: string;
}

interface Listing {
  items: Resource[];
  next: string | null;
  /** A POST query continues with the same body. */
  body?: unknown;
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
  const format = useFormat();
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
  const sortable = sortableColumns(fields.data ?? []);
  // A sort from an old link on a column that is no longer sortable is ignored rather than failing the list.
  const requestedSort = parseSort(search.sort);
  const sort = requestedSort && sortable.has(requestedSort.field) ? requestedSort : undefined;
  const columnChoices = [...systemFields.filter((field) => field.key !== '$id'), ...(fields.data ?? [])].map((field) => ({
    key: field.key,
    label: columnLabel(field.key, fields.data ?? []),
  }));
  const chosenColumns = search.cols
    ?.split(',')
    .filter((key) => columnChoices.some((choice) => choice.key === key));
  const pageColumns = chosenColumns?.length ? [...new Set(['$name', ...chosenColumns])] : undefined;
  const request = listingRequest(listId, { folder: search.folder, view, q: search.q, sort, columns: pageColumns });
  // Boards and calendars load their own rows: per column, per month.
  const ownRows = !!view && (layout === 'board' || layout === 'calendar');
  const listing = useQuery({
    queryKey: ['resources', listId, 'rows', request],
    enabled: !!collection.data && views.isSuccess && (!search.sort || fields.isSuccess) && !ownRows,
    queryFn: () => loadListing(request),
  });
  const [snapshot, setSnapshot] = useState(listing.data);
  const [more, setMore] = useState<Resource[]>([]);
  const [next, setNext] = useState<string | null>(listing.data?.next ?? null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [selected, setSelected] = useState<Map<string, Resource>>(new Map());
  // Everything the listing matches, beyond the loaded rows (for bulk edits).
  const [allMatching, setAllMatching] = useState(false);
  if (listing.data !== snapshot) {
    setSnapshot(listing.data);
    setMore([]);
    setNext(listing.data?.next ?? null);
    setSelected(new Map());
    setAllMatching(false);
  }
  const [bulkEditing, setBulkEditing] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [operations, setOperations] = useState<string[]>([]);
  const [creating, setCreating] = useState<'item' | 'folder' | null>(null);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const rows = [...(listing.data?.items ?? []), ...more];
  // The count comes after the first page, and only when there is more than one page.
  const counted = useMatchingTotal(listId, listingQuery(request, listId), !ownRows && !!listing.data?.next);
  // Search hits can sit in any folder below; each shows where.
  const folderIds = search.q
    ? [...new Set(rows.flatMap((row) => (row.parent_id && row.parent_id !== listId ? [row.parent_id] : [])))].sort()
    : [];
  const folderNames = useResourceNames(folderIds);
  const names = useValueNames(fields.data ?? [], rows);
  const fieldList = fields.data ?? [];
  const typeList = types.data ?? [];
  const columns = (
    pageColumns ??
    (view?.columns?.length
      ? view.columns
      : [
          '$name',
          ...fieldsForType(fieldList, typeList)
            .slice(0, defaultColumnCount)
            .map((field) => field.key),
          '$modified_at',
        ])
  ).filter((column) => column !== '$id');
  const refresh = () => queryClient.invalidateQueries({ queryKey: keys.resources });
  // Opening a folder found by a search shows the folder, not the search inside it.
  const open = (resource: Resource) =>
    resource.kind === 'folder'
      ? onSearch({ folder: resource.id, item: undefined, tab: undefined, q: undefined })
      : onSearch({ item: resource.id, tab: undefined });

  const { upload } = useUploads();
  // A finished single upload opens its item, unless the person has left this list meanwhile.
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

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
      const page = listing.data.body
        ? (await api<Page<Resource>>(next, { method: 'POST', body: listing.data.body })).data
        : (await api<Page<Resource>>(next)).data;
      setMore((current) => [...current, ...(page.value ?? [])]);
      setNext(continuation(page['@odata.nextLink']));
    } finally {
      setLoadingMore(false);
    }
  }

  function toggle(resource: Resource, checked: boolean) {
    setAllMatching(false);
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
  const matching = listingQuery(request, listId);
  const allLoadedSelected = rows.length > 0 && selected.size === rows.length;
  const startUpload = () => fileInput.current?.click();
  const uploadFiles = (files: FileList | null | undefined) => {
    const list = [...(files ?? [])];
    if (!list.length) return;
    const folder = search.folder;
    void upload(list, {
      url: `/v1/resources/${listId}/documents`,
      label: folder ? (crumbs.data?.at(-1)?.name ?? 'the folder') : current.name,
      metadata: () => ({ folder_id: folder }),
      open: (itemId) => ({
        to: '/w/$workspaceId/l/$listId',
        params: { workspaceId, listId },
        search: folder ? { folder, item: itemId } : { item: itemId },
      }),
    }).then((results) => {
      if (list.length === 1 && results.length === 1 && mounted.current) onSearch({ item: results[0].item.id, tab: undefined });
    });
  };
  const hasFiles = (event: React.DragEvent) => {
    const transfer = event.dataTransfer;
    if (!library || !canAdd || !transfer) return false;
    return Array.from(transfer.types).some((type) => ['files', 'application/x-moz-file'].includes(type.toLowerCase()))
      || Array.from(transfer.items ?? []).some((item) => item.kind === 'file')
      || transfer.files?.length > 0;
  };

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
                <Button variant="primary" onClick={startUpload}>
                  <Upload /> Upload
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
              <ViewTab key={entry.id} active={entry.id === viewId} onClick={() => onSearch({ view: entry.id, q: undefined, sort: undefined, cols: undefined })}>
                {entry.name}
              </ViewTab>
            ))}
            <ViewTab active={!viewId} onClick={() => onSearch({ view: 'none', q: undefined, sort: undefined, cols: undefined })}>
              All items
            </ViewTab>
          </div>
        )}
        {layout === 'table' && (
          <ColumnPicker
            collectionId={listId}
            columns={columns}
            available={columnChoices}
            changed={!!pageColumns}
            view={view}
            sort={sort}
            canManage={canManage}
            onChange={(next) => onSearch({ cols: next?.join(',') }, true)}
            onSaved={(id) => onSearch({ view: id, cols: undefined, sort: undefined })}
          />
        )}
        <SearchBox initial={search.q ?? ''} noun={noun} onSearch={(q) => onSearch({ q }, true)} />
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
          Results for “{search.q}” in {search.folder ? 'this folder and its subfolders' : `the whole ${noun}`}
          {search.folder && (
            <Button size="sm" variant="ghost" onClick={() => onSearch({ folder: undefined, item: undefined }, true)}>
              <Search /> Search the whole {noun}
            </Button>
          )}
          <Button size="sm" variant="ghost" onClick={() => onSearch({ q: undefined }, true)}>
            <X /> Clear search
          </Button>
        </p>
      )}

      {selected.size > 0 && (
        <div className="mb-3 flex flex-wrap items-center gap-2 rounded-lg bg-accent-soft px-3 py-2 text-[13px]">
          <span className="font-medium">
            {allMatching ? <MatchingCount collectionId={listId} query={matching} /> : `${selected.size} selected`}
          </span>
          {!allMatching && allLoadedSelected && next && canEdit && (
            <Button size="sm" variant="ghost" className="text-accent" onClick={() => setAllMatching(true)}>
              Select all <MatchingCount collectionId={listId} query={matching} bare />
            </Button>
          )}
          <Button size="sm" disabled={!allMatching && items.length === 0} onClick={() => setBulkEditing(true)}>
            <Pencil /> Edit a field
          </Button>
          {!allMatching && canEdit && items.length === selected.size && (
            <RunWorkflowAction workspaceId={workspaceId} collectionId={listId} items={items} />
          )}
          {!allMatching && (
            <Button size="sm" disabled={remove.isPending} onClick={() => setConfirmingDelete(true)}>
              <Trash2 /> Delete
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            className="ml-auto"
            onClick={() => {
              setSelected(new Map());
              setAllMatching(false);
            }}
          >
            <X /> Clear selection
          </Button>
        </div>
      )}

      <QueryFeedback query={views} />
      <div
        role="region"
        aria-label={library ? 'Library contents' : 'List contents'}
        className={cn('relative rounded-lg', dragging && 'ring-2 ring-accent ring-offset-2')}
        onDragOver={(event) => {
          if (!hasFiles(event)) return;
          event.preventDefault();
          event.dataTransfer.dropEffect = 'copy';
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
        {ownRows && layout === 'board' ? (
          <ItemsBoard
            collectionId={listId}
            view={view}
            fields={fieldList.filter((field) => view.columns?.includes(field.key))}
            groupBy={fieldList.find((field) => field.key === view.query?.group_by)}
            readOnly={!canEdit}
            onOpen={open}
          />
        ) : ownRows ? (
          <ItemsCalendar
            collectionId={listId}
            view={view}
            dateField={[...systemFields, ...fieldList].find((field) => field.key === view.query?.group_by)}
            onOpen={open}
          />
        ) : listing.isPending ? (
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
        ) : layout === 'gallery' ? (
          <ItemsGallery
            items={rows}
            fields={fieldList.filter((field) => view?.columns?.includes(field.key))}
            names={names}
            onOpen={open}
          />
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
            sort={sort}
            sortable={sortable}
            onSort={(next) => onSearch({ sort: sortParam(next) }, true)}
            onToggle={toggle}
            onToggleAll={(checked) => {
              setAllMatching(false);
              setSelected(new Map(checked ? rows.map((row) => [row.id, row]) : []));
            }}
            locations={search.q ? folderNames : undefined}
            onOpen={open}
          />
        )}
      </div>
      {rows.length > 0 && !ownRows && (
        <div className="mt-3 flex items-center gap-3 text-xs text-muted">
          <span>
            {!next
              ? `${format.number(rows.length)} ${rows.length === 1 ? 'entry' : 'entries'}`
              : counted
                ? `${format.number(rows.length)} of ${format.number(counted.total)}${counted.capped ? '+' : ''} entries`
                : `${format.number(rows.length)}+ entries`}
          </span>
          {next && (
            <Button size="sm" disabled={loadingMore} onClick={() => void loadMore()}>
              {loadingMore && <Spinner />} Load more
            </Button>
          )}
          {layout === 'table' && (
            <span className="ml-auto hidden md:inline">
              <Kbd>↑</Kbd> <Kbd>↓</Kbd> move · <Kbd>Enter</Kbd> open{canEdit && <> · <Kbd>x</Kbd> select</>} · <Kbd>/</Kbd>{' '}
              search
            </span>
          )}
        </div>
      )}
      {operations.map((id, index) => (
        <OperationProgress
          key={id}
          id={id}
          label={`Field update${operations.length > 1 ? ` (part ${index + 1} of ${operations.length})` : ''}`}
        />
      ))}
      {bulkEditing && (
        <BulkEditDialog
          collectionId={listId}
          target={allMatching ? { query: matching } : { ids: items.map((item) => item.id) }}
          count={allMatching ? <MatchingCount collectionId={listId} query={matching} bare /> : undefined}
          fields={fieldList}
          onClose={() => setBulkEditing(false)}
          onStarted={(ids) => {
            toast.success('The field update runs in the background. Its progress shows below the list.');
            setOperations(ids);
            setSelected(new Map());
            setAllMatching(false);
          }}
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
  const [previousInitial, setPreviousInitial] = useState(initial);
  // URL navigation updates the value without replacing the focused input.
  if (initial !== previousInitial) {
    setPreviousInitial(initial);
    setValue(initial);
  }
  const input = useRef<HTMLInputElement>(null);
  const latest = useRef(onSearch);
  // "/" jumps to the search box from anywhere on the page, as in many web apps.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (event.key !== '/' || event.ctrlKey || event.metaKey || event.altKey) return;
      if (target && (target.closest('input, textarea, select, [contenteditable="true"], [role="dialog"]') !== null)) return;
      event.preventDefault();
      input.current?.focus();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  useEffect(() => {
    latest.current = onSearch;
  });
  useEffect(() => {
    const trimmed = value.trim();
    if (trimmed === initial.trim()) return;
    const timer = setTimeout(() => latest.current(trimmed || undefined), 350);
    return () => clearTimeout(timer);
  }, [value, initial]);
  const clear = () => {
    setValue('');
    onSearch(undefined);
    input.current?.focus();
  };
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
        ref={input}
        value={value}
        onChange={(event) => setValue(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Escape' && value) {
            event.preventDefault();
            clear();
          }
        }}
        placeholder={`Search this ${noun}…`}
        aria-keyshortcuts="/"
        aria-label={`Search this ${noun}`}
        className="px-8"
      />
      {value && (
        <button
          type="button"
          aria-label="Clear search"
          className="absolute top-1/2 right-2 -translate-y-1/2 rounded p-0.5 text-muted hover:bg-surface-muted hover:text-foreground"
          onClick={clear}
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
  const [tags, setTags] = useState<string[]>([]);
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
            tags,
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
      <Label htmlFor="draft-tags">Tags</Label>
      <TagInput id="draft-tags" value={tags} onChange={setTags} workspaceId={collection.workspace_id} />
    </FormDialog>
  );
}

async function loadListing(request: ApiCall): Promise<Listing> {
  const page = (await api<Page<Resource>>(request.path, { method: request.method, query: request.query, body: request.body }))
    .data;
  return {
    items: page.value ?? [],
    next: continuation(page['@odata.nextLink']),
    body: request.method === 'POST' ? request.body : undefined,
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

/** How many items a query matches, counted by the API apart from the rows; `capped` when counting stopped. */
function useMatchingTotal(collectionId: string, query: QuerySpec, enabled = true) {
  const call = countRequest(collectionId, query);
  return useQuery({
    queryKey: ['resources', collectionId, 'count', call.body],
    enabled,
    queryFn: async () => {
      const page = (await api<Page<Resource>>(call.path, { method: call.method, query: call.query, body: call.body })).data;
      return { total: page.total ?? 0, capped: !!page.total_capped };
    },
  }).data;
}

/** "2,490 items" or "100,000+ items" matching a query. */
function MatchingCount({ collectionId, query, bare }: { collectionId: string; query: QuerySpec; bare?: boolean }) {
  const format = useFormat();
  const counted = useMatchingTotal(collectionId, query);
  if (!counted) return <>{bare ? 'all matching items' : 'All matching items selected'}</>;
  const count = `${format.number(counted.total)}${counted.capped ? '+' : ''} ${counted.total === 1 && !counted.capped ? 'item' : 'items'}`;
  return <>{bare ? count : `All ${count} selected`}</>;
}
