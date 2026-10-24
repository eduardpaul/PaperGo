import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, ChevronLeft, ChevronRight, Download, GitCompare, Info, Link2, MoreHorizontal, PanelRight, Pencil, Upload, X } from 'lucide-react';
import { Suspense, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { authorizationHeaders, fileEditMax } from '@/api/auth';
import { api } from '@/api/http';
import { resourceQuery } from '@/api/queries';
import type { ContentType, FieldDefinition, FileVersion, Page, Resource } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/feedback';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/menu';
import { downloadContent } from '@/features/documents/transfer';
import { can } from '@/features/lists/access';
import { ItemPanel } from '@/features/lists/item-sheet';
import { publishState } from '@/features/lists/schema';
import { ConfirmDialog } from '@/features/settings/section';
import { useFormat } from '@/lib/preferences';
import { cn } from '@/lib/utils';
import { usePeopleNames } from '@/features/fields/people';
import { FileTypeIcon, fileTypeLabel } from './file-types';
import { OpenInOffice } from './office';
import { comparable, TextCompareDialog } from './text-compare';
import { fileRef, fileSource, handlerFor, type EditSession, type FileRef } from './handlers';
import { HandlerBoundary, typing, useViewerKeys, ViewerMessage } from './viewer-kit';

const infoKey = 'papergo.viewer.info';
function rememberedInfo() {
  try {
    return localStorage.getItem(infoKey) === 'open';
  } catch {
    return false;
  }
}

export interface ViewerSiblings {
  previous?: string;
  next?: string;
  /** The item's place in the list, from 1, when the list knows it. */
  position?: number;
  total?: number;
  /** More items follow beyond those loaded. */
  more?: boolean;
}

/**
 * The whole file, full screen above its list. It lives in the URL (open=file,
 * page, blob), so it can be linked, reloaded and closed with Back. Handlers
 * draw the file; the viewer owns everything around it.
 */
export function FileViewer({
  itemId,
  blob,
  page,
  collection,
  fields,
  contentTypes,
  tab,
  comment,
  siblings,
  edit,
  find,
  onEdit,
  onStep,
  onPage,
  onTab,
  onComment,
  onClose,
  onDetails,
  onMoved,
}: {
  itemId: string;
  blob?: string;
  page?: number;
  collection: Resource;
  fields: FieldDefinition[];
  contentTypes: ContentType[];
  tab: string;
  comment?: string;
  siblings: ViewerSiblings;
  /** The file is open in its handler's editor (edit=1). */
  edit?: boolean;
  /** Words to find in the file when it opens. */
  find?: string;
  onEdit: (edit: boolean) => void;
  onStep: (itemId: string) => void;
  onPage: (page: number) => void;
  onTab: (tab: string) => void;
  /** Shows a comment thread in the info panel's discussion. */
  onComment: (threadId: string) => void;
  onClose: () => void;
  onDetails: () => void;
  onMoved: (resource: Resource) => void;
}) {
  const format = useFormat();
  const names = usePeopleNames();
  const itemQuery = useQuery(resourceQuery(itemId));
  const item = itemQuery.data;
  const parent = useQuery({ ...resourceQuery(item?.parent_id ?? ''), enabled: !!item?.parent_id && item.parent_id !== collection.id });
  const earlierVersion = !!blob && !!item && item.file?.blob_id !== blob;
  const versions = useQuery({
    queryKey: ['resources', itemId, 'file-versions', 'viewer'],
    queryFn: async () => (await api<Page<FileVersion>>(`/v1/items/${itemId}/content/versions`, { query: { $top: 100 } })).data.value ?? [],
    enabled: earlierVersion,
  });
  const version = earlierVersion ? versions.data?.find((entry) => entry.id === blob) : undefined;
  const file: FileRef | undefined = earlierVersion
    ? version && { itemId, blobId: version.id, contentType: version.content_type, filename: version.filename, size: version.size, version: version.version, earlier: true }
    : fileRef(itemId, item?.file);
  const handler = handlerFor(file);
  const authorization = authorizationHeaders().Authorization;
  const source = useMemo(() => (file ? fileSource(file) : undefined), [file?.itemId, file?.blobId, file?.earlier, authorization]); // eslint-disable-line react-hooks/exhaustive-deps

  const [info, setInfo] = useState(rememberedInfo);
  const [comparing, setComparing] = useState(false);
  const toggleInfo = (open = !info) => {
    setInfo(open);
    try {
      localStorage.setItem(infoKey, open ? 'open' : 'closed');
    } catch {
      /* The panel still toggles for this visit. */
    }
  };
  // Unsaved details in the info panel, or unsaved text in an editor, are kept until the person decides.
  const dirty = useRef({ details: false, file: false });
  const onDirty = useCallback((value: boolean) => {
    dirty.current.details = value;
  }, []);
  const onFileDirty = useCallback((value: boolean) => {
    dirty.current.file = value;
  }, []);
  const [pending, setPending] = useState<() => void>();
  const guard = (action: () => void) => (dirty.current.details || dirty.current.file ? setPending(() => action) : action());

  // Editable handlers edit the item's file, never an earlier version, for people who may write the item.
  const writable = can(item, 'write');
  const editable = !!handler?.editable && !!file && !file.earlier && writable;
  const tooLarge = !!file && file.size > fileEditMax();
  const canEdit = editable && !tooLarge;
  const editing: EditSession | undefined =
    editable && item
      ? {
          active: !!edit && canEdit,
          allowed: canEdit,
          reason: tooLarge ? `This file is too large to edit in the browser (the limit is ${format.fileSize(fileEditMax())}). Download it, or edit it over WebDAV.` : undefined,
          version: item.version,
          containerId: item.container_id ?? collection.id,
          folderId: item.parent_id !== (item.container_id ?? collection.id) ? item.parent_id : undefined,
          setActive: (active) => onEdit(active),
          onDirty: onFileDirty,
        }
      : undefined;

  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    root.current?.focus();
    const overflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = overflow;
      if (opener?.isConnected) opener.focus();
    };
  }, []);

  const download = () => file && void downloadContent(itemId, file.filename, file.earlier ? file.blobId : undefined);
  const step = (id: string | undefined) => id && guard(() => onStep(id));
  const close = () => guard(onClose);
  const copyLink = () =>
    void navigator.clipboard.writeText(window.location.href).then(
      () => toast.success('Link copied'),
      () => toast.error('The link could not be copied'),
    );

  useViewerKeys((event) => {
    if (event.ctrlKey || event.metaKey || event.altKey) return false;
    // Menus and dialogs above the viewer handle their own keys.
    if (document.querySelector('[role="menu"], [role="alertdialog"]') || [...document.querySelectorAll('[role="dialog"]')].some((dialog) => dialog !== root.current)) return false;
    if (event.key === 'Escape') return close();
    if (typing(event)) return false;
    if (event.key === 'ArrowLeft' || event.key === 'k') return siblings.previous ? void step(siblings.previous) : false;
    if (event.key === 'ArrowRight' || event.key === 'j') return siblings.next ? void step(siblings.next) : false;
    if (event.key === 'i') return toggleInfo();
    if (event.key === 'd' && file) return download();
    if (event.key === 'e' && canEdit && !edit) return onEdit(true);
    return false;
  });

  const draft = item && publishState(item, !!collection.publishing_enabled) === 'Draft';
  const folder = parent.data && parent.data.id !== collection.id ? parent.data : undefined;
  const title = file?.filename ?? item?.name ?? 'File';

  let canvas: ReactNode;
  if (itemQuery.isPending || (earlierVersion && versions.isPending))
    canvas = (
      <ViewerMessage title="Opening…">
        <Spinner />
      </ViewerMessage>
    );
  else if (!item) canvas = <ViewerMessage title="This item is not available.">It was deleted or moved, or you no longer have access.</ViewerMessage>;
  else if (earlierVersion && !version) canvas = <ViewerMessage title="This file version is not available.">It may have been reclaimed, or you can read only the published file.</ViewerMessage>;
  else if (!file)
    canvas = (
      <ViewerMessage title="This item has no file yet.">
        <Button onClick={onDetails}>
          <Upload /> Upload a file from the details
        </Button>
      </ViewerMessage>
    );
  else if (!handler || !source)
    canvas = (
      <ViewerMessage title={file.filename}>
        <FileTypeIcon media={file.contentType} className="size-12" />
        <span>
          {fileTypeLabel(file.contentType)} · {format.fileSize(file.size)}
        </span>
        <span>PaperGo cannot show this kind of file yet.</span>
        <Button variant="primary" onClick={download}>
          <Download /> Download
        </Button>
      </ViewerMessage>
    );
  else
    canvas = (
      // The item's own file keeps its handler across saves; an earlier version is another file.
      <HandlerBoundary key={`${file.itemId}:${file.earlier ? file.blobId : 'current'}`} onDownload={download}>
        <Suspense
          fallback={
            <ViewerMessage title="Opening…">
              <Spinner />
            </ViewerMessage>
          }
        >
          <handler.View
            file={file}
            source={source}
            page={page}
            onPage={onPage}
            find={find}
            comment={comment}
            onComment={(threadId) => {
              toggleInfo(true);
              onComment(threadId);
            }}
            onStep={(delta) => step(delta < 0 ? siblings.previous : siblings.next)}
            editing={editing}
          />
        </Suspense>
      </HandlerBoundary>
    );

  return (
    <div ref={root} role="dialog" aria-modal aria-label={title} tabIndex={-1} className="fixed inset-0 z-40 flex flex-col bg-background outline-none">
      <header className="flex shrink-0 items-center gap-1 border-b bg-surface px-2 py-1.5 sm:gap-2 sm:px-3">
        <Button variant="ghost" size="icon" aria-label={`Back to ${collection.name}`} title="Back (Esc)" onClick={close}>
          <ArrowLeft />
        </Button>
        <div className="min-w-0 flex-1">
          <p className="flex min-w-0 items-center gap-1.5 text-sm font-medium">
            {file && <FileTypeIcon media={file.contentType} />}
            <span className="truncate">{title}</span>
            {draft && !file?.earlier && <Badge>Draft</Badge>}
            {item?.archived && <Badge>Archived</Badge>}
            {file?.earlier && <Badge>Version {file.version}</Badge>}
          </p>
          <p className="hidden truncate text-xs text-muted sm:block">
            {collection.name}
            {folder && ` / ${folder.name}`}
            {item && item.name !== title && ` · ${item.name}`}
          </p>
        </div>
        {(siblings.previous || siblings.next) && (
          <div className="flex items-center">
            <Button variant="ghost" size="icon" aria-label="Previous item (←)" disabled={!siblings.previous} onClick={() => step(siblings.previous)}>
              <ChevronLeft />
            </Button>
            {siblings.position && (
              <span className="hidden text-xs text-muted tabular-nums md:inline">
                {format.number(siblings.position)} of {format.number(siblings.total ?? 0)}
                {siblings.more && '+'}
              </span>
            )}
            <Button variant="ghost" size="icon" aria-label="Next item (→)" disabled={!siblings.next} onClick={() => step(siblings.next)}>
              <ChevronRight />
            </Button>
          </div>
        )}
        {item && !file?.earlier && <OpenInOffice item={item} collection={collection} className="hidden sm:inline-flex" />}
        {canEdit && !edit && (
          <Button variant="secondary" onClick={() => onEdit(true)} title="Edit (E)">
            <Pencil /> <span className="hidden sm:inline">Edit</span>
          </Button>
        )}
        {file && (
          <Button variant="ghost" className="hidden sm:inline-flex" onClick={download} title="Download (D)">
            <Download /> Download
          </Button>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="More file actions">
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={copyLink}>
              <Link2 /> Copy file link
            </DropdownMenuItem>
            {file && (
              <DropdownMenuItem className="sm:hidden" onSelect={download}>
                <Download /> Download
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => guard(onDetails)}>
              <PanelRight /> Open details
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <Button variant={info ? 'secondary' : 'ghost'} size="icon" aria-label="Info (I)" aria-pressed={info} title="Info (I)" onClick={() => toggleInfo()}>
          <Info />
        </Button>
        <Button variant="ghost" size="icon" aria-label="Close" title="Close (Esc)" className="hidden sm:inline-flex" onClick={close}>
          <X />
        </Button>
      </header>
      {file?.earlier && version && (
        <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b bg-warning-soft px-4 py-2 text-[13px]">
          <span>
            You are viewing version {version.version}, uploaded {format.dateTime(version.created_at)}
            {version.created_by && ` by ${names.get(version.created_by) ?? version.created_by}`}.
          </span>
          <Button size="sm" variant="ghost" onClick={() => guard(onDetails)}>
            Restore it from the File tab
          </Button>
          <Button size="sm" variant="ghost" onClick={() => item && onStep(item.id)}>
            View the current file
          </Button>
          {item?.file && comparable(version.content_type) && comparable(item.file.content_type) && (
            <Button size="sm" variant="ghost" onClick={() => setComparing(true)}>
              <GitCompare /> Compare with the current file
            </Button>
          )}
        </div>
      )}
      {comparing && version && item?.file && (
        <TextCompareDialog
          itemId={itemId}
          filename={item.file.filename}
          from={{ version: version.version, blobId: version.id, contentType: version.content_type }}
          to={{ version: item.file.version, contentType: item.file.content_type }}
          onClose={() => setComparing(false)}
        />
      )}
      <div className="relative flex min-h-0 flex-1">
        <div className="min-w-0 flex-1">{canvas}</div>
        {info && item && (
          <div className={cn('absolute inset-y-0 right-0 z-10 w-full border-l shadow-xl sm:w-[400px] lg:static lg:shadow-none')}>
            <ItemPanel
              chrome="panel"
              itemId={item.id}
              collection={collection}
              fields={fields}
              contentTypes={contentTypes}
              tab={tab}
              comment={comment}
              onTab={onTab}
              onClose={() => toggleInfo(false)}
              onMoved={onMoved}
              onDirty={onDirty}
            />
          </div>
        )}
      </div>
      <ConfirmDialog
        open={!!pending}
        onOpenChange={(open) => !open && setPending(undefined)}
        title="Discard unsaved changes?"
        description="Your changes to this item's details have not been saved."
        confirm="Discard changes"
        onConfirm={() => {
          const action = pending;
          setPending(undefined);
          dirty.current = { details: false, file: false };
          action?.();
        }}
      />
    </div>
  );
}

/** A handler that fails to load or draw shows a way out instead of an empty canvas. */
