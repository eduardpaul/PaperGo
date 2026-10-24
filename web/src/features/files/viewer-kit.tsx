import { Download } from 'lucide-react';
import { Component, createContext, useContext, useEffect, useRef, type ReactNode, type RefObject } from 'react';
import { Button } from '@/components/ui/button';
import { typingIn } from '@/lib/keys';
import { cn } from '@/lib/utils';

/** Keys typed into a field, or pressed with a modifier the viewer does not use, are not viewer shortcuts. */
export function typing(event: KeyboardEvent) {
  return typingIn(event.target) || (event.target instanceof Element && !!event.target.closest('[role="menu"], [role="listbox"]'));
}

/**
 * Handles viewer shortcuts while mounted. Each handler returns false to let
 * the key through; handled keys are not passed on. File handlers listen first
 * (in the capture phase), so their keys, such as Escape closing find, win over
 * the viewer's own, whichever mounted first.
 */
export function useViewerKeys(handle: (event: KeyboardEvent) => boolean | void, { first = false }: { first?: boolean } = {}) {
  const latest = useRef(handle);
  const scope = useContext(KeyScope);
  useEffect(() => {
    latest.current = handle;
  });
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing) return;
      if (scope && !(event.target instanceof Node && scope.current?.contains(event.target))) return;
      if (latest.current(event) === false) return;
      event.preventDefault();
      event.stopPropagation();
    };
    window.addEventListener('keydown', onKey, first);
    return () => window.removeEventListener('keydown', onKey, first);
  }, [first, scope]);
}

/**
 * Where a handler shown outside the viewer, as in a review, takes its keys:
 * only keys pressed inside that element, so the page keeps its own.
 */
export const KeyScope = createContext<RefObject<HTMLElement | null> | null>(null);

/** Shows a download in place of a handler that failed, rather than a broken canvas. */
export class HandlerBoundary extends Component<{ children: ReactNode; onDownload: () => void }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    if (!this.state.failed) return this.props.children;
    return (
      <ViewerMessage title="This file could not be shown.">
        <span>Reload the page to try again, or download the file.</span>
        <Button variant="primary" onClick={this.props.onDownload}>
          <Download /> Download
        </Button>
      </ViewerMessage>
    );
  }
}

/** The bar of page, zoom and find controls under a handler's canvas. */
export function ViewerBar({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div role="toolbar" aria-label="File controls" className={cn('flex shrink-0 flex-wrap items-center justify-center gap-1 border-t bg-surface px-2 py-1.5 text-[13px]', className)}>
      {children}
    </div>
  );
}

/** A state that fills the canvas: loading, a failure, or a question such as a password. */
export function ViewerMessage({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="grid h-full place-items-center p-6">
      <div className="flex max-w-sm flex-col items-center gap-3 text-center">
        <p className="text-sm font-medium">{title}</p>
        {children && <div className="flex flex-col items-center gap-3 text-[13px] text-muted">{children}</div>}
      </div>
    </div>
  );
}
