import { useArchiveActions } from './archive-actions';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Download, Expand, FileCode2, FolderInput, FolderSearch, GitCompare, MoreHorizontal, Trash2, Upload, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { keys, resourceQuery, revisions } from '@/api/queries';
import { useApiPages } from '@/api/pages';
import type { ContentType, FieldDefinition, FileVersion, Resource, Revision } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { Button, buttonVariants } from '@/components/ui/button';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Skeleton } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/menu';
import { Select } from '@/components/ui/select';
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet';
import { KeywordInput } from '@/components/keyword-input';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { FilePreview } from '@/features/documents/preview';
import { FileBar, FileThumb, useOpenFileLink, viewable } from '@/features/files/file-action';
import { comparable, TextCompareDialog } from '@/features/files/text-compare';
import { downloadContent, replaceContent } from '@/features/documents/transfer';
import { FieldEditor } from '@/features/fields/editor';
import { usePeopleNames } from '@/features/fields/people';
import { valuesPatch } from '@/features/fields/values';
import { QueryFeedback } from '@/features/admin/common';
import { ConfirmDialog } from '@/features/settings/section';
import { AddToSmartFolder } from '@/features/smart-folders/classification';
import { Discussion } from '@/features/comments/discussion';
import { commentsAvailable } from '@/api/auth';
import { ItemWorkflows } from '@/features/workflows/item-workflows';
import { useFormat } from '@/lib/preferences';
import { can } from './access';
import { AccessEditor } from './access-editor';
import { ItemSchemaView, Publications } from './item-history';
import { MoveDialog } from './move-dialog';
import { RenameDialog } from './rename-dialog';
import { RelatedItems } from './related-items';
import { fieldsForType, publishState } from './schema';

type ItemPanelProps = {
  itemId: string;
  collection: Resource;
  fields: FieldDefinition[];
  contentTypes: ContentType[];
  tab: string;
  /** The comment to bring into view on the Discussion tab. */
  comment?: string;
  onTab: (tab: string) => void;
  onClose: () => void;
  onMoved: (resource: Resource) => void;
};

/** The item panel next to its list: the file, details and the other tabs, with item actions in the header menu. */
export function ItemSheet(props: ItemPanelProps) {
  return <ItemPanel {...props} chrome="sheet" />;
}

/**
 * The item's details and tabs, as a sheet beside its list or as the viewer's
 * info panel. The panel reports unsaved details, so whatever hosts it can ask
 * before it goes.
 */
export function ItemPanel({
  itemId,
  collection,
  fields,
  contentTypes,
  tab,
  comment,
  onTab,
  onClose,
  onMoved,
  chrome,
  onDirty,
}: ItemPanelProps & { chrome: 'sheet' | 'panel'; onDirty?: (dirty: boolean) => void }) {
  const queryClient = useQueryClient();
  const format = useFormat();
  const names = usePeopleNames();
  const itemQuery = useQuery(resourceQuery(itemId));
  const item = itemQuery.data;
  const library = collection.kind === 'library';
  const [dirty, setDirty] = useState(false);
  useEffect(() => onDirty?.(dirty), [dirty, onDirty]);
  const replaceInput = useRef<HTMLInputElement>(null);
  const replaceFile = useReplaceFile(item);
  const [confirmingClose, setConfirmingClose] = useState(false);
  const [dialog, setDialog] = useState<'move' | 'classify' | 'delete' | 'schema' | 'rename'>();
  const remove = useMutation({
    meta: { silent: true },
    mutationFn: () =>
      api(`/v1/resources/${item!.id}`, {
        method: 'DELETE',
        ifMatch: item!.version,
        query: item!.kind === 'folder' ? { recursive: 'true' } : undefined,
      }),
    onSuccess: async () => {
      setDialog(undefined);
      toast.success('Moved to the recycle bin');
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      onClose();
    },
  });
  const close = () => (dirty ? setConfirmingClose(true) : onClose());
  const archive = useArchiveActions();
  const isFile = library && item?.kind === 'item';
  const canWrite = can(item, 'write');
  const canManage = can(item, 'manage');
  const hasActions = isFile || item?.kind === 'item' || canWrite;
  const discussed = item?.kind === 'item' && commentsAvailable();
  const tabs = [
    'details',
    ...(isFile ? ['content'] : []),
    'versions',
    'links',
    ...(item?.kind === 'item' ? ['workflows'] : []),
    ...(discussed ? ['discussion'] : []),
    ...(canManage ? ['access'] : []),
    ...(collection.publishing_enabled ? ['publish'] : []),
  ];
  const current = tabs.includes(tab) ? tab : 'details';
  const editor = item?.updated_by ? (names.get(item.updated_by) ?? item.updated_by) : undefined;
  const sheet = chrome === 'sheet';
  const Title = sheet ? SheetTitle : 'h2';
  const Description = sheet ? SheetDescription : 'p';

  const body = (
    <>
        <header className="flex items-start gap-2 border-b px-4 py-3">
          <div className="min-w-0 flex-1">
            <Title className="truncate text-base font-semibold">{item?.name ?? (itemQuery.isPending ? '…' : 'Item')}{item?.archived && <Badge className="ml-2">Archived</Badge>}</Title>
            <Description className="truncate text-xs text-muted">
              {collection.name}
              {item?.updated_at && ` · Updated ${format.relative(item.updated_at)}${editor ? ` by ${editor}` : ''}`}
            </Description>
          </div>
          {item && hasActions && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" aria-label="Item actions">
                  <MoreHorizontal />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                {isFile && (
                  <DropdownMenuItem onSelect={() => void downloadContent(item.id, item.name)}>
                    <Download /> Download original
                  </DropdownMenuItem>
                )}
                {item.kind === 'item' && canWrite && (
                  <>
                    <DropdownMenuItem disabled={archive.isPending} onSelect={() => archive.mutate({ items: [item], archived: !item.archived })}>{item.archived ? 'Unarchive' : 'Archive'}</DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => setDialog('rename')}>Rename…</DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => setDialog('move')}>
                      <FolderInput /> Move…
                    </DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => setDialog('classify')}>
                      <FolderSearch /> Add to smart folder…
                    </DropdownMenuItem>
                  </>
                )}
                {item.kind === 'item' && (
                  <DropdownMenuItem onSelect={() => setDialog('schema')}>
                    <FileCode2 /> Field definitions
                  </DropdownMenuItem>
                )}
                {canWrite && (
                  <>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem tone="danger" onSelect={() => setDialog('delete')}>
                      <Trash2 /> Delete
                    </DropdownMenuItem>
                  </>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          {sheet ? (
            <SheetClose />
          ) : (
            <Button variant="ghost" size="icon" aria-label="Close the info panel" onClick={close}>
              <X />
            </Button>
          )}
        </header>
        {item && isFile && sheet && (
          <>
            <FileBar item={item} collection={collection} draft={publishState(item, !!collection.publishing_enabled) === 'Draft'} canWrite={canWrite} onUpload={() => replaceInput.current?.click()} />
            <ReplaceInput input={replaceInput} onFile={(file) => replaceFile.mutate(file)} />
          </>
        )}
        {item ? (
          <Tabs value={current} onValueChange={onTab} className="flex min-h-0 flex-1 flex-col">
            <TabsList className="shrink-0 overflow-x-auto">
              <TabsTrigger value="details">Details</TabsTrigger>
              {isFile && <TabsTrigger value="content">File</TabsTrigger>}
              <TabsTrigger value="versions">Versions</TabsTrigger>
              <TabsTrigger value="links">Links</TabsTrigger>
              {item.kind === 'item' && <TabsTrigger value="workflows">Workflows</TabsTrigger>}
              {discussed && <TabsTrigger value="discussion">Discussion</TabsTrigger>}
              {canManage && <TabsTrigger value="access">Permissions</TabsTrigger>}
              {collection.publishing_enabled && <TabsTrigger value="publish">Publishing</TabsTrigger>}
            </TabsList>
            {/* Kept mounted so switching tabs does not lose unsaved edits. */}
            <TabsContent value="details" forceMount className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
              <Details
                key={`${item.id}:${item.version}`}
                item={item}
                fields={fields}
                contentTypes={contentTypes}
                readOnly={!canWrite}
                onDirty={setDirty}
              />
            </TabsContent>
            {isFile && (
              <TabsContent value="content" className="min-h-0 flex-1 overflow-auto p-4">
                <Content item={item} canWrite={canWrite} />
              </TabsContent>
            )}
            {item.kind === 'item' && <TabsContent value="workflows" className="min-h-0 flex-1 overflow-auto p-4">
              <ItemWorkflows item={item} collectionId={collection.id} canWrite={canWrite} />
            </TabsContent>}
            {discussed && (
              <TabsContent value="discussion" className="min-h-0 flex-1 overflow-auto p-4">
                <Discussion key={comment} itemId={item.id} focus={comment} />
              </TabsContent>
            )}
            <TabsContent value="versions" className="min-h-0 flex-1 overflow-auto p-4">
              <Versions item={item} collection={collection} canWrite={canWrite} />
            </TabsContent>
            <TabsContent value="links" className="min-h-0 flex-1 overflow-auto p-4">
              <RelatedItems item={item} canWrite={canWrite} />
            </TabsContent>
            {canManage && (
              <TabsContent value="access" className="min-h-0 flex-1 overflow-auto p-4">
                <AccessEditor resource={item} />
              </TabsContent>
            )}
            {collection.publishing_enabled && (
              <TabsContent value="publish" className="min-h-0 flex-1 overflow-auto p-4">
                <Publish item={item} collection={collection} canPublish={can(item, 'publish')} />
              </TabsContent>
            )}
          </Tabs>
        ) : itemQuery.isPending ? (
          <div className="space-y-3 p-4">
            <Skeleton className="h-9" />
            <Skeleton className="h-9" />
            <Skeleton className="h-9" />
          </div>
        ) : (
          <p className="p-4 text-sm text-muted">This item is not available. It was deleted or moved, or you no longer have access.</p>
        )}
        {item && dialog === 'rename' && <RenameDialog item={item} onClose={() => setDialog(undefined)} />}
        {item && dialog === 'move' && (
          <MoveDialog item={item} collection={collection} onClose={() => setDialog(undefined)} onMoved={onMoved} />
        )}
        {item && dialog === 'classify' && <AddToSmartFolder item={item} onClose={() => setDialog(undefined)} />}
        {item && dialog === 'schema' && (
          <Dialog open onOpenChange={(open) => !open && setDialog(undefined)}>
            <DialogContent className="max-h-[85vh] overflow-auto">
              <DialogHeader>
                <DialogTitle>Field definitions</DialogTitle>
              </DialogHeader>
              <div className="px-5 pb-5">
                <ItemSchemaView itemId={item.id} />
              </div>
            </DialogContent>
          </Dialog>
        )}
        <ConfirmDialog
          open={dialog === 'delete'}
          onOpenChange={(open) => !open && setDialog(undefined)}
          title={`Delete “${item?.name ?? ''}”?`}
          description={
            item?.kind === 'folder'
              ? 'The folder is deleted with everything in it. You can restore it from the recycle bin.'
              : 'You can restore it from the recycle bin.'
          }
          confirm="Delete"
          busy={remove.isPending}
          error={remove.error}
          onConfirm={() => remove.mutate()}
        />
        <ConfirmDialog
          open={confirmingClose}
          onOpenChange={setConfirmingClose}
          title="Discard unsaved changes?"
          description="Your changes to this item have not been saved."
          confirm="Discard changes"
          onConfirm={() => {
            setConfirmingClose(false);
            onClose();
          }}
        />
    </>
  );
  return sheet ? (
    <Sheet open onOpenChange={(open) => !open && close()}>
      <SheetContent aria-describedby={undefined}>{body}</SheetContent>
    </Sheet>
  ) : (
    <aside aria-label="Item info" className="flex h-full min-h-0 flex-col bg-surface">
      {body}
    </aside>
  );
}

/** Replaces an item's file with a new version. */
function useReplaceFile(item: Resource | undefined) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (file: File) => replaceContent(item!.id, item!.version, file),
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      const duplicates = result.data.duplicates ?? [];
      if (duplicates.length)
        toast.warning(`Already stored elsewhere: ${duplicates.map((duplicate) => duplicate.name).join(', ')}`);
      else toast.success(item?.file ? 'File replaced' : 'File uploaded');
    },
  });
}

function ReplaceInput({ input, onFile }: { input: React.RefObject<HTMLInputElement | null>; onFile: (file: File) => void }) {
  return (
    <input
      ref={input}
      type="file"
      className="sr-only"
      tabIndex={-1}
      aria-label="Replacement file"
      onChange={(event) => {
        const file = event.target.files?.[0];
        if (file) onFile(file);
        event.target.value = '';
      }}
    />
  );
}

function Details({
  item,
  fields,
  contentTypes,
  readOnly,
  onDirty,
}: {
  item: Resource;
  fields: FieldDefinition[];
  contentTypes: ContentType[];
  readOnly: boolean;
  onDirty: (dirty: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const ownership = useQuery({ queryKey: ['resources', item.id, 'integration'], queryFn: async () => (await api<{ integration_id: string | null; name?: string; managed_fields: string[] }>(`/v1/items/${item.id}/integration`)).data, enabled: item.kind === 'item' });
  const managed = new Set(ownership.data?.managed_fields ?? []);
  const ownershipPending = item.kind === 'item' && !ownership.data;
  const [name, setName] = useState(item.name);
  const [keywords, setKeywords] = useState<string[]>(item.keywords ?? []);
  const [contentTypeId, setContentTypeId] = useState(item.content_type_id ?? '');
  const visible = fieldsForType(fields, contentTypes, contentTypeId || item.content_type_id);
  const [values, setValues] = useState<Record<string, unknown>>({ ...(item.values ?? {}) });

  const body = () => {
    const changes: Record<string, unknown> = {};
    if (name.trim() !== item.name) changes.name = name.trim();
    if (keywords.join('\n') !== (item.keywords ?? []).join('\n')) changes.keywords = keywords;
    if (contentTypeId && contentTypeId !== (item.content_type_id ?? '')) changes.content_type_id = contentTypeId;
    const patch = valuesPatch(visible, item.values, values);
    if (patch) changes.values = patch;
    return changes;
  };
  const dirty = Object.keys(body()).length > 0;
  useEffect(() => onDirty(dirty), [dirty, onDirty]);

  const save = useMutation({
    mutationFn: async () =>
      (await api<Resource>(`/v1/resources/${item.id}`, { method: 'PATCH', ifMatch: item.version, body: body() })).data,
    onSuccess: async (updated) => {
      setName(updated.name);
      setKeywords(updated.keywords ?? []);
      setValues({ ...(updated.values ?? {}) });
      setContentTypeId(updated.content_type_id ?? '');
      queryClient.setQueryData(keys.resource(item.id), updated);
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      toast.success('Saved');
    },
  });

  return (
    <form
      className="flex min-h-0 flex-1 flex-col"
      onSubmit={(event) => {
        event.preventDefault();
        if (dirty) save.mutate();
      }}
    >
      <fieldset disabled={readOnly || save.isPending} className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto p-4">
        {readOnly && <p className="text-xs text-muted">You can read this item but not change it.</p>}
        <Label htmlFor="item-name">Name</Label>
        <Input id="item-name" disabled={ownershipPending || managed.has('$name')} value={name} onChange={(event) => setName(event.target.value)} required />
        {contentTypes.length > 1 && item.kind === 'item' && (
          <>
            <Label htmlFor="item-type">Content type</Label>
            <Select id="item-type" disabled={ownershipPending || managed.has('$content_type')} value={contentTypeId} onChange={(event) => setContentTypeId(event.target.value)}>
              {!item.content_type_id && <option value="">Default</option>}
              {contentTypes.map((type) => (
                <option key={type.id} value={type.id}>
                  {type.name}
                </option>
              ))}
            </Select>
          </>
        )}
        {ownership.data?.integration_id && <p className="text-xs text-muted">{ownership.data.name} manages the event details. Local metadata stays editable.</p>}
        <QueryFeedback query={ownership} />
        {visible.map((field) => (
          <fieldset key={field.id} disabled={ownershipPending || managed.has(field.key)}><FieldEditor
            key={field.id}
            field={field}
            value={values[field.key]}
            onChange={(value) => setValues((current) => ({ ...current, [field.key]: value }))}
          />{managed.has(field.key) && <p className="text-xs text-muted">Managed by integration</p>}</fieldset>
        ))}
        <Label htmlFor="item-keywords">Keywords</Label>
        <KeywordInput id="item-keywords" value={keywords} onChange={setKeywords} disabled={readOnly} />
      </fieldset>
      {!readOnly && (
      <footer className="flex shrink-0 items-center justify-end gap-2 border-t bg-surface px-4 py-3">
        {dirty && <span className="mr-auto text-xs text-muted">Unsaved changes</span>}
        <Button
          type="button"
          disabled={!dirty || save.isPending}
          onClick={() => {
            setName(item.name);
            setKeywords(item.keywords ?? []);
            setContentTypeId(item.content_type_id ?? '');
            setValues({ ...(item.values ?? {}) });
          }}
        >
          Discard
        </Button>
        <Button type="submit" variant="primary" disabled={!dirty || save.isPending || !name.trim()}>
          {save.isPending ? 'Saving…' : 'Save'}
        </Button>
      </footer>
      )}
    </form>
  );
}

function Content({ item, canWrite }: { item: Resource; canWrite: boolean }) {
  const queryClient = useQueryClient();
  const format = useFormat();
  const names = usePeopleNames();
  const input = useRef<HTMLInputElement>(null);
  const [restoring, setRestoring] = useState<FileVersion>();
  const [comparing, setComparing] = useState<FileVersion>();
  const versions = useApiPages<FileVersion>(['resources', item.id, 'file-versions'], `/v1/items/${item.id}/content/versions`);
  const current = versions.items.find((version) => version.is_current);
  const restore = useMutation({
    meta: { silent: true },
    mutationFn: (version: FileVersion) =>
      api(`/v1/items/${item.id}/content/versions/${version.version}/restore`, { method: 'POST', ifMatch: item.version }),
    onSuccess: async () => {
      setRestoring(undefined);
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      await queryClient.invalidateQueries({ queryKey: ['revisions', item.id] });
      toast.success('File restored as a new version');
    },
  });
  const replace = useReplaceFile(item);
  const link = useOpenFileLink();
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap gap-2">
        <Button variant="primary" onClick={() => void downloadContent(item.id, item.name)}>
          <Download /> Download original
        </Button>
        {canWrite && (
          <Button disabled={replace.isPending} onClick={() => input.current?.click()}>
            <Upload /> {replace.isPending ? 'Uploading…' : 'Replace file'}
          </Button>
        )}
        <ReplaceInput input={input} onFile={(file) => replace.mutate(file)} />
      </div>
      {viewable(item) ? (
        <Link {...link(item)} aria-label={`Open ${item.file?.filename ?? item.name}`} className="cursor-zoom-in rounded-md focus-visible:ring-2 focus-visible:ring-ring">
          <FilePreview itemId={item.id} version={item.version} alt={`Preview of ${item.name}`} />
        </Link>
      ) : (
        <FileThumb item={item} className="h-40" iconClassName="size-12" />
      )}
      {canWrite && (
        <p className="text-xs text-muted">Replacing the file keeps the name, keywords and columns, and adds a new file version.</p>
      )}
      <section aria-labelledby="file-versions" className="flex flex-col gap-2">
        <h3 id="file-versions" className="text-[13px] font-medium">
          File versions
        </h3>
        <QueryFeedback query={versions} />
        <ul className="flex flex-col divide-y rounded-md border">
          {versions.items.map((version) => {
            const unavailable = version.availability === 'reclaimed';
            return (
              <li key={version.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[13px] font-medium">
                    Version {version.version}
                    {version.is_current && <Badge className="ml-2">Current</Badge>}
                    {unavailable && <Badge className="ml-2">Unavailable</Badge>}
                  </p>
                  <p className="truncate text-xs text-muted">
                    {version.filename} · {format.fileSize(version.size)} · {format.dateTime(version.created_at)}
                    {version.created_by && ` · ${names.get(version.created_by) ?? version.created_by}`}
                    {unavailable && ' · content unavailable'}
                  </p>
                </div>
                {!unavailable && viewable({ file: { blob_id: version.id, content_type: version.content_type, filename: version.filename, size: version.size, version: version.version } }) && (
                  <Link {...link(item, version.is_current ? {} : { blob: version.id })} aria-label={`Open version ${version.version}`} className={buttonVariants({ size: 'sm', variant: 'ghost' })}>
                    <Expand />
                  </Link>
                )}
                {!unavailable && (
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`Download version ${version.version}`}
                    onClick={() => void downloadContent(item.id, version.filename, version.is_current ? undefined : version.id)}
                  >
                    <Download />
                  </Button>
                )}
                {!version.is_current && !unavailable && current && comparable(version.content_type) && comparable(current.content_type) && (
                  <Button size="sm" variant="ghost" aria-label={`Compare version ${version.version} with the current file`} title="Compare with the current file" onClick={() => setComparing(version)}>
                    <GitCompare />
                  </Button>
                )}
                {canWrite && !version.is_current && !unavailable && (
                  <Button size="sm" onClick={() => setRestoring(version)}>
                    Restore
                  </Button>
                )}
              </li>
            );
          })}
          {!versions.isPending && versions.items.length === 0 && (
            <li className="px-3 py-2 text-[13px] text-muted">No file yet.</li>
          )}
        </ul>
        {versions.hasNextPage && (
          <Button size="sm" disabled={versions.isFetchingNextPage} onClick={() => void versions.fetchNextPage()}>
            Older versions
          </Button>
        )}
      </section>
      {comparing && current && (
        <TextCompareDialog
          itemId={item.id}
          filename={current.filename}
          from={{ version: comparing.version, blobId: comparing.id, contentType: comparing.content_type }}
          to={{ version: current.version, contentType: current.content_type }}
          onClose={() => setComparing(undefined)}
        />
      )}
      <ConfirmDialog
        open={restoring !== undefined}
        onOpenChange={(open) => !open && setRestoring(undefined)}
        title={`Restore file version ${restoring?.version ?? ''}?`}
        description="The earlier file becomes the current file as a new version. The name, keywords and columns stay as they are, and the current file stays in the version list."
        confirm="Restore file"
        busy={restore.isPending}
        error={restore.error}
        onConfirm={() => restoring && restore.mutate(restoring)}
      />
    </div>
  );
}

function Versions({ item, collection, canWrite }: { item: Resource; collection: Resource; canWrite: boolean }) {
  const format = useFormat();
  const names = usePeopleNames();
  const queryClient = useQueryClient();
  const history = useQuery({ queryKey: ['revisions', item.id], queryFn: () => revisions(item.id) });
  const restore = useMutation({
    mutationFn: (revision: Revision) =>
      api(`/v1/items/${item.id}/revisions/${revision.revision_number}/restore`, {
        method: 'POST',
        ifMatch: item.version,
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.resource(item.id) });
      await queryClient.invalidateQueries({ queryKey: ['revisions', item.id] });
      toast.success('Earlier version restored');
    },
  });
  const rows = history.data?.value ?? [];
  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-muted">
        {collection.versioning === 'off'
          ? `Version history is off for this ${collection.kind === 'library' ? 'library' : 'list'}. Retained earlier versions are shown.`
          : 'Restoring brings back the name, keywords and columns of that version as a new version.'}
        {collection.kind === 'library' && ' The file stays as it is; restore files on the File tab.'}
      </p>
      {rows.map((revision) => (
        <div key={revision.id} className="flex items-center gap-3 rounded-md border px-3 py-2">
          <div className="min-w-0 flex-1">
            <p className="truncate text-[13px] font-medium">
              Version {revision.revision_number}
              {revision.is_current && <span className="font-normal text-muted"> · current</span>}
            </p>
            <p className="text-xs text-muted">
              {format.dateTime(revision.created_at)} · {names.get(revision.created_by) ?? revision.created_by}
              {!!revision.changed_fields?.length && ` · changed ${revision.changed_fields.map((key) => key.replace(/^\$/, '')).join(', ')}`}
            </p>
          </div>
          {canWrite && !revision.is_current && (
            <Button size="sm" disabled={restore.isPending} onClick={() => restore.mutate(revision)}>
              Restore
            </Button>
          )}
        </div>
      ))}
      {!history.isPending && rows.length === 0 && <p className="text-sm text-muted">No earlier versions.</p>}
    </div>
  );
}

function Publish({ item, collection, canPublish }: { item: Resource; collection: Resource; canPublish: boolean }) {
  const queryClient = useQueryClient();
  const state = publishState(item, !!collection.publishing_enabled);
  const act = useMutation({
    mutationFn: (action: 'publish' | 'unpublish') =>
      api(action === 'publish' ? `/v1/items/${item.id}/publications` : `/v1/items/${item.id}/unpublish`, {
        method: 'POST',
        ifMatch: item.version,
      }),
    onSuccess: async (_result, action) => {
      toast.success(action === 'publish' ? 'Published' : 'Unpublished');
      await queryClient.invalidateQueries({ queryKey: keys.resource(item.id) });
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      await queryClient.invalidateQueries({ queryKey: ['publications', item.id] });
    },
  });
  const explanation =
    state === 'Published'
      ? 'Readers see the current version.'
      : state === 'Draft'
        ? 'Readers see the last published version; publish to show them your changes.'
        : 'Readers do not see this item yet.';
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm">
        <span className="font-medium">{state}.</span> {explanation}
      </p>
      {canPublish && (
      <div className="flex gap-2">
        <Button variant="primary" disabled={act.isPending || state === 'Published'} onClick={() => act.mutate('publish')}>
          Publish
        </Button>
        <Button disabled={act.isPending || state === 'Unpublished'} onClick={() => act.mutate('unpublish')}>
          Unpublish
        </Button>
      </div>
      )}
      <Publications itemId={item.id} />
    </div>
  );
}
