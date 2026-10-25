// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, renderHook, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import type * as Auth from '@/api/auth';
import type { CommentThread, FileSummary } from '@/api/types';
import { editableFile, FileAction, FileLinksProvider, useClickOrOpenFile, viewable } from './file-action';
import { fileFamily, fileTypeLabel } from './file-types';
import { fileRef, fileSource, handlerFor } from './handlers';
import { detectDelimiter, maxCells, maxColumns, parseCsv } from './csv';
import { officeApp, officeUri } from './office';
import { fileRegion, threadsOn, turn, unturn } from './regions';
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

it('parses CSV with quotes, line breaks in cells and the delimiter in use', () => {
  expect(parseCsv('﻿name,amount\r\n"ACME, Inc.",12\n"He said ""hi""\nthere",3\n')).toEqual({
    rows: [['name', 'amount'], ['ACME, Inc.', '12'], ['He said "hi"\nthere', '3']],
    delimiter: ',',
    truncated: false,
    wide: false,
  });
  expect(detectDelimiter('a;b;c\n1;2;3\n')).toBe(';');
  expect(detectDelimiter('a\tb\n"x,y"\tz\n')).toBe('\t');
  expect(parseCsv('a,b\n1,2', ',').rows).toEqual([['a', 'b'], ['1', '2']]);
  expect(parseCsv('1\n2\n3\n4\n', ',', 2)).toEqual({ rows: [['1'], ['2']], delimiter: ',', truncated: true, wide: false });
});

it('bounds the columns and cells of a wide CSV file', () => {
  // A line of commas is one row of many empty cells; each row keeps its first columns.
  const wide = parseCsv(','.repeat(100_000), ',');
  expect(wide.rows).toHaveLength(1);
  expect(wide.rows[0]).toHaveLength(maxColumns);
  expect(wide.wide).toBe(true);
  // Many wide rows stop at the cell bound.
  const line = Array.from({ length: maxColumns + 5 }, (_, n) => n).join(',') + '\n';
  const many = parseCsv(line.repeat(Math.ceil(maxCells / maxColumns) + 10), ',');
  expect(many.rows.length * maxColumns).toBeLessThanOrEqual(maxCells);
  expect(many.truncated).toBe(true);
  expect(many.rows[0].at(-1)).toBe(String(maxColumns - 1));
  expect(handlerFor({ contentType: 'text/csv', filename: 'a.csv' })?.id).toBe('csv');
  expect(handlerFor({ contentType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', filename: 'a.docx' })?.id).toBe('docx');
});

it('opens Office files in their desktop app over WebDAV', () => {
  expect(officeApp('application/vnd.openxmlformats-officedocument.spreadsheetml.sheet')?.name).toBe('Excel');
  expect(officeApp('application/pdf')).toBeUndefined();
  expect(officeUri(officeApp('application/msword')!, 'https://papergo.example/webdav/lib/Contracts/Lease%202026.docx')).toBe('ms-word:ofe|u|https://papergo.example/webdav/lib/Contracts/Lease%202026.docx');
});

it('places regions on turned pages and reads them back upright', () => {
  const rect = { x: 0.1, y: 0.2, w: 0.3, h: 0.1 };
  const close = (a: { x: number; y: number; w: number; h: number }, b: typeof a) => expect([a.x, a.y, a.w, a.h].map((n) => n.toFixed(6))).toEqual([b.x, b.y, b.w, b.h].map((n) => n.toFixed(6)));
  close(turn(rect, 0), rect);
  close(turn(rect, 90), { x: 0.7, y: 0.1, w: 0.1, h: 0.3 });
  close(turn(rect, 180), { x: 0.6, y: 0.7, w: 0.3, h: 0.1 });
  close(turn(rect, 270), { x: 0.2, y: 0.6, w: 0.1, h: 0.3 });
  for (const rotation of [0, 90, 180, 270]) close(unturn(turn(rect, rotation), rotation), rect);
  const region = { kind: 'file.region', blob_id: 'b1', page: 2, ...rect };
  expect(fileRegion(region)).toEqual(region);
  expect(fileRegion({ ...region, page: 0 })).toBeUndefined();
  expect(fileRegion({ ...region, x: 2 })).toBeUndefined();
  expect(fileRegion({ kind: 'text.range' })).toBeUndefined();
  const thread = (id: string, anchor?: object) => ({ id, anchor }) as unknown as CommentThread;
  const pins = threadsOn([thread('a', region), thread('b'), thread('c', { ...region, blob_id: 'b0' }), thread('d', { ...region, page: 3 })], 'b1');
  expect(pins.map((pin) => [pin.thread.id, pin.number, pin.region.page])).toEqual([
    ['a', 1, 2],
    ['d', 2, 3],
  ]);
});
