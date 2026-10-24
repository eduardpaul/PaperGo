import 'pdfjs-dist/legacy/web/pdf_viewer.css';
import workerUrl from 'pdfjs-dist/legacy/build/pdf.worker.min.mjs?url';
import { ChevronDown, ChevronLeft, ChevronRight, ChevronUp, Images, MessageSquarePlus, RotateCw, Search, X, ZoomIn, ZoomOut } from 'lucide-react';
import type * as PdfLibrary from 'pdfjs-dist/legacy/build/pdf.mjs';
import type * as PdfComponents from 'pdfjs-dist/legacy/web/pdf_viewer.mjs';
import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import type { FileRef, ViewerProps } from '../handlers';
import { PageRegions, threadsOn, useRegionThreads } from '../regions';
import { PageMatches, pagesMatching, useReadText } from '../text-find';
import { typing, useViewerKeys, ViewerBar, ViewerMessage } from '../viewer-kit';
import { useFollowLink } from '../trust';
import { PageImages } from './pages';

declare const __PDFJS_ASSETS__: string;

type Library = typeof PdfLibrary;
type Components = typeof PdfComponents;
type Viewer = InstanceType<Components['PDFViewer']>;
type Bus = InstanceType<Components['EventBus']>;

// The legacy build carries the polyfills that PDF.js needs on browsers a release or two old.
// The viewer components read PDF.js from a global, so it is set before they load.
let loading: Promise<{ lib: Library; components: Components }> | undefined;
function load() {
  loading ??= (async () => {
    const lib = await import('pdfjs-dist/legacy/build/pdf.mjs');
    lib.GlobalWorkerOptions.workerSrc = workerUrl;
    (globalThis as { pdfjsLib?: Library }).pdfjsLib = lib;
    return { lib, components: await import('pdfjs-dist/legacy/web/pdf_viewer.mjs') };
  })();
  return loading;
}

const scales = [
  ['page-width', 'Fit width'],
  ['page-fit', 'Fit page'],
  ['0.5', '50%'],
  ['0.75', '75%'],
  ['1', '100%'],
  ['1.5', '150%'],
  ['2', '200%'],
  ['3', '300%'],
  ['4', '400%'],
] as const;

type State = { status: 'loading' } | { status: 'password'; retry: (password: string) => void; wrong: boolean } | { status: 'failed'; message: string } | { status: 'ready'; pages: number };

/** Above this size a PDF opens as page images on phones and tablets with little memory. */
const lightBytes = 25 * 1024 * 1024;

/** Whether a PDF opens as server-rendered page images, sparing a small device's memory. */
function opensLight(file: FileRef) {
  if (file.detached || file.size < lightBytes) return false;
  const memory = (navigator as { deviceMemory?: number }).deviceMemory;
  return window.matchMedia?.('(pointer: coarse)').matches && (memory === undefined || memory <= 4);
}

/**
 * A PDF: drawn by PDF.js in the browser, or, for very large files on small
 * devices or when PDF.js cannot draw it, as page images the server renders.
 */
export default function PdfView(props: ViewerProps) {
  const [light, setLight] = useState(() => opensLight(props.file));
  if (light) {
    return (
      <PageImages
        {...props}
        notice={
          <span className="flex flex-wrap items-center gap-2">
            <span className="flex-1">Showing page images, which use less memory on this device. Text selection and links need the full viewer.</span>
            <Button size="sm" onClick={() => setLight(false)}>
              Open the full viewer
            </Button>
          </span>
        }
      />
    );
  }
  return <PdfDocument {...props} onLight={props.file.detached ? undefined : () => setLight(true)} />;
}

/** Every page of a PDF, with find, text selection, zoom and links, rendered by PDF.js without scripts. */
function PdfDocument({ file, source, page, onPage, find: initialFind, comment, onComment, onLight }: ViewerProps & { onLight?: () => void }) {
  const container = useRef<HTMLDivElement>(null);
  const pages = useRef<HTMLDivElement>(null);
  const { follow, dialog } = useFollowLink();
  const followLink = useRef(follow);
  useEffect(() => {
    followLink.current = follow;
  });
  const viewer = useRef<Viewer>(null);
  const bus = useRef<Bus>(null);
  const [state, setState] = useState<State>({ status: 'loading' });
  const [current, setCurrent] = useState(page ?? 1);
  const [scale, setScale] = useState<string>('page-width');
  const [percent, setPercent] = useState(100);
  const [finding, setFinding] = useState(!!initialFind);
  const [query, setQuery] = useState(initialFind ?? '');
  // Scanned pages have no text layer; the text PaperGo recognized names the pages that match.
  const recognized = useReadText(file, finding && !!query.trim());
  const scanned = pagesMatching(recognized.data, query, 'ocr');
  const [matches, setMatches] = useState<{ current: number; total: number }>();
  const findInput = useRef<HTMLInputElement>(null);
  // Comment threads placed on regions of this file version, drawn on their pages.
  const [pageLayers, setPageLayers] = useState<{ number: number; page: HTMLElement; layer: HTMLElement }[]>([]);
  const [rotation, setRotation] = useState(0);
  const [drawing, setDrawing] = useState(false);
  const regions = useRegionThreads(file, !!onComment);
  const pins = threadsOn(regions.data?.threads, file.blobId);
  const canComment = !!onComment && !!regions.data?.canPost;
  const initialPage = useRef(page);
  const initialQuery = useRef(initialFind);
  const reportPage = useRef(onPage);
  useEffect(() => {
    reportPage.current = onPage;
  });

  useEffect(() => {
    let cancelled = false;
    // The viewer mounts a handler per file, so a document loads once per mount.
    let task: ReturnType<Library['getDocument']> | undefined;
    void load().then(({ lib, components }) => {
      if (cancelled || !container.current || !pages.current) return;
      const eventBus = new components.EventBus();
      const links = new components.PDFLinkService({ eventBus, externalLinkTarget: components.LinkTarget.BLANK, externalLinkRel: 'noopener noreferrer nofollow' });
      const findController = new components.PDFFindController({ eventBus, linkService: links });
      const pdfViewer = new components.PDFViewer({
        container: container.current,
        viewer: pages.current,
        eventBus,
        linkService: links,
        findController,
        annotationMode: lib.AnnotationMode.ENABLE,
        annotationEditorMode: lib.AnnotationEditorType.DISABLE,
      });
      links.setViewer(pdfViewer);
      viewer.current = pdfViewer;
      bus.current = eventBus;
      eventBus.on('pagesinit', () => {
        setPageLayers(
          [...(pages.current?.querySelectorAll<HTMLElement>('.page[data-page-number]') ?? [])].map((page) => {
            const layer = document.createElement('div');
            layer.className = 'pointer-events-none absolute inset-0 z-[5]';
            return { number: Number(page.dataset.pageNumber), page, layer };
          }),
        );
        pdfViewer.currentScaleValue = 'page-width';
        const first = initialPage.current;
        if (first && first > 1 && first <= pdfViewer.pagesCount) pdfViewer.currentPageNumber = first;
        // Words a search found are highlighted from the page it opened at.
        if (initialQuery.current) eventBus.dispatch('find', { source: null, type: '', query: initialQuery.current, caseSensitive: false, entireWord: false, highlightAll: true, findPrevious: false, matchDiacritics: false });
      });
      eventBus.on('pagechanging', ({ pageNumber }: { pageNumber: number }) => {
        setCurrent(pageNumber);
        reportPage.current?.(pageNumber);
      });
      eventBus.on('rotationchanging', ({ pagesRotation }: { pagesRotation: number }) => setRotation(pagesRotation));
      eventBus.on('scalechanging', ({ scale, presetValue }: { scale: number; presetValue?: string }) => {
        setPercent(Math.round(scale * 100));
        setScale(presetValue ?? String(scale));
      });
      eventBus.on('updatefindmatchescount', ({ matchesCount }: { matchesCount: { current: number; total: number } }) => setMatches(matchesCount));
      eventBus.on('updatefindcontrolstate', ({ matchesCount }: { matchesCount?: { current: number; total: number } }) => matchesCount && setMatches(matchesCount));
      task = lib.getDocument({
        url: source.url,
        httpHeaders: source.headers,
        enableXfa: false,
        disableAutoFetch: true,
        cMapUrl: `/${__PDFJS_ASSETS__}cmaps/`,
        standardFontDataUrl: `/${__PDFJS_ASSETS__}standard_fonts/`,
        iccUrl: `/${__PDFJS_ASSETS__}iccs/`,
        wasmUrl: `/${__PDFJS_ASSETS__}wasm/`,
      });
      task.onPassword = (update: (password: string) => void, reason: number) => {
        if (!cancelled) setState({ status: 'password', retry: update, wrong: reason === lib.PasswordResponses.INCORRECT_PASSWORD });
      };
      task.promise.then(
        (pdf) => {
          if (cancelled) return;
          pdfViewer.setDocument(pdf);
          links.setDocument(pdf);
          setState({ status: 'ready', pages: pdf.numPages });
        },
        (error: unknown) => {
          if (cancelled) return;
          const name = (error as { name?: string })?.name;
          setState({
            status: 'failed',
            message: name === 'InvalidPDFException' ? 'This PDF is damaged or not a PDF.' : name === 'ResponseException' ? 'The file could not be loaded.' : 'This PDF could not be shown.',
          });
        },
      );
    });
    return () => {
      cancelled = true;
      viewer.current?.setDocument(null as never);
      viewer.current = null;
      bus.current = null;
      void task?.destroy();
    };
  }, [source.url, source.headers]);

  // PDF.js empties a page each time it draws it again, so each page's region layer goes back on.
  useEffect(() => {
    const element = pages.current;
    if (!element || pageLayers.length === 0) return;
    const layers = new Set<Node>(pageLayers.map(({ layer }) => layer));
    const attach = () => pageLayers.forEach(({ page, layer }) => layer.parentNode !== page && page.append(layer));
    attach();
    const observer = new MutationObserver((records) => {
      if (records.some((record) => [...record.removedNodes].some((node) => layers.has(node)))) attach();
    });
    observer.observe(element, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, [pageLayers]);

  // Links out of the document follow the organization's trusted hosts, by click, Enter or middle click; links to
  // places in the document keep their own handling.
  useEffect(() => {
    const element = container.current;
    if (!element) return;
    const onClick = (event: MouseEvent) => {
      const link = (event.target as Element | null)?.closest?.('.annotationLayer a[href]');
      const href = link?.getAttribute('href') ?? '';
      if (!link || href.startsWith('#') || link.classList.contains('internalLink')) return;
      event.preventDefault();
      event.stopPropagation();
      if (event.type === 'click' || event.button === 1) followLink.current(href);
    };
    element.addEventListener('click', onClick, true);
    element.addEventListener('auxclick', onClick, true);
    return () => {
      element.removeEventListener('click', onClick, true);
      element.removeEventListener('auxclick', onClick, true);
    };
  }, []);

  // A fitted scale fits again when the canvas changes size, as when the info panel opens.
  useEffect(() => {
    const element = container.current;
    if (!element) return;
    const observer = new ResizeObserver(() => {
      const preset = viewer.current?.currentScaleValue;
      if (viewer.current && (preset === 'page-width' || preset === 'page-fit' || preset === 'auto')) viewer.current.currentScaleValue = preset;
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  // Ctrl or ⌘ with the wheel zooms around the pointer, as in other viewers.
  useEffect(() => {
    const element = container.current;
    if (!element) return;
    const onWheel = (event: WheelEvent) => {
      if (!(event.ctrlKey || event.metaKey) || !viewer.current) return;
      event.preventDefault();
      viewer.current.updateScale({ steps: event.deltaY < 0 ? 1 : -1, origin: [event.clientX, event.clientY] });
    };
    element.addEventListener('wheel', onWheel, { passive: false });
    return () => element.removeEventListener('wheel', onWheel);
  }, []);

  const ready = state.status === 'ready';
  const total = ready ? state.pages : 0;

  // A page asked for from outside, as by a comment's page link, is shown.
  useEffect(() => {
    const pdf = viewer.current;
    if (ready && pdf && page && page !== pdf.currentPageNumber && page <= pdf.pagesCount) pdf.currentPageNumber = page;
  }, [page, ready]);
  // A thread asked for is shown on its page once.
  const shownThread = useRef<string>(undefined);
  const focusPage = pins.find((pin) => pin.thread.id === comment)?.region.page;
  useEffect(() => {
    const pdf = viewer.current;
    if (!ready || !pdf || !comment || !focusPage || shownThread.current === comment) return;
    shownThread.current = comment;
    if (focusPage !== pdf.currentPageNumber) pdf.currentPageNumber = focusPage;
  }, [comment, focusPage, ready]);
  const go = (target: number) => {
    if (viewer.current && target >= 1 && target <= total) viewer.current.currentPageNumber = target;
  };
  const zoom = (steps: number) => viewer.current?.updateScale({ steps });
  const choose = (value: string) => {
    if (viewer.current) viewer.current.currentScaleValue = value;
  };
  const find = (again: boolean, previous = false) =>
    bus.current?.dispatch('find', { source: null, type: again ? 'again' : '', query, caseSensitive: false, entireWord: false, highlightAll: true, findPrevious: previous, matchDiacritics: false });
  const openFind = () => {
    setFinding(true);
    requestAnimationFrame(() => findInput.current?.select());
  };
  const closeFind = () => {
    setFinding(false);
    setMatches(undefined);
    bus.current?.dispatch('findbarclose', { source: null });
  };

  useViewerKeys((event) => {
    if (!ready) return false;
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'f') return openFind();
    if (event.key === 'Escape' && finding) return closeFind();
    if (event.key === 'Escape' && drawing) return setDrawing(false);
    if (typing(event) || event.ctrlKey || event.metaKey || event.altKey) return false;
    if (event.key === '+' || event.key === '=') return void zoom(1);
    if (event.key === '-') return void zoom(-1);
    if (event.key === '0') return choose('page-width');
    if (event.key === 'Home') return go(1);
    if (event.key === 'End') return go(total);
    if (event.key === 'r') return void (viewer.current && (viewer.current.pagesRotation = (viewer.current.pagesRotation + 90) % 360));
    if (event.key === 'c' && canComment) return setDrawing(!drawing);
    return false;
  }, { first: true });

  return (
    <div className="flex h-full flex-col">
      <div className="relative min-h-0 flex-1 bg-surface-muted">
        {/* PDF.js positions pages inside an absolutely positioned scroll container. */}
        <div ref={container} tabIndex={0} aria-label={`Pages of ${file.filename}`} className="absolute inset-0 overflow-auto outline-none">
          <div ref={pages} className="pdfViewer" />
        </div>
        {ready &&
          onComment &&
          pageLayers.map(({ number, layer }) =>
            createPortal(
              <PageRegions
                file={file}
                page={number}
                rotation={rotation}
                pins={pins}
                focus={comment}
                drawing={drawing}
                onOpen={onComment}
                onDone={(threadId) => {
                  setDrawing(false);
                  if (threadId) onComment(threadId);
                }}
              />,
              layer,
              `regions-${number}`,
            ),
          )}
        {drawing && (
          <p role="status" className="absolute top-3 left-1/2 z-10 -translate-x-1/2 rounded-full bg-foreground px-3 py-1 text-xs text-background shadow">
            Drag over the area to comment on, or click a spot. Esc cancels.
          </p>
        )}
        {state.status !== 'ready' && (
          <div className="absolute inset-0 bg-surface-muted">
            {state.status === 'loading' ? (
              <ViewerMessage title="Opening the PDF…">
                <Spinner />
              </ViewerMessage>
            ) : state.status === 'password' ? (
              <PasswordForm wrong={state.wrong} onSubmit={state.retry} />
            ) : (
              <ViewerMessage title={state.message}>
                Download the file to open it in another application.
                {onLight && (
                  <Button onClick={onLight}>
                    <Images /> Show page images
                  </Button>
                )}
              </ViewerMessage>
            )}
          </div>
        )}
      </div>
      {ready && (
        <ViewerBar>
          <Button size="icon" variant="ghost" aria-label="Previous page" disabled={current <= 1} onClick={() => go(current - 1)}>
            <ChevronUp />
          </Button>
          <PageBox current={current} total={total} onGo={go} />
          <Button size="icon" variant="ghost" aria-label="Next page" disabled={current >= total} onClick={() => go(current + 1)}>
            <ChevronDown />
          </Button>
          <span className="mx-1 h-5 w-px bg-border" aria-hidden />
          <Button size="icon" variant="ghost" aria-label="Zoom out (−)" onClick={() => zoom(-1)}>
            <ZoomOut />
          </Button>
          <Select aria-label="Zoom" className="h-8 w-28" value={scales.some(([value]) => value === scale) ? scale : 'custom'} onChange={(event) => choose(event.target.value)}>
            {!scales.some(([value]) => value === scale) && <option value="custom">{percent}%</option>}
            {scales.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </Select>
          <Button size="icon" variant="ghost" aria-label="Zoom in (+)" onClick={() => zoom(1)}>
            <ZoomIn />
          </Button>
          <Button size="icon" variant="ghost" aria-label="Rotate the view (R)" onClick={() => viewer.current && (viewer.current.pagesRotation = (viewer.current.pagesRotation + 90) % 360)}>
            <RotateCw />
          </Button>
          {canComment && (
            <Button size="sm" variant={drawing ? 'secondary' : 'ghost'} aria-pressed={drawing} aria-label="Comment on an area (C)" title="Comment on an area (C)" onClick={() => setDrawing(!drawing)}>
              <MessageSquarePlus /> <span className="hidden md:inline">Comment</span>
            </Button>
          )}
          <span className="mx-1 h-5 w-px bg-border" aria-hidden />
          {finding ? (
            <form
              role="search"
              className="flex items-center gap-1"
              onSubmit={(event) => {
                event.preventDefault();
                find(true);
              }}
            >
              <Input
                ref={findInput}
                aria-label="Find in the PDF"
                placeholder="Find"
                className="h-8 w-40"
                value={query}
                onChange={(event) => {
                  setQuery(event.target.value);
                  bus.current?.dispatch('find', { source: null, type: '', query: event.target.value, caseSensitive: false, entireWord: false, highlightAll: true, findPrevious: false, matchDiacritics: false });
                }}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && event.shiftKey) {
                    event.preventDefault();
                    find(true, true);
                  }
                }}
              />
              <span aria-live="polite" className="min-w-16 text-xs text-muted">
                {query && matches ? (matches.total ? `${matches.current} of ${matches.total}` : scanned.length ? '' : 'No matches') : ''}
              </span>
              <PageMatches label="Scanned pages" pages={scanned} current={current} onGo={go} />
              <Button size="icon" variant="ghost" aria-label="Previous match" disabled={!matches?.total} onClick={() => find(true, true)}>
                <ChevronLeft />
              </Button>
              <Button size="icon" variant="ghost" aria-label="Next match" disabled={!matches?.total} onClick={() => find(true)}>
                <ChevronRight />
              </Button>
              <Button size="icon" variant="ghost" aria-label="Close find" onClick={closeFind}>
                <X />
              </Button>
            </form>
          ) : (
            <Button size="sm" variant="ghost" onClick={openFind}>
              <Search /> Find
            </Button>
          )}
        </ViewerBar>
      )}
      {dialog}
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

function PasswordForm({ wrong, onSubmit }: { wrong: boolean; onSubmit: (password: string) => void }) {
  const [password, setPassword] = useState('');
  return (
    <ViewerMessage title="This PDF is protected with a password">
      <form
        className="flex w-64 flex-col gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          onSubmit(password);
        }}
      >
        <Input type="password" aria-label="PDF password" autoFocus value={password} onChange={(event) => setPassword(event.target.value)} />
        {wrong && <p className="text-xs text-danger">That password is not correct.</p>}
        <Button type="submit" variant="primary" disabled={!password}>
          Open
        </Button>
        <p className="text-xs">The password is used in this browser only.</p>
      </form>
    </ViewerMessage>
  );
}
