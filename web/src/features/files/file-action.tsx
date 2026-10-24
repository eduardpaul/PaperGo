import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate, type LinkProps } from '@tanstack/react-router';
import { Download, Expand, Pencil, Upload } from 'lucide-react';
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { authorizationHeaders, fileEditMax } from '@/api/auth';
import { downloadContentPath } from '@/api/requests';
import type { FileSummary, Resource } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { buttonVariants } from '@/components/ui/button';
import { FilePreview } from '@/features/documents/preview';
import { downloadContent } from '@/features/documents/transfer';
import { useFormat } from '@/lib/preferences';
import { cn } from '@/lib/utils';
import { FileTypeIcon, fileFamily, fileTypeLabel } from './file-types';
import { handlerFor } from './handlers';

/** An item with the fields that locate and describe its file. */
export type FileItem = Pick<Resource, 'id' | 'workspace_id' | 'container_id' | 'name'> & { file?: FileSummary | null };

export interface OpenFileOptions {
  /** A PDF page to open at. */
  page?: number;
  /** An earlier file version to open instead of the item's file. */
  blob?: string;
  /** Open the file in its editor. */
  edit?: boolean;
}

/**
 * Where Open goes. A collection page keeps its view, folder, search and sort,
 * so the viewer steps through what the list shows; elsewhere Open goes to the
 * item's collection.
 */
type FileLinkBuilder = (item: FileItem, options?: OpenFileOptions) => LinkProps;

export function openFileLink(item: FileItem, options: OpenFileOptions = {}): LinkProps {
  return {
    to: '/w/$workspaceId/l/$listId',
    params: { workspaceId: item.workspace_id, listId: item.container_id ?? '' },
    search: { item: item.id, open: 'file', page: options.page, blob: options.blob, edit: options.edit || undefined },
  };
}

const FileLinks = createContext<FileLinkBuilder>(openFileLink);

export function FileLinksProvider({ value, children }: { value: FileLinkBuilder; children: ReactNode }) {
  return <FileLinks.Provider value={value}>{children}</FileLinks.Provider>;
}

export function useOpenFileLink() {
  return useContext(FileLinks);
}

/** Opens an item's file the way its Open link does, for keys and double-clicks; files without a viewer download. */
export function useOpenFile() {
  const navigate = useNavigate();
  const link = useOpenFileLink();
  return (item: FileItem, options?: OpenFileOptions) => {
    if (!item.file) return false;
    if (viewable(item)) void navigate(link(item, options) as Parameters<typeof navigate>[0]);
    else void downloadContent(item.id, item.file.filename);
    return true;
  };
}

/**
 * Click and double-click on a row or card with a file. The click opens the
 * details sheet, which covers the list, so it waits a moment for a second
 * click that opens the file instead. Items without a file open at once.
 */
export function useClickOrOpenFile<T extends FileItem>(onClick: (item: T) => void) {
  const openFile = useOpenFile();
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  return {
    click: (item: T, event: React.MouseEvent) => {
      if (!item.file) return onClick(item);
      if (event.detail > 1) return;
      clearTimeout(timer.current);
      timer.current = setTimeout(() => onClick(item), 250);
    },
    doubleClick: (item: T) => {
      clearTimeout(timer.current);
      openFile(item);
    },
  };
}

/** Whether PaperGo can show the item's file in the viewer. */
export function viewable(item: { file?: FileSummary | null }) {
  return !!item.file && !!handlerFor({ contentType: item.file.content_type, filename: item.file.filename });
}

/** Whether PaperGo can edit the item's file in the browser: an editable type, within the edit size limit. */
export function editableFile(item: { file?: FileSummary | null }) {
  return !!item.file && item.file.size <= fileEditMax() && !!handlerFor({ contentType: item.file.content_type, filename: item.file.filename })?.editable;
}

/**
 * The one control for an item's file, the same everywhere: Open (a real link,
 * so it also opens in a new tab) when a handler shows the type, Download
 * otherwise. Clicks do not reach the row or card around it.
 */
export function FileAction({ item, options, variant = 'icon', className }: { item: FileItem; options?: OpenFileOptions; variant?: 'icon' | 'button'; className?: string }) {
  const link = useOpenFileLink();
  if (!item.file) return null;
  const stop = { onClick: (event: React.MouseEvent) => event.stopPropagation(), onDoubleClick: (event: React.MouseEvent) => event.stopPropagation(), onKeyDown: (event: React.KeyboardEvent) => event.stopPropagation() };
  const style = variant === 'icon' ? buttonVariants({ variant: 'ghost', size: 'icon' }) : buttonVariants({ variant: 'primary' });
  if (viewable(item)) {
    return (
      <Link {...link(item, options)} {...stop} aria-label={`Open ${item.file.filename}${options?.page ? ` at page ${options.page}` : ''}`} title="Open (O)" className={cn(style, className)}>
        <Expand />
        {variant === 'button' && 'Open'}
      </Link>
    );
  }
  return (
    <button
      type="button"
      {...stop}
      onClick={(event) => {
        event.stopPropagation();
        void downloadContent(item.id, item.file?.filename ?? item.name);
      }}
      aria-label={`Download ${item.file.filename}`}
      title="Download (D)"
      className={cn(style, className)}
    >
      <Download />
      {variant === 'button' && 'Download'}
    </button>
  );
}

/** A small picture of the file: its preview for types that have one, the opening lines of text files, else its type icon. */
export function FileThumb({ item, className, iconClassName }: { item: FileItem; className?: string; iconClassName?: string }) {
  const family = fileFamily(item.file?.content_type);
  if (item.file && (family === 'pdf' || family === 'image'))
    return <FilePreview itemId={item.id} version={item.file.version} alt="" className={className} />;
  if (item.file && family === 'text' && viewable(item)) return <TextExcerpt item={item} className={className} iconClassName={iconClassName} />;
  return (
    <span className={cn('grid place-items-center rounded-md bg-surface-muted', className)}>
      <FileTypeIcon media={item.file?.content_type} className={cn('size-8', iconClassName)} />
    </span>
  );
}

/** The first kilobyte of a text file, decoded leniently; the file's icon until it arrives. */
function TextExcerpt({ item, className, iconClassName }: { item: FileItem; className?: string; iconClassName?: string }) {
  const excerpt = useQuery({
    queryKey: ['resources', item.id, 'excerpt', item.file?.version],
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const response = await fetch(downloadContentPath(item.id), { headers: { ...authorizationHeaders(), Range: 'bytes=0-1023' }, credentials: 'same-origin', signal });
      if (!response.ok) throw new Error(`excerpt ${response.status}`);
      return new TextDecoder('utf-8').decode(await response.arrayBuffer()).replace(/\uFFFD+$/, '');
    },
  });
  if (!excerpt.data?.trim()) {
    return (
      <span className={cn('grid place-items-center rounded-md bg-surface-muted', className)}>
        <FileTypeIcon media={item.file?.content_type} className={cn('size-8', iconClassName)} />
      </span>
    );
  }
  return (
    <span aria-hidden className={cn('relative block overflow-hidden rounded-md border bg-surface p-2 text-left font-mono text-[9px] leading-tight whitespace-pre-wrap break-words text-muted', className)}>
      {excerpt.data.slice(0, 600)}
      <span className="absolute inset-x-0 bottom-0 h-1/3 bg-linear-to-t from-surface" />
    </span>
  );
}

/**
 * The file of an item, shown above the item sheet's tabs on every tab, so the
 * file is one click from the details: a thumbnail that opens it, what it is,
 * and Open or Download.
 */
export function FileBar({ item, draft, canWrite, onUpload }: { item: FileItem; draft?: boolean; canWrite?: boolean; onUpload?: () => void }) {
  const link = useOpenFileLink();
  const format = useFormat();
  const file = item.file;
  const open = viewable(item);
  const [hint, setHint] = useState(() => open && !seen(hintKey));
  const dismiss = () => {
    setHint(false);
    remember(hintKey);
  };
  if (!file) {
    return (
      <div className="flex items-center gap-3 border-b px-4 py-2.5 text-[13px] text-muted">
        <FileTypeIcon media={undefined} className="size-5" />
        <span className="flex-1">No file yet</span>
        {canWrite && onUpload && (
          <button type="button" className={buttonVariants({ size: 'sm' })} onClick={onUpload}>
            <Upload /> Upload file
          </button>
        )}
      </div>
    );
  }
  const thumb = <FileThumb item={item} className="size-16 min-h-0 shrink-0 [&_img]:max-h-16" iconClassName="size-7" />;
  return (
    <div className="border-b">
    <div className="flex items-center gap-3 px-4 py-2.5">
      {open ? (
        <Link {...link(item)} aria-label={`Open ${file.filename}`} className="hidden shrink-0 cursor-zoom-in rounded-md focus-visible:ring-2 focus-visible:ring-ring sm:block">
          {thumb}
        </Link>
      ) : (
        <span className="hidden sm:block">{thumb}</span>
      )}
      <div className="min-w-0 flex-1">
        <p className="flex items-center gap-1.5 truncate text-[13px] font-medium">
          <FileTypeIcon media={file.content_type} className="sm:hidden" />
          <span className="truncate">{file.filename}</span>
        </p>
        <p className="truncate text-xs text-muted">
          {fileTypeLabel(file.content_type)} · {format.fileSize(file.size)} · version {file.version}
          {draft && <Badge className="ml-1.5">Draft</Badge>}
        </p>
      </div>
      {canWrite && editableFile(item) && (
        <Link {...link(item, { edit: true })} aria-label={`Edit ${file.filename}`} title="Edit (E in the viewer)" className={buttonVariants()}>
          <Pencil /> Edit
        </Link>
      )}
      <FileAction item={item} variant="button" />
      {open && (
        <button type="button" aria-label={`Download ${file.filename}`} title="Download (D)" className={buttonVariants({ variant: 'ghost', size: 'icon' })} onClick={() => void downloadContent(item.id, file.filename)}>
          <Download />
        </button>
      )}
    </div>
    {hint && (
      <p role="note" className="mx-4 mb-2.5 flex items-center gap-2 rounded-md bg-accent-soft px-3 py-1.5 text-xs">
        <span className="flex-1">
          Open the whole file here, full screen. From the list, double-click a row or press <kbd className="font-sans font-semibold">O</kbd>.
        </span>
        <button type="button" className="font-medium text-accent hover:underline" onClick={dismiss}>
          Got it
        </button>
      </p>
    )}
    </div>
  );
}

const hintKey = 'papergo.hint.open-file';
function seen(key: string) {
  try {
    return localStorage.getItem(key) === 'seen';
  } catch {
    return true;
  }
}
function remember(key: string) {
  try {
    localStorage.setItem(key, 'seen');
  } catch {
    /* The hint returns next time; nothing else depends on it. */
  }
}
