import { keepPreviousData, useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { Eye, PencilLine, Save, X } from 'lucide-react';
import { useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { isApiError } from '@/api/http';
import { keys, resourceQuery } from '@/api/queries';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/feedback';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { replaceContent, UploadNameConflict, uploadDocument } from '@/features/documents/transfer';
import { usePeopleNames } from '@/features/fields/people';
import { ConfirmDialog } from '@/features/settings/section';
import { i18n, useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';
import { cn } from '@/lib/utils';
import type { EditSession, ViewerProps } from './handlers';
import { TextEditor } from './text-editor';
import { useViewerKeys, ViewerMessage } from './viewer-kit';

/** A text file's bytes as text: UTF-8 (a leading BOM dropped), or marked when they are not. */
interface Loaded {
  text: string;
  utf8: boolean;
}

function decode(buffer: ArrayBuffer): Loaded {
  try {
    return { text: new TextDecoder('utf-8', { fatal: true, ignoreBOM: false }).decode(buffer), utf8: true };
  } catch {
    return { text: new TextDecoder('utf-8').decode(buffer), utf8: false };
  }
}

/** Where the text being edited started: the file version and the item version it was read at. */
interface Base {
  blobId: string;
  version: number;
}

/** A text and the version it is. */
interface Snapshot extends Base {
  text: string;
}

/**
 * Unsaved text, kept in this browser per item with the version it started
 * from, so a crash or a closed tab loses nothing and a restored draft still
 * meets a newer file as a conflict.
 */
interface Draft extends Base {
  text: string;
  at: string;
}
const draftPrefix = 'papergo.draft.';
const draftKey = (itemId: string) => draftPrefix + itemId;
/** Drafts nobody came back to for this long are dropped. */
const draftDays = 30;
/** Typing writes the draft once it pauses this long, not at every key. */
const draftDelay = 600;

function readDraft(key: string): Draft | undefined {
  try {
    const raw = localStorage.getItem(key);
    const draft = raw ? (JSON.parse(raw) as Partial<Draft>) : undefined;
    return typeof draft?.text === 'string' && typeof draft.blobId === 'string' && typeof draft.version === 'number' && typeof draft.at === 'string' ? (draft as Draft) : undefined;
  } catch {
    return undefined;
  }
}

/** Drops drafts that are old or unreadable, to keep room for new ones. */
function pruneDrafts() {
  try {
    const oldest = Date.now() - draftDays * 86_400_000;
    for (let i = localStorage.length - 1; i >= 0; i--) {
      const key = localStorage.key(i);
      if (!key?.startsWith(draftPrefix)) continue;
      const draft = readDraft(key);
      if (!draft || !(Date.parse(draft.at) >= oldest)) localStorage.removeItem(key);
    }
  } catch {
    /* Storage that cannot be read holds no drafts to drop. */
  }
}

/** Keeps or forgets a draft; reports false when the browser has no room for it, even after old drafts go. */
function writeDraft(key: string, draft: Draft | undefined): boolean {
  const write = () => (draft ? localStorage.setItem(key, JSON.stringify(draft)) : localStorage.removeItem(key));
  try {
    write();
    return true;
  } catch {
    pruneDrafts();
    try {
      write();
      return true;
    } catch {
      return false;
    }
  }
}

let warnedNoRoom = false;

const mediaTypes = { markdown: 'text/markdown', plaintext: 'text/plain' };
const panes = ['write', 'preview'] as const;

/**
 * A text file in the viewer: read as rendered by render, and edited as
 * source with an optional live preview. Save makes a new file version with
 * the item version the text was read at; a newer version asks the person
 * what to keep. Unsaved text is guarded and kept in this browser.
 */
export function TextDocument({ file, source, editing, language, render, preview }: ViewerProps & { language: 'markdown' | 'plaintext'; render: (text: string) => ReactNode; preview?: (text: string) => ReactNode }) {
  const { t } = useTranslation(['files', 'common']);
  const client = useQueryClient();
  const format = useFormat();
  const editorId = useId();
  // The file's text, tagged with the version it is; while a newer version loads, the earlier one stays shown.
  const loaded = useQuery({
    queryKey: ['file-text', source.url, file.blobId],
    queryFn: async ({ signal }) => {
      const response = await fetch(source.url, { headers: source.headers, credentials: 'same-origin', signal });
      if (!response.ok) throw new Error(String(response.status));
      return { ...decode(await response.arrayBuffer()), blobId: file.blobId };
    },
    placeholderData: keepPreviousData,
    staleTime: Infinity,
    gcTime: 60_000,
    retry: false,
  });
  const key = draftKey(file.itemId);
  // The edit session: the last save made here, the text a draft started from, and the draft.
  const [saved, setSaved] = useState<Snapshot & { replaced: string }>();
  const [base, setBase] = useState<Snapshot>();
  const [draft, setDraft] = useState<string>();
  const [declined, setDeclined] = useState(false);
  const [pane, setPane] = useState<'write' | 'preview'>('write');
  const [conflict, setConflict] = useState(false);
  const [discarding, setDiscarding] = useState(false);
  const area = useRef<HTMLDivElement>(null);
  // Counts texts that replace the editor's from outside: a kept draft restored, or their version loaded.
  const [revision, setRevision] = useState(0);
  const version = editing?.version ?? 0;
  // The file as this editor knows it: its own last save until the item shows a file other than the one that
  // save replaced, then the file loaded. A file still loading has no known version, so a draft begun on it asks.
  const fresh = !!saved && (file.blobId === saved.replaced || file.blobId === saved.blobId);
  const current: Snapshot = fresh
    ? { ...saved, version: file.blobId === saved.blobId ? Math.max(saved.version, version) : saved.version }
    : { blobId: loaded.data?.blobId ?? file.blobId, version: loaded.data?.blobId === file.blobId ? version : 0, text: loaded.data?.text ?? '' };
  const baseline = base?.text ?? current.text;
  const active = !!editing?.active && !!editing.allowed && !!loaded.data?.utf8;
  const text = draft ?? current.text;
  const dirty = active && draft !== undefined && draft !== baseline;

  // The editor offers text this browser kept from an unsaved edit, until the person types or decides.
  const offering = active && draft === undefined && !declined;
  const kept = useMemo(() => (offering ? readDraft(key) : undefined), [offering, key]);
  const restorable = kept && kept.text !== current.text ? kept : undefined;

  // Drafts are written after typing pauses, and before the page goes away.
  const pending = useRef<{ key: string; draft: Draft }>(undefined);
  const timer = useRef<number>(undefined);
  const flush = useCallback(() => {
    window.clearTimeout(timer.current);
    const next = pending.current;
    pending.current = undefined;
    if (next && !writeDraft(next.key, next.draft) && !warnedNoRoom) {
      warnedNoRoom = true;
      toast.warning(i18n.t('files:edit.noRoom'));
    }
  }, []);
  const keep = (text: string, from: Base) => {
    pending.current = { key, draft: { text, at: new Date().toISOString(), blobId: from.blobId, version: from.version } };
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(flush, draftDelay);
  };
  const forget = () => {
    window.clearTimeout(timer.current);
    pending.current = undefined;
    writeDraft(key, undefined);
  };
  useEffect(() => {
    pruneDrafts();
    const hidden = () => document.visibilityState === 'hidden' && flush();
    window.addEventListener('pagehide', flush);
    document.addEventListener('visibilitychange', hidden);
    return () => {
      window.removeEventListener('pagehide', flush);
      document.removeEventListener('visibilitychange', hidden);
      flush();
    };
  }, [flush]);

  const reportDirty = editing?.onDirty;
  useEffect(() => {
    reportDirty?.(dirty);
  }, [dirty, reportDirty]);
  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);

  // A draft starts from the file as the editor knows it, or, restored, from the version it was kept with.
  const change = (value: string, restored?: Base) => {
    const from = restored ? { ...restored, text: current.text } : (base ?? current);
    if (from !== base) setBase(from);
    setDraft(value);
    if (value === from.text && !restored) forget();
    else keep(value, from);
  };
  const clear = () => {
    setDraft(undefined);
    setBase(undefined);
    forget();
    // Said at once, so leaving right after discarding does not ask again.
    editing?.onDirty(false);
  };
  const done = () => {
    clear();
    editing?.setActive(false);
  };

  const asFile = (body: string, name = file.filename) => new File([body], name, { type: mediaTypes[language] });
  const save = useMutation({
    meta: { silent: true },
    mutationFn: async ({ body, ifMatch }: { body: string; ifMatch: number; replaced: string }) => (await replaceContent(file.itemId, ifMatch, asFile(body))).data,
    onSuccess: async (result, { body, replaced }) => {
      const next = { blobId: result.id, version: result.resource_version, text: body };
      setSaved({ ...next, replaced });
      // Text typed while the save was on its way stays a draft, now based on the saved file.
      const rest = draftRef.current === body ? undefined : draftRef.current;
      if (rest === undefined) clear();
      else {
        setDraft(rest);
        setBase(next);
        keep(rest, next);
      }
      toast.success(t('edit.saved', { version: result.version }));
      await client.invalidateQueries({ queryKey: keys.resources });
    },
    onError: async (error, { body, ifMatch, replaced }) => {
      if (!isApiError(error, 412)) return void toast.error(problemMessage(error));
      // Only the item's details changed when its file is still the one edited: save again on the new version.
      const latest = await client.fetchQuery({ ...resourceQuery(file.itemId), staleTime: 0 }).catch(() => undefined);
      if (base && latest && latest.file?.blob_id === base.blobId && latest.version !== ifMatch) {
        setBase({ ...base, version: latest.version });
        save.mutate({ body, ifMatch: latest.version, replaced });
      } else setConflict(true);
    },
  });
  const draftRef = useRef(draft);
  useEffect(() => {
    draftRef.current = draft;
  });
  const submit = () => {
    if (dirty && base && !save.isPending) save.mutate({ body: text, ifMatch: base.version, replaced: base.blobId });
  };

  useViewerKeys(
    (event) => {
      if (!active) return false;
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') return submit();
      if (event.key === 'Escape') {
        // Dialogs above the editor, and Monaco's own popups such as suggestions and find, close first.
        if (!(event.target instanceof Node && area.current?.contains(event.target))) return false;
        if (document.querySelector('.monaco-editor :is(.suggest-widget, .find-widget, .parameter-hints-widget).visible')) return false;
        return dirty ? setDiscarding(true) : done();
      }
      return false;
    },
    { first: true },
  );

  if (loaded.isPending)
    return (
      <ViewerMessage title={t('viewer.opening')}>
        <Spinner />
      </ViewerMessage>
    );
  if (loaded.isError || !loaded.data) return <ViewerMessage title={t('viewer.failed')}>{t('viewer.downloadToOpen')}</ViewerMessage>;

  const notice = !loaded.data.utf8 ? t('edit.notUtf8') : editing && !editing.allowed && editing.reason ? editing.reason : undefined;
  if (!active)
    return (
      <div className="flex h-full flex-col">
        {notice && <p className="shrink-0 border-b bg-warning-soft px-4 py-2 text-[13px]">{notice}</p>}
        <div className="min-h-0 flex-1 overflow-auto bg-surface">{render(loaded.data.text)}</div>
      </div>
    );

  return (
    <div ref={area} className="flex h-full flex-col">
      <div role="toolbar" aria-label={t('edit.editing')} className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-surface px-3 py-1.5 text-[13px]">
        <span className="flex items-center gap-1.5 font-medium">
          <PencilLine className="size-4 text-accent" /> {t('edit.editing')}
        </span>
        <span aria-live="polite" className="text-xs text-muted">
          {save.isPending ? t('common:status.saving') : dirty ? t('edit.unsaved') : fresh ? t('edit.allSaved') : ''}
        </span>
        {preview && (
          <div role="tablist" aria-label={t('edit.panes')} className="ml-2 flex rounded-md bg-surface-muted p-0.5 lg:hidden">
            {panes.map((choice) => (
              <button key={choice} type="button" role="tab" aria-selected={pane === choice} onClick={() => setPane(choice)} className={cn('rounded px-2 py-0.5 text-xs font-medium text-muted', pane === choice && 'bg-surface text-foreground shadow-xs')}>
                {choice === 'write' ? t('edit.write') : t('edit.preview')}
              </button>
            ))}
          </div>
        )}
        <span className="ml-auto hidden text-xs text-muted md:inline">{t('edit.eachSave')}</span>
        <Button size="sm" onClick={() => (dirty ? setDiscarding(true) : done())}>
          <X /> {dirty ? t('common:actions.cancel') : t('common:actions.done')}
        </Button>
        <Button size="sm" variant="primary" disabled={!dirty || save.isPending} onClick={submit} title={t('edit.saveTitle')}>
          <Save /> {t('common:actions.save')}
        </Button>
      </div>
      {restorable && (
        <div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-accent-soft px-4 py-2 text-[13px]">
          <span className="flex-1">{t('edit.kept', { date: format.dateTime(restorable.at) })}</span>
          <Button
            size="sm"
            variant="primary"
            onClick={() => {
              change(restorable.text, { blobId: restorable.blobId, version: restorable.version });
              setRevision((current) => current + 1);
            }}
          >
            {t('edit.restore')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              forget();
              setDeclined(true);
            }}
          >
            {t('common:actions.discard')}
          </Button>
        </div>
      )}
      <div className="flex min-h-0 flex-1">
        <div className={cn('min-w-0 flex-1 border-r', preview && pane === 'preview' && 'hidden lg:block')}>
          {/* A clean editor follows the file; a draft keeps its own text. */}
          <TextEditor id={editorId} value={text} revision={draft === undefined ? `${revision}:${current.blobId}` : String(revision)} language={language} label={t('edit.editorLabel', { name: file.filename })} onChange={change} onSave={submit} />
        </div>
        {preview && (
          <div className={cn('min-w-0 flex-1 overflow-auto bg-surface', pane === 'write' && 'hidden lg:block')} aria-label={t('edit.preview')}>
            <p className="flex items-center gap-1.5 border-b px-4 py-1.5 text-xs text-muted">
              <Eye className="size-3.5" /> {t('edit.preview')}
            </p>
            {preview(text)}
          </div>
        )}
      </div>
      <ConfirmDialog
        open={discarding}
        onOpenChange={setDiscarding}
        title={t('edit.discardTitle')}
        description={t('edit.discardDescription')}
        confirm={t('edit.discardChanges')}
        onConfirm={() => {
          setDiscarding(false);
          done();
        }}
      />
      {conflict && (
        <ConflictDialog
          itemId={file.itemId}
          text={text}
          filename={file.filename}
          history={!!editing?.history}
          busy={save.isPending}
          onClose={() => setConflict(false)}
          onTheirs={async (copied) => {
            setConflict(false);
            setSaved(undefined);
            clear();
            await client.invalidateQueries({ queryKey: keys.resources });
            await client.invalidateQueries({ queryKey: ['file-text', source.url] });
            setRevision((current) => current + 1);
            toast.success(copied ? t('edit.loadedTheirsCopied') : t('edit.loadedTheirs'));
          }}
          onMine={async () => {
            const latest = await client.fetchQuery({ ...resourceQuery(file.itemId), staleTime: 0 });
            setConflict(false);
            // Theirs is the base now, so later saves conflict only with newer changes.
            const theirs = latest.file?.blob_id ?? '';
            if (base) setBase({ ...base, blobId: theirs, version: latest.version });
            save.mutate({ body: text, ifMatch: latest.version, replaced: theirs });
          }}
          onCopy={async () => {
            try {
              const name = await saveCopy(client, file, editing!, (name) => asFile(text, name));
              setConflict(false);
              done();
              await client.invalidateQueries({ queryKey: keys.resources });
              toast.success(t('edit.savedAs', { name }));
            } catch (error) {
              toast.error(problemMessage(error));
            }
          }}
        />
      )}
    </div>
  );
}

/**
 * Saves text as a new item beside the file, with the item's content type,
 * keywords and values, as "name (copy).ext", numbered when that is taken.
 * Returns the name it got; a refusal leaves the editor as it was.
 */
async function saveCopy(client: QueryClient, file: ViewerProps['file'], editing: EditSession, asFile: (name: string) => File) {
  const source = await client.fetchQuery({ ...resourceQuery(file.itemId), staleTime: 0 });
  const dot = file.filename.lastIndexOf('.');
  const [stem, extension] = dot > 0 ? [file.filename.slice(0, dot), file.filename.slice(dot)] : [file.filename, ''];
  for (let n = 1; ; n++) {
    const name = n > 1 ? i18n.t('files:edit.copyNameNumbered', { stem, number: n, extension }) : i18n.t('files:edit.copyName', { stem, extension });
    try {
      await uploadDocument(`/v1/resources/${editing.containerId}/documents`, asFile(name), {
        name,
        folder_id: editing.folderId,
        content_type_id: source.content_type_id,
        keywords: source.keywords ?? undefined,
        values: source.values ?? undefined,
      });
      return name;
    } catch (error) {
      if (!(error instanceof UploadNameConflict) || n >= 20) throw error;
    }
  }
}

/** Copies text to the clipboard, reporting whether the browser let it. */
async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

/** Gives the person their text as a file, for when the clipboard cannot hold it. */
function downloadText(text: string, filename: string) {
  const dot = filename.lastIndexOf('.');
  const [stem, extension] = dot > 0 ? [filename.slice(0, dot), filename.slice(dot)] : [filename, ''];
  const name = i18n.t('files:edit.mineName', { stem, extension });
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }));
  const link = Object.assign(document.createElement('a'), { href: url, download: name });
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

/**
 * Someone saved the file since it was opened: keep theirs, keep mine, or keep
 * both. Loading theirs keeps the person's text first: on the clipboard, or,
 * when the browser refuses, in a box to copy and a file to download, until
 * they choose to let it go. Saving mine over theirs says what the library
 * keeps of theirs.
 */
function ConflictDialog({
  itemId,
  text,
  filename,
  history,
  busy,
  onClose,
  onTheirs,
  onMine,
  onCopy,
}: {
  itemId: string;
  text: string;
  filename: string;
  history: boolean;
  busy: boolean;
  onClose: () => void;
  onTheirs: (copied: boolean) => void;
  onMine: () => void;
  onCopy: () => void;
}) {
  const { t } = useTranslation('files');
  const latest = useQuery({ ...resourceQuery(itemId), staleTime: 0 });
  const names = usePeopleNames();
  const format = useFormat();
  const [uncopied, setUncopied] = useState(false);
  const who = latest.data?.updated_by ? (names.get(latest.data.updated_by) ?? latest.data.updated_by) : t('conflict.someone');
  if (uncopied)
    return (
      <Dialog open onOpenChange={(open) => !open && onClose()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('conflict.keepFirst')}</DialogTitle>
            <DialogDescription>{t('conflict.copyFailed')}</DialogDescription>
          </DialogHeader>
          <div className="grid gap-2 px-5 pb-2">
            <textarea readOnly aria-label={t('conflict.yourText')} value={text} rows={8} className="w-full rounded-md border bg-surface-muted p-2 font-mono text-xs" onFocus={(event) => event.currentTarget.select()} />
            <Button onClick={() => downloadText(text, filename)}>{t('conflict.downloadYours')}</Button>
          </div>
          <DialogFooter>
            <Button variant="ghost" onClick={onClose}>
              {t('conflict.keepEditing')}
            </Button>
            <Button variant="danger" onClick={() => onTheirs(false)}>
              {t('conflict.loadTheirs')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('conflict.changed')}</DialogTitle>
          <DialogDescription>
            {latest.data?.updated_at ? t('conflict.savedByWhen', { who, when: format.relative(latest.data.updated_at) }) : t('conflict.savedBy', { who })}
            {!history && ` ${t('conflict.noHistory')}`}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2 px-5 pb-2">
          <Button disabled={busy} onClick={async () => ((await copyText(text)) ? onTheirs(true) : setUncopied(true))}>
            {t('conflict.loadTheirsClipboard')}
          </Button>
          <Button disabled={busy} onClick={onMine}>
            {history ? t('conflict.saveMineAnyway') : t('conflict.replaceTheirs')}
          </Button>
          <Button disabled={busy} onClick={onCopy}>
            {t('conflict.saveAsNew')}
          </Button>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t('conflict.keepEditing')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
