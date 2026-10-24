import { useEffect, useRef, type ReactNode } from 'react';
import { cn } from '@/lib/utils';

/** Keys typed into a field, or pressed with a modifier the viewer does not use, are not viewer shortcuts. */
export function typing(event: KeyboardEvent) {
  const target = event.target as HTMLElement | null;
  return !!target?.closest('input, textarea, select, [contenteditable="true"], [role="menu"], [role="listbox"]');
}

/**
 * Handles viewer shortcuts while mounted. Each handler returns false to let
 * the key through; handled keys are not passed on. File handlers listen first
 * (in the capture phase), so their keys, such as Escape closing find, win over
 * the viewer's own, whichever mounted first.
 */
export function useViewerKeys(handle: (event: KeyboardEvent) => boolean | void, { first = false }: { first?: boolean } = {}) {
  const latest = useRef(handle);
  useEffect(() => {
    latest.current = handle;
  });
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing) return;
      if (latest.current(event) === false) return;
      event.preventDefault();
      event.stopPropagation();
    };
    window.addEventListener('keydown', onKey, first);
    return () => window.removeEventListener('keydown', onKey, first);
  }, [first]);
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
