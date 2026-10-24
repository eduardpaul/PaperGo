import { useQuery } from '@tanstack/react-query';
import { Maximize, RotateCw, ZoomIn, ZoomOut } from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/feedback';
import type { ViewerProps } from '../handlers';
import { typing, useViewerKeys, ViewerBar, ViewerMessage } from '../viewer-kit';

const minZoom = 0.05;
const maxZoom = 8;
const clamp = (value: number) => Math.min(maxZoom, Math.max(minZoom, value));

/** An image at full resolution: fit by default, then zoom, pan and rotate. */
export default function ImageView({ file, source, onStep }: ViewerProps) {
  const image = useQuery({
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
  const url = useMemo(() => (image.data ? URL.createObjectURL(image.data) : undefined), [image.data]);
  useEffect(() => () => { if (url) URL.revokeObjectURL(url); }, [url]);
  const frame = useRef<HTMLDivElement>(null);
  const [natural, setNatural] = useState<{ width: number; height: number }>();
  const [box, setBox] = useState({ width: 0, height: 0 });
  // Undefined zoom fits the image to the frame.
  const [zoom, setZoom] = useState<number>();
  const [rotation, setRotation] = useState(0);
  const [broken, setBroken] = useState(false);

  useLayoutEffect(() => {
    const element = frame.current;
    if (!element) return;
    const observer = new ResizeObserver(() => setBox({ width: element.clientWidth, height: element.clientHeight }));
    observer.observe(element);
    return () => observer.disconnect();
  }, [url]);

  const turned = rotation % 180 !== 0;
  const width = natural ? (turned ? natural.height : natural.width) : 0;
  const height = natural ? (turned ? natural.width : natural.height) : 0;
  const fit = natural && box.width && box.height ? Math.min(1, (box.width - 32) / width, (box.height - 32) / height) : 1;
  const scale = zoom ?? fit;

  const zoomTo = (next: number, origin?: { x: number; y: number }) => {
    const element = frame.current;
    const target = clamp(next);
    if (!element) return setZoom(target);
    // Keep the point under the pointer (or the centre) where it is.
    const point = origin ?? { x: element.clientWidth / 2, y: element.clientHeight / 2 };
    const ratio = target / scale;
    const x = (element.scrollLeft + point.x) * ratio - point.x;
    const y = (element.scrollTop + point.y) * ratio - point.y;
    setZoom(target);
    requestAnimationFrame(() => {
      element.scrollLeft = x;
      element.scrollTop = y;
    });
  };
  const fitOrActual = () => (zoom === undefined && fit < 1 ? zoomTo(1) : setZoom(undefined));

  useEffect(() => {
    const element = frame.current;
    if (!element) return;
    const onWheel = (event: WheelEvent) => {
      if (!(event.ctrlKey || event.metaKey)) return;
      event.preventDefault();
      const bounds = element.getBoundingClientRect();
      zoomTo(scale * (event.deltaY < 0 ? 1.15 : 1 / 1.15), { x: event.clientX - bounds.left, y: event.clientY - bounds.top });
    };
    element.addEventListener('wheel', onWheel, { passive: false });
    return () => element.removeEventListener('wheel', onWheel);
  });

  // Dragging pans a zoomed image; two fingers pinch; a horizontal swipe at fit steps to the next item.
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const gesture = useRef<{ startX: number; startY: number; left: number; top: number; distance?: number; zoom: number }>(null);
  const onPointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    const element = frame.current;
    if (!element || event.button !== 0) return;
    pointers.current.set(event.pointerId, { x: event.clientX, y: event.clientY });
    element.setPointerCapture(event.pointerId);
    const points = [...pointers.current.values()];
    gesture.current = {
      startX: event.clientX,
      startY: event.clientY,
      left: element.scrollLeft,
      top: element.scrollTop,
      distance: points.length === 2 ? Math.hypot(points[0].x - points[1].x, points[0].y - points[1].y) : undefined,
      zoom: scale,
    };
  };
  const onPointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    const element = frame.current;
    const start = gesture.current;
    if (!element || !start || !pointers.current.has(event.pointerId)) return;
    pointers.current.set(event.pointerId, { x: event.clientX, y: event.clientY });
    const points = [...pointers.current.values()];
    if (points.length === 2 && start.distance) {
      setZoom(clamp(start.zoom * (Math.hypot(points[0].x - points[1].x, points[0].y - points[1].y) / start.distance)));
      return;
    }
    element.scrollLeft = start.left - (event.clientX - start.startX);
    element.scrollTop = start.top - (event.clientY - start.startY);
  };
  const onPointerUp = (event: React.PointerEvent<HTMLDivElement>) => {
    const start = gesture.current;
    pointers.current.delete(event.pointerId);
    if (pointers.current.size) return;
    gesture.current = null;
    if (!start || start.distance || zoom !== undefined || event.pointerType !== 'touch' || !onStep) return;
    const dx = event.clientX - start.startX;
    if (Math.abs(dx) > 60 && Math.abs(dx) > 2 * Math.abs(event.clientY - start.startY)) onStep(dx < 0 ? 1 : -1);
  };

  useViewerKeys((event) => {
    if (!natural || typing(event) || event.ctrlKey || event.metaKey || event.altKey) return false;
    if (event.key === '+' || event.key === '=') return zoomTo(scale * 1.25);
    if (event.key === '-') return zoomTo(scale / 1.25);
    if (event.key === '0') return setZoom(undefined);
    if (event.key === '1') return fitOrActual();
    if (event.key === 'r') return setRotation((value) => (value + 90) % 360);
    return false;
  }, { first: true });

  if (image.isError || broken) return <ViewerMessage title="This image could not be shown.">Download the file to open it in another application.</ViewerMessage>;
  return (
    <div className="flex h-full flex-col">
      <div
        ref={frame}
        tabIndex={0}
        aria-label={`Image ${file.filename}`}
        className="min-h-0 flex-1 touch-none overflow-auto bg-surface-muted outline-none select-none"
        style={{ cursor: zoom !== undefined && (width * scale > box.width || height * scale > box.height) ? 'grab' : 'zoom-in' }}
        onDoubleClick={fitOrActual}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
      >
        {url ? (
          <div className="flex min-h-full min-w-full items-center justify-center p-4" style={{ width: natural ? width * scale + 32 : undefined, height: natural ? height * scale + 32 : undefined }}>
            <div className="flex shrink-0 items-center justify-center" style={{ width: width * scale, height: height * scale }}>
              <img
                src={url}
                alt={file.filename}
                draggable={false}
                className="max-w-none shrink-0 shadow-sm"
                style={natural ? { width: natural.width * scale, height: natural.height * scale, transform: `rotate(${rotation}deg)` } : { visibility: 'hidden' }}
                onLoad={(event) => setNatural({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })}
                onError={() => setBroken(true)}
              />
            </div>
          </div>
        ) : (
          <ViewerMessage title="Opening the image…">
            <Spinner />
          </ViewerMessage>
        )}
      </div>
      <ViewerBar>
        <Button size="icon" variant="ghost" aria-label="Zoom out (−)" disabled={!natural} onClick={() => zoomTo(scale / 1.25)}>
          <ZoomOut />
        </Button>
        <span className="w-14 text-center tabular-nums" aria-live="polite">
          {Math.round(scale * 100)}%
        </span>
        <Button size="icon" variant="ghost" aria-label="Zoom in (+)" disabled={!natural} onClick={() => zoomTo(scale * 1.25)}>
          <ZoomIn />
        </Button>
        <Button size="sm" variant="ghost" disabled={!natural} onClick={fitOrActual}>
          <Maximize /> {zoom === undefined && fit < 1 ? 'Actual size' : 'Fit'}
        </Button>
        <Button size="icon" variant="ghost" aria-label="Rotate the view (R)" disabled={!natural} onClick={() => setRotation((value) => (value + 90) % 360)}>
          <RotateCw />
        </Button>
        {natural && (
          <span className="ml-2 text-xs text-muted">
            {natural.width} × {natural.height} px
          </span>
        )}
      </ViewerBar>
    </div>
  );
}
