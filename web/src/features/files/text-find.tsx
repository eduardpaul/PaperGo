import { useQuery } from '@tanstack/react-query';
import { api, isApiError } from '@/api/http';
import { Button } from '@/components/ui/button';
import type { FileRef } from './handlers';

interface ReadText {
  pages: { page: number; text: string; ocr?: boolean }[];
}

/** The text PaperGo read from a file, by page; null before it was read or for files read nowhere. */
export function useReadText(file: FileRef, enabled: boolean) {
  return useQuery({
    queryKey: ['file-read-text', file.itemId, file.blobId],
    enabled: enabled && !file.detached,
    staleTime: 5 * 60_000,
    retry: false,
    queryFn: async () => {
      try {
        return (await api<ReadText>(`/v1/items/${file.itemId}/text`, { query: { blob_id: file.blobId } })).data;
      } catch (error) {
        if (isApiError(error) && (error.status === 404 || error.status === 410 || error.status === 403)) return null;
        throw error;
      }
    },
  });
}

/** Pages whose text holds every word of query, ignoring case and accents. */
export function pagesMatching(text: ReadText | null | undefined, query: string, which: 'all' | 'ocr' = 'all'): number[] {
  const words = fold(query).split(/\s+/).filter(Boolean);
  if (!text || words.length === 0) return [];
  return text.pages.filter((page) => (which === 'all' || page.ocr) && words.every((word) => fold(page.text).includes(word))).map((page) => page.page);
}

function fold(value: string) {
  return value.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase();
}

/**
 * Pages where recognized text matches: scanned pages have no text to
 * highlight, so find names them and goes to them instead.
 */
export function PageMatches({ label, pages, current, onGo }: { label: string; pages: number[]; current?: number; onGo: (page: number) => void }) {
  if (pages.length === 0) return null;
  const shown = pages.slice(0, 12);
  return (
    <span className="flex flex-wrap items-center gap-1 text-xs text-muted" aria-live="polite">
      {label}
      {shown.map((page) => (
        <Button key={page} size="sm" variant={page === current ? 'secondary' : 'ghost'} className="h-6 px-1.5" aria-label={`Go to page ${page}`} onClick={() => onGo(page)}>
          {page}
        </Button>
      ))}
      {pages.length > shown.length && <span>and {pages.length - shown.length} more</span>}
    </span>
  );
}
