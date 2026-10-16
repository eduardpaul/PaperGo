import { ChevronRight, Folder, FolderOpen } from 'lucide-react';
import { useApiPages } from '@/api/pages';
import type { Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/feedback';
import { QueryFeedback } from '@/features/admin/common';

/** A shared destination picker; folder identifiers stay out of the editing flow. */
export function FolderBrowser({ root, path, onChange, excluded = [] }: { root: Resource; path: Resource[]; onChange: (path: Resource[]) => void; excluded?: string[] }) {
  const parent = path.at(-1)?.id ?? root.id;
  const folders = useApiPages<Resource>(['resources', parent, 'folders'], `/v1/resources/${parent}/children`, { query: { kind: 'folder', $top: 100 } });
  return <div className="grid gap-2">
    <nav aria-label="Destination folder" className="flex flex-wrap items-center gap-1 text-sm">
      <button type="button" className="text-accent hover:underline" onClick={() => onChange([])}>{root.name}</button>
      {path.map((folder, index) => <span key={folder.id} className="flex items-center gap-1"><ChevronRight className="size-3.5 text-muted" /><button type="button" className="text-accent hover:underline" onClick={() => onChange(path.slice(0, index + 1))}>{folder.name}</button></span>)}
    </nav>
    <div className="max-h-60 overflow-auto rounded-md border">
      {folders.isPending ? <Skeleton className="m-2 h-8" /> : folders.isError ? <QueryFeedback query={folders} /> : folders.items.length ? <ul>{folders.items.filter(folder => !excluded.includes(folder.id)).map(folder => <li key={folder.id}><button type="button" className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-surface-muted" onClick={() => onChange([...path, folder])}><Folder className="size-4 text-muted" /><span className="flex-1 truncate">{folder.name}</span><ChevronRight className="size-4 text-muted" /></button></li>)}</ul> : <p className="flex items-center gap-2 px-3 py-2 text-sm text-muted"><FolderOpen className="size-4" />No folders here</p>}
      {folders.hasNextPage && <Button className="m-2" size="sm" disabled={folders.isFetchingNextPage} onClick={() => void folders.fetchNextPage()}>{folders.isFetchingNextPage ? 'Loading…' : 'More folders'}</Button>}
    </div>
  </div>;
}
