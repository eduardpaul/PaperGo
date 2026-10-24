// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, renderHook, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import type * as Auth from '@/api/auth';
import type { FileSummary } from '@/api/types';
import { editableFile, FileAction, FileLinksProvider, useClickOrOpenFile, viewable } from './file-action';
import { fileFamily, fileTypeLabel } from './file-types';
import { fileRef, fileSource, handlerFor } from './handlers';
import { comparable } from './text-compare';
import { pagesMatching } from './text-find';

const navigate = vi.hoisted(() => vi.fn());
const download = vi.hoisted(() => vi.fn());
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, search, onClick, ...props }: { children: ReactNode; search: Record<string, unknown>; onClick?: (event: React.MouseEvent) => void } & Record<string, unknown>) => (
    <a href={`?${new URLSearchParams(Object.entries(search).filter(([, value]) => value !== undefined) as [string, string][])}`} aria-label={props['aria-label'] as string} onClick={(event) => { onClick?.(event); event.preventDefault(); }}>
      {children}
    </a>
  ),
  useNavigate: () => navigate,
}));
vi.mock('@/features/documents/transfer', () => ({ downloadContent: download }));
vi.mock('@/api/auth', async (original) => ({
  ...(await original<typeof Auth>()),
  fileEditMax: () => 100,
  fileTypes: () => [
    { media_type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', label: 'Word document', family: 'document', extensions: ['.docx'] },
    { media_type: 'text/csv', label: 'CSV table', family: 'spreadsheet', extensions: ['.csv'] },
    { media_type: 'application/vnd.ms-powerpoint', label: 'PowerPoint presentation', family: 'presentation', extensions: ['.ppt'] },
  ],
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.useRealTimers();
});

const file = (content_type: string, filename = 'a'): FileSummary => ({ blob_id: 'b1', content_type, filename, size: 10, version: 2 });
const item = (summary?: FileSummary) => ({ id: 'i1', workspace_id: 'w', container_id: 'l', name: 'Item', file: summary });

it('picks a handler by media type and leaves other files to download', () => {
  expect(handlerFor({ contentType: 'application/pdf', filename: 'a.pdf' })?.id).toBe('pdf');
  expect(handlerFor({ contentType: 'image/tiff', filename: 'a.tif' })?.id).toBe('pages');
  expect(handlerFor({ contentType: 'application/zip', filename: 'a.zip' })).toBeUndefined();
  expect(viewable(item(file('image/png')))).toBe(true);
  expect(viewable(item(file('application/zip')))).toBe(false);
  expect(viewable(item())).toBe(false);
});

it('edits Markdown and plain text within the size limit, and nothing else', () => {
  expect(handlerFor({ contentType: 'text/markdown', filename: 'a.md' })?.id).toBe('markdown');
  expect(handlerFor({ contentType: 'text/plain', filename: 'a.txt' })?.id).toBe('text');
  expect(editableFile(item(file('text/markdown')))).toBe(true);
  expect(editableFile(item(file('text/plain')))).toBe(true);
  expect(editableFile(item({ ...file('text/markdown'), size: 101 }))).toBe(false);
  expect(editableFile(item(file('application/pdf')))).toBe(false);
  expect(editableFile(item())).toBe(false);
});

it('reads the item file without a blob and an earlier version with its blob', () => {
  expect(fileSource(fileRef('i1', file('application/pdf'))!).url).toBe('/v1/items/i1/content');
  expect(fileSource(fileRef('i1', file('application/pdf'), true)!).url).toBe('/v1/items/i1/content?blob_id=b1');
});

it('names file types and groups them into families', () => {
  expect(fileTypeLabel('application/vnd.openxmlformats-officedocument.wordprocessingml.document')).toBe('Word document');
  expect(fileTypeLabel('application/x-thing')).toBe('THING file');
  expect(fileFamily('text/csv')).toBe('spreadsheet');
  expect(fileFamily('application/vnd.ms-powerpoint')).toBe('presentation');
  expect(fileFamily(undefined)).toBe('file');
});

it('opens viewable files as links that keep the page, and downloads the rest', () => {
  const row = vi.fn();
  render(
    <FileLinksProvider value={(target, options) => ({ to: '/w/$workspaceId/l/$listId', search: { item: target.id, open: 'file', page: options?.page } })}>
      <div onClick={row}>
        <FileAction item={item(file('application/pdf', 'contract.pdf'))} options={{ page: 3 }} />
        <FileAction item={{ ...item(file('application/zip', 'export.zip')), id: 'i2' }} />
        <FileAction item={{ ...item(), id: 'i3' }} />
      </div>
    </FileLinksProvider>,
  );
  const open = screen.getByRole('link', { name: 'Open contract.pdf at page 3' });
  expect(open.getAttribute('href')).toBe('?item=i1&open=file&page=3');
  fireEvent.click(open);
  fireEvent.click(screen.getByRole('button', { name: 'Download export.zip' }));
  expect(download).toHaveBeenCalledWith('i2', 'export.zip');
  expect(row).not.toHaveBeenCalled();
  expect(screen.getAllByRole('link').length + screen.getAllByRole('button').length).toBe(2);
});

it('waits for a double-click on items with a file and opens others at once', () => {
  vi.useFakeTimers();
  const details = vi.fn();
  const { result } = renderHook(() => useClickOrOpenFile(details));
  const withFile = item(file('application/pdf'));
  const click = (detail: number) => ({ detail }) as React.MouseEvent;
  act(() => result.current.click(item(), click(1)));
  expect(details).toHaveBeenCalledTimes(1);
  act(() => {
    result.current.click(withFile, click(1));
    result.current.click(withFile, click(2));
    result.current.doubleClick(withFile);
    vi.advanceTimersByTime(500);
  });
  expect(details).toHaveBeenCalledTimes(1);
  expect(navigate).toHaveBeenCalledOnce();
  act(() => {
    result.current.click(withFile, click(1));
    vi.advanceTimersByTime(500);
  });
  expect(details).toHaveBeenCalledTimes(2);
});

it('finds pages by every word of a query, ignoring case and accents, in all pages or scanned ones', () => {
  const text = {
    pages: [
      { page: 1, text: 'Invoice for March', ocr: false },
      { page: 2, text: 'Payment received – Café Müller', ocr: true },
      { page: 3, text: 'march payment summary', ocr: true },
    ],
  };
  expect(pagesMatching(text, 'march')).toEqual([1, 3]);
  expect(pagesMatching(text, 'march', 'ocr')).toEqual([3]);
  expect(pagesMatching(text, 'cafe MULLER')).toEqual([2]);
  expect(pagesMatching(text, 'payment summary')).toEqual([3]);
  expect(pagesMatching(text, '  ')).toEqual([]);
  expect(pagesMatching(null, 'march')).toEqual([]);
});

it('compares only text versions', () => {
  expect(comparable('text/markdown')).toBe(true);
  expect(comparable('text/plain')).toBe(true);
  expect(comparable('application/pdf')).toBe(false);
});
