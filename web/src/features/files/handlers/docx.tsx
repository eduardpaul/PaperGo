import { useQuery } from '@tanstack/react-query';
import { Maximize, ZoomIn, ZoomOut } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/feedback';
import type { ViewerProps } from '../handlers';
import { useFollowLink } from '../trust';
import { typing, useViewerKeys, ViewerBar, ViewerMessage } from '../viewer-kit';

/**
 * The frame's own document: no scripts can run in it, only the app draws
 * into it, and it loads nothing from anywhere. A document's styles can name
 * outside URLs, so its policy allows only the images and fonts docx-preview
 * inlines from the file itself.
 */
const framePolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src data: blob:; font-src data: blob:";
const frameDocument = `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${framePolicy}"><style>
html,body{margin:0;background:#e5e7eb}
body{padding:16px 0}
.docx-wrapper{background:transparent!important;padding:0!important;display:flex;flex-direction:column;align-items:center;gap:16px}
.docx-wrapper>section.docx{box-shadow:0 1px 3px rgb(0 0 0/.15);margin:0!important}
a[href]{cursor:pointer;color:#1d4ed8;text-decoration:underline}
</style></head><body><div id="styles"></div><main id="pages"></main></body></html>`;

const minZoom = 0.25;
const maxZoom = 3;

/**
 * A Word document, laid out in the browser by docx-preview inside a
 * sandboxed frame that runs no scripts: pages, fonts, tables, headers and
 * footers close to Word's, though fields, tracked changes and floating
 * objects may differ. Links follow the organization's trusted hosts.
 */
export default function DocxView({ file, source }: ViewerProps) {
  const bytes = useQuery({
    queryKey: ['file-bytes', source.url, file.blobId],
    queryFn: async ({ signal }) => {
      const response = await fetch(source.url, { headers: source.headers, credentials: 'same-origin', signal });
      if (!response.ok) throw new Error(String(response.status));
      return response.blob();
    },
    staleTime: Infinity,
    gcTime: 60_000,
    retry: false,
  });
  const frame = useRef<HTMLIFrameElement>(null);
  const [ready, setReady] = useState(false);
  const [rendered, setRendered] = useState<'pending' | 'done' | 'failed'>('pending');
  const [zoom, setZoom] = useState<number>();
  const [fit, setFit] = useState(1);
  const { follow, dialog } = useFollowLink();
  const followLink = useRef(follow);
  useEffect(() => {
    followLink.current = follow;
  });

  useEffect(() => {
    const doc = frame.current?.contentDocument;
    // docx-preview empties the containers it is given, so the frame's own styles stay apart from them.
    const pages = doc?.getElementById('pages');
    const styles = doc?.getElementById('styles');
    if (!ready || !bytes.data || !doc || !pages || !styles) return;
    let live = true;
    void import('docx-preview')
      .then(({ renderAsync }) =>
        renderAsync(bytes.data, pages, styles, {
          className: 'docx',
          inWrapper: true,
          breakPages: true,
          ignoreLastRenderedPageBreak: true,
          useBase64URL: true,
          renderHeaders: true,
          renderFooters: true,
          renderFootnotes: true,
          renderEndnotes: true,
          renderComments: false,
          renderChanges: false,
          // Embedded HTML chunks stay out, whatever the frame allows.
          renderAltChunks: false,
          experimental: false,
        }),
      )
      .then(
        () => live && setRendered('done'),
        () => live && setRendered('failed'),
      );
    // Links open through the app: in-document ones scroll, others follow trusted hosts.
    const onClick = (event: MouseEvent) => {
      const link = (event.target as Element | null)?.closest?.('a[href]');
      if (!link) return;
      event.preventDefault();
      const href = link.getAttribute('href') ?? '';
      if (href.startsWith('#')) {
        const name = CSS.escape(href.slice(1));
        doc.querySelector(`[id="${name}"], [name="${name}"]`)?.scrollIntoView({ block: 'start' });
        return;
      }
      followLink.current(href);
    };
    doc.addEventListener('click', onClick);
    return () => {
      live = false;
      doc.removeEventListener('click', onClick);
      pages.replaceChildren();
      styles.replaceChildren();
    };
  }, [ready, bytes.data]);

  // Pages fit the frame's width unless zoomed; Word pages are about 816 pixels wide.
  useEffect(() => {
    const element = frame.current;
    if (!element || rendered !== 'done') return;
    const measure = () => {
      const page = element.contentDocument?.querySelector<HTMLElement>('section.docx');
      const width = page?.offsetWidth ?? 816;
      setFit(Math.min(1, (element.clientWidth - 32) / width));
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [rendered]);
  const scale = zoom ?? fit;
  useEffect(() => {
    const body = frame.current?.contentDocument?.body;
    body?.style.setProperty('zoom', String(scale));
  }, [scale, rendered]);

  const step = (factor: number) => setZoom(Math.min(maxZoom, Math.max(minZoom, scale * factor)));
  useViewerKeys(
    (event) => {
      if (rendered !== 'done' || typing(event) || event.ctrlKey || event.metaKey || event.altKey) return false;
      if (event.key === '+' || event.key === '=') return step(1.2);
      if (event.key === '-') return step(1 / 1.2);
      if (event.key === '0') return setZoom(undefined);
      return false;
    },
    { first: true },
  );

  if (bytes.isError) return <ViewerMessage title="The file could not be loaded.">Download it to open it in Word.</ViewerMessage>;
  return (
    <div className="flex h-full flex-col">
      <div className="relative min-h-0 flex-1">
        <iframe
          ref={frame}
          title={`Pages of ${file.filename}`}
          // Same origin lets the app draw into the frame; without allow-scripts nothing in the document runs.
          sandbox="allow-same-origin"
          srcDoc={frameDocument}
          onLoad={() => setReady(true)}
          className="absolute inset-0 h-full w-full border-0"
        />
        {rendered !== 'done' && (
          <div className="absolute inset-0 bg-surface-muted">
            {rendered === 'failed' ? (
              <ViewerMessage title="This Word document could not be shown.">It may be damaged, protected or in an older format. Download it to open it in Word.</ViewerMessage>
            ) : (
              <ViewerMessage title="Opening the document…">
                <Spinner />
              </ViewerMessage>
            )}
          </div>
        )}
      </div>
      {rendered === 'done' && (
        <ViewerBar>
          <Button size="icon" variant="ghost" aria-label="Zoom out (−)" onClick={() => step(1 / 1.2)}>
            <ZoomOut />
          </Button>
          <span className="w-12 text-center text-muted" aria-live="polite">
            {Math.round(scale * 100)}%
          </span>
          <Button size="icon" variant="ghost" aria-label="Zoom in (+)" onClick={() => step(1.2)}>
            <ZoomIn />
          </Button>
          <Button size="sm" variant="ghost" aria-pressed={zoom === undefined} onClick={() => setZoom(undefined)}>
            <Maximize /> Fit width
          </Button>
          <span className="mx-1 h-5 w-px bg-border" aria-hidden />
          <span className="text-xs text-muted">Shown in a simplified layout; Word may differ.</span>
        </ViewerBar>
      )}
      {dialog}
    </div>
  );
}
