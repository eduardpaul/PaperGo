import { useQuery } from '@tanstack/react-query';
import type * as Api from 'monaco-editor/editor/editor.api';
import { ArrowDown, ArrowUp } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { fetchBlob } from '@/api/files';
import { downloadContentPath } from '@/api/requests';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Spinner } from '@/components/ui/feedback';
import { problemMessage } from '@/lib/errors';

/** A file version a comparison reads: the item's current file has no blob ID. */
export interface ComparedVersion {
  version: number;
  blobId?: string;
  contentType: string;
}

/** Whether two versions of a file can be compared as text. */
export function comparable(contentType: string) {
  return contentType === 'text/markdown' || contentType === 'text/plain';
}

/**
 * What changed between two versions of a text file, line by line: side by
 * side on wide screens, inline on narrow ones, read-only.
 */
export function TextCompareDialog({ itemId, filename, from, to, onClose }: { itemId: string; filename: string; from: ComparedVersion; to: ComparedVersion; onClose: () => void }) {
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex h-[85vh] w-[min(1200px,calc(100vw-2rem))] max-w-none flex-col">
        <DialogHeader>
          <DialogTitle>
            Changes from version {from.version} to version {to.version}
          </DialogTitle>
          <DialogDescription>{filename}</DialogDescription>
        </DialogHeader>
        <Comparison itemId={itemId} from={from} to={to} language={from.contentType === 'text/markdown' ? 'markdown' : 'plaintext'} />
      </DialogContent>
    </Dialog>
  );
}

function useText(itemId: string, version: ComparedVersion) {
  return useQuery({
    queryKey: ['file-version-text', itemId, version.blobId ?? 'current', version.version],
    queryFn: async () => new TextDecoder('utf-8').decode(await (await fetchBlob(downloadContentPath(itemId, version.blobId))).arrayBuffer()),
    staleTime: Infinity,
  });
}

function Comparison({ itemId, from, to, language }: { itemId: string; from: ComparedVersion; to: ComparedVersion; language: 'markdown' | 'plaintext' }) {
  const before = useText(itemId, from);
  const after = useText(itemId, to);
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<Api.editor.IStandaloneDiffEditor>(undefined);
  const [changes, setChanges] = useState<number>();
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (before.data === undefined || after.data === undefined || !host.current) return;
    let live = true;
    let dispose = () => {};
    import('./monaco-text').then(({ monaco }) => {
      if (!live || !host.current) return;
      const original = monaco.editor.createModel(before.data!, language);
      const modified = monaco.editor.createModel(after.data!, language);
      const diff = monaco.editor.createDiffEditor(host.current, {
        readOnly: true,
        originalEditable: false,
        automaticLayout: true,
        renderSideBySide: host.current.clientWidth >= 800,
        useInlineViewWhenSpaceIsLimited: true,
        wordWrap: 'on',
        diffWordWrap: 'on',
        minimap: { enabled: false },
        scrollBeyondLastLine: false,
        fontSize: 13,
        theme: document.documentElement.classList.contains('dark') ? 'vs-dark' : 'vs',
      });
      diff.setModel({ original, modified });
      editor.current = diff;
      const updated = diff.onDidUpdateDiff(() => setChanges(diff.getLineChanges()?.length ?? 0));
      dispose = () => {
        updated.dispose();
        diff.dispose();
        original.dispose();
        modified.dispose();
        editor.current = undefined;
      };
    }, () => live && setFailed(true));
    return () => {
      live = false;
      dispose();
    };
  }, [before.data, after.data, language]);

  const error = before.error ?? after.error;
  if (error) return <p className="px-5 text-[13px] text-danger">{problemMessage(error)}</p>;
  if (failed) return <p className="px-5 text-[13px] text-danger">The comparison could not be shown.</p>;
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2 px-5 pb-5">
      <div className="flex items-center gap-2 text-[13px]">
        <span aria-live="polite" className="text-muted">
          {changes === undefined ? 'Comparing…' : changes === 0 ? 'The texts are the same.' : `${changes} change${changes === 1 ? '' : 's'}`}
        </span>
        {!!changes && (
          <>
            <Button size="icon" variant="ghost" aria-label="Previous change" onClick={() => editor.current?.goToDiff('previous')}>
              <ArrowUp />
            </Button>
            <Button size="icon" variant="ghost" aria-label="Next change" onClick={() => editor.current?.goToDiff('next')}>
              <ArrowDown />
            </Button>
          </>
        )}
      </div>
      <div className="relative min-h-0 flex-1 overflow-hidden rounded-md border">
        <div ref={host} className="absolute inset-0" />
        {changes === undefined && (
          <div className="absolute inset-0 grid place-items-center">
            <Spinner />
          </div>
        )}
      </div>
    </div>
  );
}
