import { useQuery } from '@tanstack/react-query';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Search } from 'lucide-react';
import { useMemo, useRef, useState } from 'react';
import { Spinner } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { Checkbox } from '@/components/ui/select';
import { useFormat } from '@/lib/preferences';
import { parseCsv } from '../csv';
import type { ViewerProps } from '../handlers';
import { useViewerKeys, ViewerBar, ViewerMessage } from '../viewer-kit';

/** Above this size a CSV file is not laid out as a table in the browser. */
const maxBytes = 20 * 1024 * 1024;
const rowHeight = 32;
const delimiters: Record<string, string> = { ',': 'commas', ';': 'semicolons', '\t': 'tabs' };

/**
 * A CSV file as a table: its first row as column names (or not), every row
 * numbered, and a filter that keeps the rows holding some text. Long files
 * render only the rows in view.
 */
export default function CsvView({ file, source, find }: ViewerProps) {
  const format = useFormat();
  const table = useQuery({
    queryKey: ['file-table', source.url, file.blobId],
    enabled: file.size <= maxBytes,
    staleTime: Infinity,
    gcTime: 60_000,
    retry: false,
    queryFn: async ({ signal }) => {
      const response = await fetch(source.url, { headers: source.headers, credentials: 'same-origin', signal });
      if (!response.ok) throw new Error(String(response.status));
      return parseCsv(new TextDecoder('utf-8').decode(await response.arrayBuffer()));
    },
  });
  const [header, setHeader] = useState(true);
  const [filter, setFilter] = useState(find ?? '');
  const filterInput = useRef<HTMLInputElement>(null);
  const scroller = useRef<HTMLDivElement>(null);

  const rows = useMemo(() => table.data?.rows ?? [], [table.data]);
  const columns = rows.reduce((most, row) => Math.max(most, row.length), 0);
  const names = header && rows.length ? rows[0] : [];
  const body = useMemo(() => {
    const numbered = rows.map((cells, index) => ({ cells, number: index + 1 })).slice(header ? 1 : 0);
    const words = filter.trim().toLowerCase();
    return words ? numbered.filter((row) => row.cells.some((cell) => cell.toLowerCase().includes(words))) : numbered;
  }, [rows, header, filter]);

  // eslint-disable-next-line react-hooks/incompatible-library -- the compiler leaves this table unmemoized, which the virtualizer needs
  const virtual = useVirtualizer({ count: body.length, getScrollElement: () => scroller.current, estimateSize: () => rowHeight, overscan: 20 });

  useViewerKeys(
    (event) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'f') return void filterInput.current?.select();
      return false;
    },
    { first: true },
  );

  if (file.size > maxBytes) return <ViewerMessage title="This CSV file is too large to show as a table here.">Download it to open it in a spreadsheet application.</ViewerMessage>;
  if (table.isPending)
    return (
      <ViewerMessage title="Opening…">
        <Spinner />
      </ViewerMessage>
    );
  if (table.isError) return <ViewerMessage title="The file could not be loaded.">Download it to open it in a spreadsheet application.</ViewerMessage>;

  const template = `4.5rem repeat(${Math.max(columns, 1)}, minmax(8rem, 20rem))`;
  return (
    <div className="flex h-full flex-col">
      <div ref={scroller} role="table" aria-label={file.filename} aria-rowcount={body.length + (header ? 1 : 0)} className="min-h-0 flex-1 overflow-auto bg-surface text-[13px]">
        <div className="w-max min-w-full">
          {header && (
            <div role="row" className="sticky top-0 z-10 grid border-b bg-surface-muted font-medium" style={{ gridTemplateColumns: template }}>
              <span role="columnheader" className="sticky left-0 bg-surface-muted px-2 py-1.5 text-right text-xs text-muted">
                #
              </span>
              {Array.from({ length: columns }, (_, index) => (
                <span key={index} role="columnheader" className="truncate border-l px-2 py-1.5" title={names[index]}>
                  {names[index] ?? ''}
                </span>
              ))}
            </div>
          )}
          <div className="relative" style={{ height: virtual.getTotalSize() }}>
            {virtual.getVirtualItems().map((item) => {
              const row = body[item.index];
              return (
                <div key={item.key} role="row" className="absolute inset-x-0 grid border-b odd:bg-surface even:bg-surface-muted/40" style={{ gridTemplateColumns: template, height: rowHeight, transform: `translateY(${item.start}px)` }}>
                  <span role="rowheader" className="sticky left-0 bg-inherit px-2 py-1.5 text-right font-mono text-xs text-muted">
                    {row.number}
                  </span>
                  {Array.from({ length: columns }, (_, index) => (
                    <span key={index} role="cell" className="truncate border-l px-2 py-1.5" title={row.cells[index]}>
                      {row.cells[index] ?? ''}
                    </span>
                  ))}
                </div>
              );
            })}
          </div>
          {body.length === 0 && <p className="px-4 py-6 text-center text-muted">{filter ? 'No row holds that text.' : 'The file has no rows.'}</p>}
        </div>
      </div>
      <ViewerBar>
        <span className="text-muted" aria-live="polite">
          {format.number(body.length)} {body.length === 1 ? 'row' : 'rows'}
          {filter.trim() ? ' match' : ''} · {format.number(columns)} {columns === 1 ? 'column' : 'columns'} · separated by {delimiters[table.data.delimiter]}
          {table.data.truncated && ` · the first ${format.number(rows.length)} rows`}
        </span>
        <span className="mx-1 h-5 w-px bg-border" aria-hidden />
        <label className="flex items-center gap-1.5">
          <Checkbox checked={header} onChange={(event) => setHeader(event.target.checked)} /> First row names the columns
        </label>
        <span className="mx-1 h-5 w-px bg-border" aria-hidden />
        <span className="flex items-center gap-1">
          <Search className="size-4 text-muted" aria-hidden />
          <Input ref={filterInput} aria-label="Filter rows" placeholder="Filter rows" className="h-8 w-44" value={filter} onChange={(event) => setFilter(event.target.value)} />
        </span>
      </ViewerBar>
    </div>
  );
}
