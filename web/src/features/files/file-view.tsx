import { Suspense, useRef } from 'react';
import { Spinner } from '@/components/ui/feedback';
import { cn } from '@/lib/utils';
import { handlerFor, type FileRef, type FileSource } from './handlers';
import { HandlerBoundary, KeyScope, ViewerMessage } from './viewer-kit';

/**
 * A file drawn by its handler in a box on a page, as in a review: the same
 * views as the viewer, read-only, taking keys only while focus is inside.
 */
export function FileView({ file, source, onDownload, className }: { file: FileRef; source: FileSource; onDownload: () => void; className?: string }) {
  const box = useRef<HTMLDivElement>(null);
  const handler = handlerFor(file);
  return (
    <div ref={box} className={cn('relative overflow-hidden rounded-md border bg-surface', className)}>
      {handler ? (
        <KeyScope.Provider value={box}>
          <HandlerBoundary onDownload={onDownload}>
            <Suspense
              fallback={
                <ViewerMessage title="Opening…">
                  <Spinner />
                </ViewerMessage>
              }
            >
              <handler.View file={file} source={source} />
            </Suspense>
          </HandlerBoundary>
        </KeyScope.Provider>
      ) : (
        <ViewerMessage title={`${file.filename} can't be shown here.`}>Download it to open it in another application.</ViewerMessage>
      )}
    </div>
  );
}
