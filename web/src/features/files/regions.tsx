import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useRef, useState } from 'react';
import { api, continuation } from '@/api/http';
import { keys } from '@/api/queries';
import type { Comment, CommentPage, CommentThread } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/input';
import { problemMessage } from '@/lib/errors';
import { cn } from '@/lib/utils';
import type { FileRef } from './handlers';

/**
 * A thread's place on a page of one file version: a rectangle as fractions
 * of the upright page's width and height, from its top left.
 */
export interface FileRegion {
  kind: 'file.region';
  blob_id: string;
  page: number;
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** The region an anchor names, if it is a well-formed file region. */
export function fileRegion(anchor: unknown): FileRegion | undefined {
  const a = anchor as Partial<FileRegion> | undefined;
  if (!a || a.kind !== 'file.region' || typeof a.blob_id !== 'string' || !Number.isInteger(a.page) || a.page! < 1) return undefined;
  const parts = [a.x, a.y, a.w, a.h];
  if (!parts.every((n) => typeof n === 'number' && n >= 0 && n <= 1)) return undefined;
  return a as FileRegion;
}

/** A rectangle as shown on a page turned clockwise by rotation degrees, from the upright page's. */
export function turn(rect: Rect, rotation: number): Rect {
  const corners: [number, number][] = [
    [rect.x, rect.y],
    [rect.x + rect.w, rect.y + rect.h],
  ];
  const point = ([x, y]: [number, number]): [number, number] => {
    switch (((rotation % 360) + 360) % 360) {
      case 90:
        return [1 - y, x];
      case 180:
        return [1 - x, 1 - y];
      case 270:
        return [y, 1 - x];
      default:
        return [x, y];
    }
  };
  const [[x1, y1], [x2, y2]] = corners.map(point);
  return { x: Math.min(x1, x2), y: Math.min(y1, y2), w: Math.abs(x2 - x1), h: Math.abs(y2 - y1) };
}

/** The upright page's rectangle of one drawn on a page turned by rotation. */
export function unturn(rect: Rect, rotation: number): Rect {
  return turn(rect, 360 - (((rotation % 360) + 360) % 360));
}

const round = (n: number) => Math.round(n * 10_000) / 10_000;

/**
 * The item's threads anchored to regions of its file, all states, with
 * whether the reader may start one. Absent where comments are off.
 */
export function useRegionThreads(file: FileRef, enabled = true) {
  return useQuery({
    queryKey: [...keys.comments, file.itemId, 'regions'],
    enabled: enabled && !file.detached,
    retry: false,
    staleTime: 30_000,
    queryFn: async () => {
      const threads: CommentThread[] = [];
      let path: string | undefined = `/v1/items/${file.itemId}/comments`;
      let canPost = false;
      // A few pages are plenty for pins; a file with more regions shows the first.
      for (let pages = 0; path && pages < 5; pages++) {
        const page: CommentPage = (await api<CommentPage>(path, { query: pages === 0 ? { status: 'all', anchor_kind: 'file.region', $top: 100 } : undefined })).data;
        if (pages === 0) canPost = page.can_post;
        threads.push(...(page.value ?? []));
        path = continuation(page['@odata.nextLink']) ?? undefined;
      }
      return { threads, canPost };
    },
  });
}

/** The threads on one page of one file version, numbered in the order they started. */
export function threadsOn(threads: CommentThread[] | undefined, blobId: string) {
  const out: { thread: CommentThread; region: FileRegion; number: number }[] = [];
  for (const thread of threads ?? []) {
    const region = fileRegion(thread.anchor);
    if (region && region.blob_id === blobId) out.push({ thread, region, number: out.length + 1 });
  }
  return out;
}

/**
 * What a page shows of its regions: their pins, the one asked for
 * highlighted, and, while commenting, a layer to draw a new region on and
 * the box to write about it.
 */
export function PageRegions({
  file,
  page,
  rotation,
  pins,
  focus,
  drawing,
  onOpen,
  onDone,
}: {
  file: FileRef;
  page: number;
  rotation: number;
  pins: ReturnType<typeof threadsOn>;
  focus?: string;
  drawing: boolean;
  onOpen: (threadId: string) => void;
  onDone: (threadId?: string) => void;
}) {
  const layer = useRef<HTMLDivElement>(null);
  const [start, setStart] = useState<{ x: number; y: number }>();
  const [rect, setRect] = useState<Rect>();
  const [placed, setPlaced] = useState<Rect>();
  const at = (event: React.PointerEvent) => {
    const box = layer.current!.getBoundingClientRect();
    return { x: Math.min(1, Math.max(0, (event.clientX - box.left) / box.width)), y: Math.min(1, Math.max(0, (event.clientY - box.top) / box.height)) };
  };
  const mine = pins.filter((pin) => pin.region.page === page);
  return (
    <div className="pointer-events-none absolute inset-0 z-[5]">
      {mine.map(({ thread, region, number }) => {
        const shown = turn(region, rotation);
        const focused = thread.id === focus;
        return (
          <div key={thread.id}>
            <div
              aria-hidden
              className={cn('absolute rounded-sm border-2', focused ? 'border-accent bg-accent/15' : 'border-amber-500/70 bg-amber-300/15', thread.resolved_at && !focused && 'opacity-40')}
              style={{ left: `${shown.x * 100}%`, top: `${shown.y * 100}%`, width: `${shown.w * 100}%`, height: `${shown.h * 100}%` }}
            />
            <button
              type="button"
              className={cn(
                'pointer-events-auto absolute grid size-6 -translate-x-1/2 -translate-y-1/2 place-items-center rounded-full text-xs font-semibold text-white shadow ring-2 ring-white',
                focused ? 'bg-accent' : thread.resolved_at ? 'bg-zinc-400' : 'bg-amber-500',
              )}
              style={{ left: `${(shown.x + shown.w) * 100}%`, top: `${shown.y * 100}%` }}
              aria-label={`Comment ${number} by ${thread.author.name}${thread.resolved_at ? ', resolved' : ''}`}
              title={thread.body.slice(0, 120)}
              onClick={() => onOpen(thread.id)}
            >
              {number}
            </button>
          </div>
        );
      })}
      {drawing && !placed && (
        <div
          ref={layer}
          role="presentation"
          className="pointer-events-auto absolute inset-0 cursor-crosshair touch-none"
          onPointerDown={(event) => {
            event.currentTarget.setPointerCapture(event.pointerId);
            setStart(at(event));
            setRect(undefined);
          }}
          onPointerMove={(event) => {
            if (!start) return;
            const p = at(event);
            setRect({ x: Math.min(start.x, p.x), y: Math.min(start.y, p.y), w: Math.abs(p.x - start.x), h: Math.abs(p.y - start.y) });
          }}
          onPointerUp={(event) => {
            if (!start) return;
            const p = at(event);
            // A click marks a small area around it.
            const r = rect && rect.w > 0.01 && rect.h > 0.01 ? rect : { x: Math.max(0, p.x - 0.03), y: Math.max(0, p.y - 0.015), w: 0.06, h: 0.03 };
            setStart(undefined);
            setRect(undefined);
            setPlaced(r);
          }}
        >
          {rect && <div className="absolute border-2 border-dashed border-accent bg-accent/10" style={{ left: `${rect.x * 100}%`, top: `${rect.y * 100}%`, width: `${rect.w * 100}%`, height: `${rect.h * 100}%` }} />}
        </div>
      )}
      {placed && (
        <RegionComposer
          file={file}
          region={{ kind: 'file.region', blob_id: file.blobId, page, ...unturn(placed, rotation) }}
          shown={placed}
          onDone={(id) => {
            setPlaced(undefined);
            onDone(id);
          }}
        />
      )}
    </div>
  );
}

/** The box to write a comment about a region just drawn, beside it on the page. */
function RegionComposer({ file, region, shown, onDone }: { file: FileRef; region: FileRegion; shown: Rect; onDone: (threadId?: string) => void }) {
  const client = useQueryClient();
  const [text, setText] = useState('');
  const post = useMutation({
    meta: { silent: true },
    mutationFn: async () =>
      (
        await api<Comment>(`/v1/items/${file.itemId}/comments`, {
          method: 'POST',
          body: { body: text.trim(), anchor: { ...region, x: round(region.x), y: round(region.y), w: round(region.w), h: round(region.h) } },
        })
      ).data,
    onSuccess: async (comment) => {
      await client.invalidateQueries({ queryKey: keys.comments });
      onDone(comment.id);
    },
  });
  const below = shown.y + shown.h < 0.75;
  return (
    <>
      <div className="absolute border-2 border-accent bg-accent/10" style={{ left: `${shown.x * 100}%`, top: `${shown.y * 100}%`, width: `${shown.w * 100}%`, height: `${shown.h * 100}%` }} />
      <form
        className="pointer-events-auto absolute z-10 flex w-72 flex-col gap-2 rounded-lg border bg-surface p-2 shadow-lg"
        style={{ left: `min(${shown.x * 100}%, calc(100% - 18rem))`, ...(below ? { top: `calc(${(shown.y + shown.h) * 100}% + 6px)` } : { bottom: `calc(${(1 - shown.y) * 100}% + 6px)` }) }}
        onSubmit={(event) => {
          event.preventDefault();
          if (text.trim()) post.mutate();
        }}
        onKeyDown={(event) => {
          if (event.key === 'Escape') {
            event.stopPropagation();
            onDone();
          }
        }}
      >
        <Textarea aria-label="Comment on this area" placeholder="Comment on this area" rows={3} autoFocus value={text} onChange={(event) => setText(event.target.value)} />
        {post.error && <p className="text-xs text-danger">{problemMessage(post.error)}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" size="sm" onClick={() => onDone()}>
            Cancel
          </Button>
          <Button type="submit" size="sm" variant="primary" disabled={!text.trim() || post.isPending}>
            Comment
          </Button>
        </div>
      </form>
    </>
  );
}
