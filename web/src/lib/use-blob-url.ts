import { useQuery } from '@tanstack/react-query';
import { useEffect, useMemo } from 'react';
import { fetchBlob } from '@/api/files';

/**
 * An object URL of an authenticated file, revoked when it is no longer
 * shown. keep is how long the bytes stay in memory once nothing shows them.
 */
export function useBlobUrl(path: string | undefined, { keep }: { keep?: number } = {}) {
  const blob = useQuery({
    queryKey: ['blob', path],
    queryFn: () => fetchBlob(path!),
    enabled: !!path,
    staleTime: Infinity,
    gcTime: keep,
  });
  const url = useMemo(() => (blob.data ? URL.createObjectURL(blob.data) : undefined), [blob.data]);
  useEffect(
    () => () => {
      if (url) URL.revokeObjectURL(url);
    },
    [url],
  );
  return { url, error: blob.error, loading: blob.isLoading };
}
