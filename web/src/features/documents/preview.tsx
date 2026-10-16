import { useQuery } from '@tanstack/react-query';
import { useEffect, useMemo } from 'react';
import { authorizationHeaders } from '@/api/auth';
import { Skeleton } from '@/components/ui/feedback';
import { cn } from '@/lib/utils';

type Rendition = { status: 'ready'; blob: Blob } | { status: 'pending' | 'unavailable' };
export async function fetchPreview(path: string, signal?: AbortSignal): Promise<Rendition> {
  const response = await fetch(path, { headers: authorizationHeaders(), credentials: 'same-origin', signal });
  if (!response.ok) return { status: 'unavailable' };
  if (response.headers.get('Content-Type')?.split(';')[0] === 'image/webp') return { status: 'ready', blob: await response.blob() };
  const state = await response.json() as { status?: string };
  return { status: state.status === 'pending' ? 'pending' : 'unavailable' };
}

/** A rendition request never falls back to original document bytes. */
export function FilePreview({ itemId, version, blobId, alt, className }: { itemId: string; version?: number; blobId?: string; alt: string; className?: string }) {
  const preview = useQuery({
    queryKey: ['resources', itemId, 'preview', version, blobId],
    queryFn: ({ signal }) => fetchPreview(`/v1/items/${itemId}/preview${blobId ? `?blob_id=${encodeURIComponent(blobId)}` : ''}`, signal),
    retry: false,
    refetchInterval: query => query.state.data?.status === 'pending' ? 2000 : false,
    staleTime: 30_000,
  });
  const data = preview.data;
  const image = data?.status === 'ready' ? data.blob : undefined;
  const url = useMemo(() => image ? URL.createObjectURL(image) : undefined, [image]);
  useEffect(() => () => { if (url) URL.revokeObjectURL(url); }, [url]);
  return <span className={cn('grid min-h-28 place-items-center overflow-hidden rounded-md bg-surface-muted', className)}>
    {url ? <img src={url} alt={alt} loading="lazy" className="max-h-[32rem] max-w-full object-contain" /> : preview.isPending || data?.status === 'pending' ? <span role="status" className="grid w-full gap-2 p-3 text-xs text-muted"><Skeleton className="h-20" />Generating preview…</span> : <span className="p-3 text-xs text-muted">Preview unavailable</span>}
  </span>;
}
