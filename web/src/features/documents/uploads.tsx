import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { AlertTriangle, Check, ChevronDown, ChevronUp, FileUp, RotateCw, X } from 'lucide-react';
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { keys } from '@/api/queries';
import type { DocumentUpload } from '@/api/types';
import { Button } from '@/components/ui/button';
import { isApiError } from '@/api/http';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';
import { cn } from '@/lib/utils';
import { uploadDocument, UploadCancelled, type DocumentMetadata } from './transfer';

/** Where files go and how to open what they became. */
export interface UploadTarget {
  /** The multipart endpoint, e.g. /v1/resources/{library}/documents. */
  url: string;
  /** Shown in the tray: the library, folder or inbox. */
  label: string;
  metadata?: (file: File) => DocumentMetadata | undefined;
  /** Where Open in the tray goes for an uploaded item. */
  open?: (itemId: string) => { to: string; params?: Record<string, string>; search?: Record<string, string | undefined> };
}

export interface UploadEntry {
  id: string;
  file: File;
  target: UploadTarget;
  status: 'queued' | 'uploading' | 'done' | 'failed' | 'cancelled';
  loaded: number;
  error?: string;
  /** Network and server failures can be retried; refused files cannot. */
  retryable?: boolean;
  result?: DocumentUpload;
}

interface Uploads {
  entries: UploadEntry[];
  /** Queues files; resolves with the uploads that succeeded once all of these files are finished. */
  upload: (files: File[], target: UploadTarget) => Promise<DocumentUpload[]>;
}

/** Upload failures in people's words: the server's generic conflict here means the file name is taken. */
function uploadError(error: unknown): string {
  if (isApiError(error, 409) && error.code === 'conflict')
    return 'A file with this name is already here. Rename the file, or replace the existing one from its File tab.';
  const message = problemMessage(error);
  return message.charAt(0).toUpperCase() + message.slice(1);
}

const UploadsContext = createContext<Uploads | null>(null);
const concurrentUploads = 2;

export function useUploads(): Uploads {
  const value = useContext(UploadsContext);
  if (!value) throw new Error('useUploads needs an UploadsProvider');
  return value;
}

/** Runs uploads for the whole app, two at a time, and shows them in a tray that survives navigation. */
export function UploadsProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const [entries, setEntries] = useState<UploadEntry[]>([]);
  const started = useRef(new Set<string>());
  const controllers = useRef(new Map<string, AbortController>());
  const waiters = useRef(new Map<string, (entry: UploadEntry) => void>());
  const patch = useCallback(
    (id: string, change: Partial<UploadEntry>) =>
      setEntries((current) => current.map((entry) => (entry.id === id ? { ...entry, ...change } : entry))),
    [],
  );

  const finish = useCallback(
    (entry: UploadEntry, change: Partial<UploadEntry>) => {
      patch(entry.id, change);
      controllers.current.delete(entry.id);
      waiters.current.get(entry.id)?.({ ...entry, ...change });
      waiters.current.delete(entry.id);
    },
    [patch],
  );

  const start = useCallback(
    (entry: UploadEntry) => {
      started.current.add(entry.id);
      const controller = new AbortController();
      controllers.current.set(entry.id, controller);
      patch(entry.id, { status: 'uploading', loaded: 0, error: undefined });
      uploadDocument(entry.target.url, entry.file, entry.target.metadata?.(entry.file), {
        signal: controller.signal,
        onProgress: (loaded) => patch(entry.id, { loaded }),
      }).then(
        async (result) => {
          finish(entry, { status: 'done', loaded: entry.file.size, result });
          await queryClient.invalidateQueries({ queryKey: keys.resources });
          await queryClient.invalidateQueries({ queryKey: keys.inboxes });
        },
        (error: unknown) =>
          finish(
            entry,
            error instanceof UploadCancelled
              ? { status: 'cancelled', retryable: true }
              : { status: 'failed', error: uploadError(error), retryable: !isApiError(error) || error.status === 0 || error.status >= 500 },
          ),
      );
    },
    [finish, patch, queryClient],
  );

  useEffect(() => {
    const running = entries.filter((entry) => entry.status === 'uploading').length;
    const waiting = entries.filter((entry) => entry.status === 'queued' && !started.current.has(entry.id));
    waiting.slice(0, Math.max(0, concurrentUploads - running)).forEach(start);
  }, [entries, start]);

  const upload = useCallback((files: File[], target: UploadTarget) => {
    const added: UploadEntry[] = files.map((file) => ({
      id: crypto.randomUUID(),
      file,
      target,
      status: 'queued',
      loaded: 0,
    }));
    const done = Promise.all(
      added.map((entry) => new Promise<UploadEntry>((resolve) => waiters.current.set(entry.id, resolve))),
    ).then((finished) => finished.flatMap((entry) => (entry.result ? [entry.result] : [])));
    setEntries((current) => [...current, ...added]);
    return done;
  }, []);

  const value = useMemo(() => ({ entries, upload }), [entries, upload]);
  return (
    <UploadsContext.Provider value={value}>
      {children}
      <UploadTray
        entries={entries}
        onCancel={(id) => {
          const controller = controllers.current.get(id);
          const entry = entries.find((candidate) => candidate.id === id);
          if (controller) controller.abort();
          else if (entry) {
            // Not started yet: never start it.
            started.current.add(id);
            finish(entry, { status: 'cancelled' });
          }
        }}
        onRetry={(id) => {
          started.current.delete(id);
          patch(id, { status: 'queued', loaded: 0, error: undefined });
        }}
        onClear={() =>
          setEntries((current) => current.filter((entry) => entry.status === 'queued' || entry.status === 'uploading'))
        }
      />
    </UploadsContext.Provider>
  );
}

function UploadTray({
  entries,
  onCancel,
  onRetry,
  onClear,
}: {
  entries: UploadEntry[];
  onCancel: (id: string) => void;
  onRetry: (id: string) => void;
  onClear: () => void;
}) {
  const format = useFormat();
  const navigate = useNavigate();
  const [collapsed, setCollapsed] = useState(false);
  if (entries.length === 0) return null;
  const active = entries.filter((entry) => entry.status === 'queued' || entry.status === 'uploading').length;
  const failed = entries.filter((entry) => entry.status === 'failed').length;
  const title = active
    ? `Uploading ${active} ${active === 1 ? 'file' : 'files'}`
    : failed
      ? `${failed} ${failed === 1 ? 'upload' : 'uploads'} failed`
      : `${entries.length} ${entries.length === 1 ? 'upload' : 'uploads'} complete`;
  return (
    <section
      aria-label="Uploads"
      className="fixed right-4 bottom-4 z-40 flex w-[min(24rem,calc(100vw-2rem))] flex-col overflow-hidden rounded-lg border bg-surface shadow-xl"
    >
      <header className="flex items-center gap-2 border-b px-3 py-2">
        <FileUp className="size-4 text-accent" />
        <h2 role="status" className="flex-1 text-[13px] font-semibold">
          {title}
        </h2>
        <Button
          size="icon"
          variant="ghost"
          className="size-7"
          aria-label={collapsed ? 'Show uploads' : 'Hide uploads'}
          aria-expanded={!collapsed}
          onClick={() => setCollapsed(!collapsed)}
        >
          {collapsed ? <ChevronUp /> : <ChevronDown />}
        </Button>
        {active === 0 && (
          <Button size="icon" variant="ghost" className="size-7" aria-label="Close uploads" onClick={onClear}>
            <X />
          </Button>
        )}
      </header>
      {!collapsed && (
        <ul className="max-h-72 divide-y overflow-y-auto">
          {entries.map((entry) => {
            const percent = entry.file.size ? Math.round((entry.loaded / entry.file.size) * 100) : 0;
            const duplicates = entry.result?.duplicates ?? [];
            const link = entry.result && entry.target.open?.(entry.result.item.id);
            return (
              <li key={entry.id} className="flex flex-col gap-1 px-3 py-2">
                <div className="flex items-center gap-2">
                  {entry.status === 'done' ? (
                    <Check className="size-4 shrink-0 text-success" />
                  ) : entry.status === 'failed' ? (
                    <AlertTriangle className="size-4 shrink-0 text-danger" />
                  ) : (
                    <FileUp className="size-4 shrink-0 text-muted" />
                  )}
                  <span className="min-w-0 flex-1 truncate text-[13px] font-medium" title={entry.file.name}>
                    {entry.file.name}
                  </span>
                  {(entry.status === 'queued' || entry.status === 'uploading') && (
                    <Button size="sm" variant="ghost" className="h-6 px-1.5" onClick={() => onCancel(entry.id)}>
                      Cancel
                    </Button>
                  )}
                  {(entry.status === 'failed' || entry.status === 'cancelled') && entry.retryable && (
                    <Button size="sm" variant="ghost" className="h-6 px-1.5" onClick={() => onRetry(entry.id)}>
                      <RotateCw /> Retry
                    </Button>
                  )}
                  {link && (
                    <Button size="sm" variant="ghost" className="h-6 px-1.5" onClick={() => void navigate(link as never)}>
                      Open
                    </Button>
                  )}
                </div>
                {entry.status === 'uploading' && (
                  <progress
                    aria-label={`Upload of ${entry.file.name}`}
                    value={percent}
                    max={100}
                    className="h-1.5 w-full accent-accent"
                  />
                )}
                <p className={cn('text-xs text-muted', entry.status === 'failed' && 'text-danger')}>
                  {entry.status === 'queued'
                    ? `Waiting · ${format.fileSize(entry.file.size)}`
                    : entry.status === 'uploading'
                      ? `${percent}% of ${format.fileSize(entry.file.size)} to ${entry.target.label}`
                      : entry.status === 'done'
                        ? `Uploaded to ${entry.target.label}`
                        : entry.status === 'cancelled'
                          ? 'Cancelled'
                          : entry.error}
                </p>
                {duplicates.length > 0 && (
                  <p className="flex items-center gap-1 text-xs">
                    <AlertTriangle className="size-3 shrink-0 text-warning" />
                    Already stored as {duplicates.map((duplicate) => duplicate.name).join(', ')}
                  </p>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
