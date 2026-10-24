import { lazy, type ComponentType, type LazyExoticComponent } from 'react';
import { authorizationHeaders } from '@/api/auth';
import { downloadContentPath } from '@/api/requests';
import type { FileSummary } from '@/api/types';

/** A file a handler shows: the file an item shows, or one of its earlier versions. */
export interface FileRef {
  itemId: string;
  blobId: string;
  contentType: string;
  filename: string;
  size: number;
  version: number;
  /** An earlier version than the one the item shows, read with its blob_id. */
  earlier?: boolean;
  /**
   * Bytes that are not one of the item's file versions, such as a file a
   * workflow staged for review: handlers read only their source, with no
   * page renditions or read text from the item.
   */
  detached?: boolean;
}

export function fileRef(itemId: string, file: FileSummary | null | undefined, earlier = false): FileRef | undefined {
  if (!file) return undefined;
  return { itemId, blobId: file.blob_id, contentType: file.content_type, filename: file.filename, size: file.size, version: file.version, earlier };
}

/** Where a handler reads a file's bytes: a URL that serves byte ranges, and the caller's credentials. */
export interface FileSource {
  url: string;
  headers: Record<string, string>;
}

export function fileSource(file: FileRef): FileSource {
  return { url: downloadContentPath(file.itemId, file.earlier ? file.blobId : undefined), headers: authorizationHeaders() };
}

export interface ViewerProps {
  file: FileRef;
  source: FileSource;
  /** The page to show first, for paged files. */
  page?: number;
  /** Reports the page in view, so links and reloads keep it. */
  onPage?: (page: number) => void;
  /** Words to find when the file opens, such as a search's query. */
  find?: string;
  /** Steps to the previous (-1) or next (1) item, for a swipe; absent when there is none. */
  onStep?: (delta: -1 | 1) => void;
  /** Editing, for editable handlers; absent for earlier file versions. */
  editing?: EditSession;
}

/**
 * The viewer's side of editing a file: whether the person may edit it here,
 * whether they are (edit=1 in the URL), and what a save needs. Each save is a
 * new file version, conditional on the item version it started from.
 */
export interface EditSession {
  active: boolean;
  allowed: boolean;
  /** Why editing is not allowed, for people who may write the item. */
  reason?: string;
  /** The item version the file was read at, for If-Match. */
  version: number;
  /** Where Save as a new file puts the copy. */
  containerId: string;
  folderId?: string;
  setActive: (active: boolean) => void;
  /** Reports unsaved changes, so the viewer asks before leaving them. */
  onDirty: (dirty: boolean) => void;
}

/**
 * A file handler shows one kind of file in the viewer. Handlers use only the
 * public API, through their source, and load lazily: a handler's libraries
 * are fetched the first time a file of its kind opens.
 */
export interface FileHandler {
  id: string;
  accepts(file: Pick<FileRef, 'contentType' | 'filename'>): boolean;
  View: LazyExoticComponent<ComponentType<ViewerProps>>;
  /** The View edits the file too, when the viewer's edit session allows. */
  editable?: boolean;
}

const handlers: FileHandler[] = [];

/** Adds a handler, replacing one with the same id. Later handlers do not override earlier matches. */
export function registerFileHandler(handler: FileHandler) {
  const index = handlers.findIndex((entry) => entry.id === handler.id);
  if (index >= 0) handlers[index] = handler;
  else handlers.push(handler);
}

export function handlerFor(file: Pick<FileRef, 'contentType' | 'filename'> | undefined): FileHandler | undefined {
  return file ? handlers.find((handler) => handler.accepts(file)) : undefined;
}

const images = new Set(['image/jpeg', 'image/png', 'image/webp', 'image/gif']);

registerFileHandler({ id: 'pdf', accepts: (file) => file.contentType === 'application/pdf', View: lazy(() => import('./handlers/pdf')) });
registerFileHandler({ id: 'image', accepts: (file) => images.has(file.contentType), View: lazy(() => import('./handlers/image')) });
// Browsers cannot decode TIFF; the server renders each page of a scan.
registerFileHandler({ id: 'pages', accepts: (file) => file.contentType === 'image/tiff', View: lazy(() => import('./handlers/pages')) });
registerFileHandler({ id: 'markdown', accepts: (file) => file.contentType === 'text/markdown', View: lazy(() => import('./handlers/markdown')), editable: true });
registerFileHandler({ id: 'text', accepts: (file) => file.contentType === 'text/plain', View: lazy(() => import('./handlers/text')), editable: true });
