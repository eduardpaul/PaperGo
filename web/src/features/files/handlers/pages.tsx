import { useQuery } from '@tanstack/react-query';
import { ChevronDown, ChevronUp, RotateCw, Search, X, ZoomIn, ZoomOut } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import { api } from '@/api/http';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { problemMessage } from '@/lib/errors';
import { useBlobUrl } from '@/lib/use-blob-url';
import type { FileRef, ViewerProps } from '../handlers';
import { PageMatches, pagesMatching, useReadText } from '../text-find';
import { typing, useViewerKeys, ViewerBar, ViewerMessage } from '../viewer-kit';

interface PageList {
  blob_id: string;
  pages: { width: number; height: number }[];
  truncated: boolean;
}

/** The widths the server renders pages at; a page asks for the smallest that covers it. */
const widths = [320, 480, 640, 960, 1280, 1600, 2048];
const renditionWidth = (pixels: number) => widths.find((width) => width >= pixels) ?? widths[widths.length - 1];

const zooms = [
  ['fit', 'Fit width'],
  ['0.5', '50%'],
  ['0.75', '75%'],
  ['1', '100%'],
  ['1.5', '150%'],
  ['2', '200%'],
] as const;

/** The pages of a TIFF scan, rendered by the server one by one. */
export default function PagesView(props: ViewerProps) {
  return <PageImages {...props} />;
}

/**
 * A file's pages as images the server renders, for files the browser cannot
 * draw itself (TIFF scans) and as a light way to read very large PDFs. Pages
 * load as they scroll into view, at the width the screen needs. Find goes
 * through the text PaperGo read from the file, page by page.
 */
export function PageImages({ file, page, onPage, find: initialFind, notice }: ViewerProps & { notice?: ReactNode }) {
  const list = useQuery({
    queryKey: ['file-pages', file.itemId, file.blobId],
    queryFn: async () => (await api<PageList>(`/v1/items/${file.itemId}/pages`, { query: { blob_id: file.blobId } })).data,
    staleTime: Infinity,
    retry: false,
  });
  const frame = useRef<HTMLDivElement>(null);
  const [frameWidth, setFrameWidth] = useState(0);
  const [zoom, setZoom] = useState<string>('fit');
  const [rotation, setRotation] = useState(0);
  const [current, setCurrent] = useState(page ?? 1);
  const [finding, setFinding] = useState(!!initialFind);
  const [query, setQuery] = useState(initialFind ?? '');
  const findInput = useRef<HTMLInputElement>(null);
  const text = useReadText(file, finding);
  const matches = pagesMatching(text.data, query);
  const reportPage = useRef(onPage);
  useEffect(() => {
    reportPage.current = onPage;
  });

  useLayoutEffect(() => {
    const element = frame.current;
    if (!element) return;
    const observer = new ResizeObserver(() => setFrameWidth(element.clientWidth));
    observer.observe(element);
    return () => observer.disconnect();
  }, [list.data]);

  const pages = list.data?.pages ?? [];
  const turned = rotation % 180 !== 0;
  const displayWidth = (size: { width: number; height: number }) => {
    const width = turned ? size.height : size.width;
    return zoom === 'fit' ? Math.max(120, frameWidth - 32) : width * Number(zoom);
  };

  const go = (target: number) => {
    const element = frame.current?.querySelector<HTMLElement>(`[data-page="${target}"]`);
    if (!element || !frame.current) return;
    frame.current.scrollTop = element.offsetTop - 16;
    setCurrent(target);
    reportPage.current?.(target);
  };

  // The first page in view is the current one.
  const onScroll = () => {
    const element = frame.current;
    if (!element) return;
    const line = element.scrollTop + element.clientHeight / 3;
    let seen = 1;
    for (const child of element.querySelectorAll<HTMLElement>('[data-page]')) {
      if (child.offsetTop > line) break;
      seen = Number(child.dataset.page);
    }
    if (seen !== current) {
      setCurrent(seen);
      reportPage.current?.(seen);
    }
  };

  // Open at the requested page once the pages have their sizes.
  const opened = useRef(false);
  useLayoutEffect(() => {
    if (opened.current || !frameWidth || pages.length === 0) return;
    opened.current = true;
    if (page && page > 1) go(page);
  });

  // A find from search goes to the first page that matches.
  const found = useRef(false);
  useEffect(() => {
    if (found.current || !initialFind || !text.data || matches.length === 0) return;
    found.current = true;
    go(matches[0]);
  });

  const step = (delta: number) => {
    const index = zooms.findIndex(([value]) => value === zoom);
    const next = zooms[Math.min(zooms.length - 1, Math.max(1, (index < 1 ? 3 : index) + delta))];
    setZoom(next[0]);
  };
  const openFind = () => {
    setFinding(true);
    requestAnimationFrame(() => findInput.current?.select());
  };

  useViewerKeys(
    (event) => {
      if (!list.data) return false;
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'f') return openFind();
      if (event.key === 'Escape' && finding) return setFinding(false);
      if (typing(event) || event.ctrlKey || event.metaKey || event.altKey) return false;
      if (event.key === '+' || event.key === '=') return step(1);
      if (event.key === '-') return step(-1);
      if (event.key === '0') return setZoom('fit');
      if (event.key === 'Home') return go(1);
      if (event.key === 'End') return go(pages.length);
      if (event.key === 'r') return setRotation((rotation + 90) % 360);
      return false;
    },
    { first: true },
  );

  if (list.isPending)
    return (
      <ViewerMessage title="Opening…">
        <Spinner />
      </ViewerMessage>
    );
  if (list.isError) return <ViewerMessage title="The pages of this file could not be shown.">{problemMessage(list.error)} Download it to open it in another application.</ViewerMessage>;

  const total = pages.length;
  return (
    <div className="flex h-full flex-col">
      {notice && <div className="shrink-0 border-b bg-surface px-4 py-2 text-[13px]">{notice}</div>}
      <div ref={frame} onScroll={onScroll} tabIndex={0} aria-label={`Pages of ${file.filename}`} className="relative min-h-0 flex-1 overflow-auto bg-surface-muted px-4 py-4 outline-none">
        <div className="mx-auto flex w-max min-w-full flex-col items-center gap-4">
          {pages.map((size, index) => (
            <PageImage key={index} scroller={frame} file={file} number={index + 1} size={size} width={displayWidth(size)} rotation={rotation} highlighted={finding && matches.includes(index + 1)} />
          ))}
          {list.data.truncated && <p className="text-[13px] text-muted">Only the first {total} pages are shown. Download the file for the rest.</p>}
        </div>
      </div>
      <ViewerBar>
        <Button size="icon" variant="ghost" aria-label="Previous page" disabled={current <= 1} onClick={() => go(current - 1)}>
          <ChevronUp />
        </Button>
        <PageBox current={current} total={total} onGo={go} />
        <Button size="icon" variant="ghost" aria-label="Next page" disabled={current >= total} onClick={() => go(current + 1)}>
          <ChevronDown />
        </Button>
        <span className="mx-1 h-5 w-px bg-border" aria-hidden />
        <Button size="icon" variant="ghost" aria-label="Zoom out (−)" onClick={() => step(-1)}>
          <ZoomOut />
        </Button>
        <Select aria-label="Zoom" className="h-8 w-28" value={zoom} onChange={(event) => setZoom(event.target.value)}>
          {zooms.map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </Select>
        <Button size="icon" variant="ghost" aria-label="Zoom in (+)" onClick={() => step(1)}>
          <ZoomIn />
        </Button>
        <Button size="icon" variant="ghost" aria-label="Rotate the view (R)" onClick={() => setRotation((rotation + 90) % 360)}>
          <RotateCw />
        </Button>
        {!file.detached && <span className="mx-1 h-5 w-px bg-border" aria-hidden />}
        {!file.detached &&
          (finding ? (
            <form role="search" className="flex flex-wrap items-center gap-1" onSubmit={(event) => event.preventDefault()}>
              <Input ref={findInput} aria-label="Find in the file" placeholder="Find" className="h-8 w-40" value={query} onChange={(event) => setQuery(event.target.value)} />
              <span aria-live="polite" className="text-xs text-muted">
                {!query.trim() ? '' : text.isPending ? 'Searching…' : !text.data ? 'No text was read from this file yet' : matches.length === 0 ? 'No matches' : ''}
              </span>
              <PageMatches label="On pages" pages={matches} current={current} onGo={go} />
              <Button size="icon" variant="ghost" aria-label="Close find" onClick={() => setFinding(false)}>
                <X />
              </Button>
            </form>
          ) : (
            <Button size="sm" variant="ghost" onClick={openFind}>
              <Search /> Find
            </Button>
          ))}
      </ViewerBar>
    </div>
  );
}

/**
 * Pages load their image as they come within loadMargin of the view and let
 * it go once they are beyond unloadMargin, so a long scan holds only the
 * images around where the person reads, however far they scroll.
 */
const loadMargin = '600px 0px';
const unloadMargin = '2400px 0px';
/** How long a page's image stays in memory after it scrolls away, for scrolling back. */
const keepImage = 10_000;

/** One page: a box of the page's shape, with its image while it is near the view. */
function PageImage({
  scroller,
  file,
  number,
  size,
  width,
  rotation,
  highlighted,
}: {
  scroller: RefObject<HTMLDivElement | null>;
  file: FileRef;
  number: number;
  size: { width: number; height: number };
  width: number;
  rotation: number;
  highlighted: boolean;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [near, setNear] = useState(false);
  useEffect(() => {
    const element = box.current;
    if (!element) return;
    // Distances count within the scrolling pages, which clip what the window sees.
    const root = scroller.current;
    const load = new IntersectionObserver((entries) => entries.some((entry) => entry.isIntersecting) && setNear(true), { root, rootMargin: loadMargin });
    const unload = new IntersectionObserver((entries) => entries.every((entry) => !entry.isIntersecting) && setNear(false), { root, rootMargin: unloadMargin });
    load.observe(element);
    unload.observe(element);
    return () => {
      load.disconnect();
      unload.disconnect();
    };
  }, [scroller]);
  const turned = rotation % 180 !== 0;
  const ratio = turned ? size.width / size.height : size.height / size.width;
  const height = Math.round(width * ratio);
  // The image is drawn upright at the page's own shape, then turned.
  const drawnWidth = turned ? height : width;
  const drawnHeight = turned ? width : height;
  const pixels = renditionWidth(Math.round(drawnWidth * (window.devicePixelRatio || 1)));
  const path = near ? `/v1/items/${file.itemId}/pages/${number}?width=${pixels}&blob_id=${file.blobId}` : undefined;
  const image = useBlobUrl(path, { keep: keepImage });
  return (
    <div
      ref={box}
      data-page={number}
      aria-label={`Page ${number}`}
      className={`relative shrink-0 overflow-hidden bg-white shadow-sm ${highlighted ? 'ring-2 ring-accent' : ''}`}
      style={{ width, height }}
    >
      {image.url ? (
        <img
          src={image.url}
          alt={`Page ${number} of ${file.filename}`}
          draggable={false}
          className="absolute top-1/2 left-1/2 max-w-none select-none"
          style={{ width: drawnWidth, height: drawnHeight, transform: `translate(-50%, -50%) rotate(${rotation}deg)` }}
        />
      ) : image.error ? (
        <p className="grid h-full place-items-center p-4 text-center text-xs text-muted">Page {number} could not be shown.</p>
      ) : (
        <span className="absolute inset-0 grid place-items-center text-xs text-muted">{near ? <Spinner /> : number}</span>
      )}
    </div>
  );
}

function PageBox({ current, total, onGo }: { current: number; total: number; onGo: (page: number) => void }) {
  const [value, setValue] = useState(String(current));
  const [shown, setShown] = useState(current);
  if (shown !== current) {
    setShown(current);
    setValue(String(current));
  }
  return (
    <form
      className="flex items-center gap-1"
      onSubmit={(event) => {
        event.preventDefault();
        const target = Number(value);
        if (Number.isInteger(target) && target >= 1 && target <= total) onGo(target);
        else setValue(String(current));
      }}
    >
      <Input aria-label="Page" inputMode="numeric" className="h-8 w-14 text-center" value={value} onChange={(event) => setValue(event.target.value)} onBlur={() => setValue(String(current))} />
      <span className="text-muted" aria-live="polite">
        of {total}
      </span>
    </form>
  );
}
