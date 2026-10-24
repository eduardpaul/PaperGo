import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Eye, PencilLine, Save, X } from 'lucide-react';
import { useEffect, useId, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { isApiError } from '@/api/http';
import { keys, resourceQuery } from '@/api/queries';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/feedback';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { replaceContent, uploadDocument } from '@/features/documents/transfer';
import { usePeopleNames } from '@/features/fields/people';
import { ConfirmDialog } from '@/features/settings/section';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';
import { cn } from '@/lib/utils';
import type { ViewerProps } from './handlers';
import { TextEditor } from './text-editor';
import { typing, useViewerKeys, ViewerMessage } from './viewer-kit';

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

/** Unsaved text, kept in this browser per item and file version, so a crash or a closed tab loses nothing. */
interface Draft {
  text: string;
  at: string;
}
const draftKey = (itemId: string, blobId: string) => `papergo.draft.${itemId}.${blobId}`;
function readDraft(key: string): Draft | undefined {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as Draft) : undefined;
  } catch {
    return undefined;
  }
}
function writeDraft(key: string, text: string | undefined) {
  try {
    if (text === undefined) localStorage.removeItem(key);
    else localStorage.setItem(key, JSON.stringify({ text, at: new Date().toISOString() }));
  } catch {
    /* Without storage the editor still guards unsaved text while it is open. */
  }
}

const mediaTypes = { markdown: 'text/markdown', plaintext: 'text/plain' };

/**
 * A text file in the viewer: read as rendered by render, and edited as
 * source with an optional live preview. Save makes a new file version with
 * the item version the text was read at; a newer version asks the person
 * what to keep. Unsaved text is guarded and kept in this browser.
 */
export function TextDocument({ file, source, editing, language, render, preview }: ViewerProps & { language: 'markdown' | 'plaintext'; render: (text: string) => ReactNode; preview?: (text: string) => ReactNode }) {
  const client = useQueryClient();
  const format = useFormat();
  const editorId = useId();
  const loaded = useQuery({
    queryKey: ['file-text', source.url, file.blobId],
    queryFn: async ({ signal }) => {
      const response = await fetch(source.url, { headers: source.headers, credentials: 'same-origin', signal });
      if (!response.ok) throw new Error(String(response.status));
      return decode(await response.arrayBuffer());
    },
    staleTime: Infinity,
    gcTime: 60_000,
    retry: false,
  });
  const key = draftKey(file.itemId, file.blobId);
  const [draft, setDraft] = useState<string>();
  const [saved, setSaved] = useState<{ version: number; text: string }>();
  const [declined, setDeclined] = useState(false);
  const [pane, setPane] = useState<'write' | 'preview'>('write');
  const [conflict, setConflict] = useState(false);
  const [discarding, setDiscarding] = useState(false);
  const baseline = saved?.text ?? loaded.data?.text ?? '';
  const active = !!editing?.active && !!editing.allowed && !!loaded.data?.utf8;
  const text = draft ?? baseline;
  const dirty = active && draft !== undefined && draft !== baseline;
  const version = Math.max(editing?.version ?? 0, saved?.version ?? 0);

  // The editor offers text this browser kept from an unsaved edit, until the person types or decides.
  const kept = active && draft === undefined && !declined ? readDraft(key) : undefined;
  const restorable = kept && kept.text !== baseline ? kept : undefined;

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

  const change = (value: string) => {
    setDraft(value);
    writeDraft(key, value === baseline ? undefined : value);
  };
  const done = () => {
    setDraft(undefined);
    writeDraft(key, undefined);
    editing?.setActive(false);
  };

  const asFile = (body: string, name = file.filename) => new File([body], name, { type: mediaTypes[language] });
  const save = useMutation({
    meta: { silent: true },
    mutationFn: async ({ body, ifMatch }: { body: string; ifMatch: number }) => (await replaceContent(file.itemId, ifMatch, asFile(body))).data,
    onSuccess: async (result, { body }) => {
      setSaved({ version: result.resource_version, text: body });
      setDraft(undefined);
      writeDraft(key, undefined);
      toast.success(`Saved · version ${result.version}`);
      await client.invalidateQueries({ queryKey: keys.resources });
    },
    onError: (error) => {
      if (isApiError(error, 412)) setConflict(true);
      else toast.error(problemMessage(error));
    },
  });
  const submit = () => {
    if (dirty && !save.isPending) save.mutate({ body: text, ifMatch: version });
  };

  useViewerKeys(
    (event) => {
      if (!active) return false;
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') return submit();
      if (event.key === 'Escape' && !typing(event)) return dirty ? setDiscarding(true) : done();
      return false;
    },
    { first: true },
  );

  if (loaded.isPending)
    return (
      <ViewerMessage title="Opening…">
        <Spinner />
      </ViewerMessage>
    );
  if (loaded.isError || !loaded.data) return <ViewerMessage title="This file could not be shown.">Download it to open it in another application.</ViewerMessage>;

  const notice = !loaded.data.utf8 ? "This file isn't UTF-8 text, so it opens read-only." : editing && !editing.allowed && editing.reason ? editing.reason : undefined;
  if (!active)
    return (
      <div className="flex h-full flex-col">
        {notice && <p className="shrink-0 border-b bg-warning-soft px-4 py-2 text-[13px]">{notice}</p>}
        <div className="min-h-0 flex-1 overflow-auto bg-surface">{render(loaded.data.text)}</div>
      </div>
    );

  return (
    <div className="flex h-full flex-col">
      <div role="toolbar" aria-label="Editing" className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-surface px-3 py-1.5 text-[13px]">
        <span className="flex items-center gap-1.5 font-medium">
          <PencilLine className="size-4 text-accent" /> Editing
        </span>
        <span aria-live="polite" className="text-xs text-muted">
          {save.isPending ? 'Saving…' : dirty ? 'Unsaved changes' : saved ? 'All changes saved' : ''}
        </span>
        {preview && (
          <div role="tablist" aria-label="Editor panes" className="ml-2 flex rounded-md bg-surface-muted p-0.5 lg:hidden">
            {(['write', 'preview'] as const).map((choice) => (
              <button key={choice} type="button" role="tab" aria-selected={pane === choice} onClick={() => setPane(choice)} className={cn('rounded px-2 py-0.5 text-xs font-medium text-muted', pane === choice && 'bg-surface text-foreground shadow-xs')}>
                {choice === 'write' ? 'Write' : 'Preview'}
              </button>
            ))}
          </div>
        )}
        <span className="ml-auto hidden text-xs text-muted md:inline">Each save is a new file version and starts the file's workflows.</span>
        <Button size="sm" onClick={() => (dirty ? setDiscarding(true) : done())}>
          <X /> {dirty ? 'Cancel' : 'Done'}
        </Button>
        <Button size="sm" variant="primary" disabled={!dirty || save.isPending} onClick={submit} title="Save (Ctrl+S)">
          <Save /> Save
        </Button>
      </div>
      {restorable && (
        <div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-accent-soft px-4 py-2 text-[13px]">
          <span className="flex-1">This browser kept unsaved changes to this file from {format.dateTime(restorable.at)}.</span>
          <Button
            size="sm"
            variant="primary"
            onClick={() => change(restorable.text)}
          >
            Restore them
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              writeDraft(key, undefined);
              setDeclined(true);
            }}
          >
            Discard
          </Button>
        </div>
      )}
      <div className="flex min-h-0 flex-1">
        <div className={cn('min-w-0 flex-1 border-r', preview && pane === 'preview' && 'hidden lg:block')}>
          <TextEditor id={editorId} value={text} language={language} label={`Text of ${file.filename}`} onChange={change} onSave={submit} />
        </div>
        {preview && (
          <div className={cn('min-w-0 flex-1 overflow-auto bg-surface', pane === 'write' && 'hidden lg:block')} aria-label="Preview">
            <p className="flex items-center gap-1.5 border-b px-4 py-1.5 text-xs text-muted">
              <Eye className="size-3.5" /> Preview
            </p>
            {preview(text)}
          </div>
        )}
      </div>
      <ConfirmDialog
        open={discarding}
        onOpenChange={setDiscarding}
        title="Discard unsaved changes?"
        description="Your changes to this file have not been saved."
        confirm="Discard changes"
        onConfirm={() => {
          setDiscarding(false);
          done();
        }}
      />
      {conflict && (
        <ConflictDialog
          itemId={file.itemId}
          busy={save.isPending}
          onClose={() => setConflict(false)}
          onTheirs={async () => {
            await navigator.clipboard?.writeText(text).catch(() => undefined);
            setConflict(false);
            setSaved(undefined);
            setDraft(undefined);
            writeDraft(key, undefined);
            await client.invalidateQueries({ queryKey: keys.resources });
            await client.invalidateQueries({ queryKey: ['file-text', source.url] });
            toast.success('Loaded their version. Your text is on the clipboard.');
          }}
          onMine={async () => {
            const latest = await client.fetchQuery({ ...resourceQuery(file.itemId), staleTime: 0 });
            setConflict(false);
            save.mutate({ body: text, ifMatch: latest.version });
          }}
          onCopy={async () => {
            const dot = file.filename.lastIndexOf('.');
            const name = dot > 0 ? `${file.filename.slice(0, dot)} (copy)${file.filename.slice(dot)}` : `${file.filename} (copy)`;
            try {
              await uploadDocument(`/v1/resources/${editing!.containerId}/documents`, asFile(text, name), { name, folder_id: editing!.folderId });
              setConflict(false);
              done();
              await client.invalidateQueries({ queryKey: keys.resources });
              toast.success(`Saved your text as ${name}`);
            } catch (error) {
              toast.error(problemMessage(error));
            }
          }}
        />
      )}
    </div>
  );
}

/** Someone saved the file since it was opened: keep theirs, keep mine, or keep both. */
function ConflictDialog({ itemId, busy, onClose, onTheirs, onMine, onCopy }: { itemId: string; busy: boolean; onClose: () => void; onTheirs: () => void; onMine: () => void; onCopy: () => void }) {
  const latest = useQuery({ ...resourceQuery(itemId), staleTime: 0 });
  const names = usePeopleNames();
  const format = useFormat();
  const who = latest.data?.updated_by ? (names.get(latest.data.updated_by) ?? latest.data.updated_by) : 'Someone';
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>This file changed while you were editing</DialogTitle>
          <DialogDescription>
            {who} saved it{latest.data?.updated_at ? ` ${format.relative(latest.data.updated_at)}` : ''}. Choose what to keep; nothing is lost.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2 px-5 pb-2">
          <Button disabled={busy} onClick={onTheirs}>
            Load their version (your text goes to the clipboard)
          </Button>
          <Button disabled={busy} onClick={onMine}>
            Save mine anyway (theirs stays in the file history)
          </Button>
          <Button disabled={busy} onClick={onCopy}>
            Save mine as a new file
          </Button>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Keep editing
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
