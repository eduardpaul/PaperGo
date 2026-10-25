import { ArrowDown, ArrowUp, ChevronsUpDown, Folder, FolderOpen } from 'lucide-react';
import { useWindowVirtualizer } from '@tanstack/react-virtual';
import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactElement, type ReactNode } from 'react';
import type { FieldDefinition, Resource } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { Checkbox } from '@/components/ui/select';
import { FieldDisplay } from '@/features/fields/display';
import { downloadContent } from '@/features/documents/transfer';
import { FileAction, useClickOrOpenFile, useOpenFile } from '@/features/files/file-action';
import { FileTypeIcon } from '@/features/files/file-types';
import { useFormat } from '@/lib/preferences';
import { cn } from '@/lib/utils';
import type { ListingSort } from '@/api/requests';
import { columnLabel, publishState } from './schema';

/** Longer tables render only the rows near the screen, so loading more stays fast. */
const virtualizeAfter = 200;
const estimatedRowHeight = 41;

/**
 * Rows of a list or library: a click opens the item (or the folder); the checkbox selects it. From the keyboard, the
 * arrow keys, Home and End move between rows, Enter opens one and x (or Space) selects it. An item's file opens with
 * its row's Open button, a double-click or o, and downloads with d.
 */
export function ItemsTable({
  rows,
  columns,
  fields,
  publishing,
  names,
  selectable,
  selected,
  activeId,
  sort,
  sortable,
  onSort,
  onToggle,
  onToggleAll,
  onOpen,
  locations,
  renderRow,
  provenance,
}: {
  provenance?: Map<string, string>;
  renderRow?: (item: Resource, row: ReactElement) => ReactNode;
  rows: Resource[];
  columns: string[];
  fields: FieldDefinition[];
  publishing: boolean;
  names: Map<string, string>;
  /** People who cannot change the list get no checkboxes: there is nothing to do with a selection. */
  selectable: boolean;
  selected: Map<string, Resource>;
  activeId?: string;
  sort?: ListingSort;
  /** Columns the server can sort by: the name, dates, people and filterable single-value columns. */
  sortable: Set<string>;
  /** Called with the next sort: ascending, then descending, then none. */
  onSort: (sort: ListingSort | undefined) => void;
  onToggle: (resource: Resource, checked: boolean) => void;
  onToggleAll: (checked: boolean) => void;
  onOpen: (resource: Resource) => void;
  /** Names of the folders rows sit in, shown under their names (search results). */
  locations?: Map<string, string>;
}) {
  const format = useFormat();
  const openFile = useOpenFile();
  const clicks = useClickOrOpenFile(onOpen);
  const all = rows.length > 0 && rows.every((row) => selected.has(row.id));
  const some = !all && rows.some((row) => selected.has(row.id));
  const allRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (allRef.current) allRef.current.indeterminate = some;
  }, [some]);
  // One row is in the tab order at a time (roving tabindex); the others are reached with the arrow keys.
  const [focusIndex, setFocusIndex] = useState(0);
  const current = Math.min(focusIndex, Math.max(rows.length - 1, 0));
  const rowRefs = useRef<(HTMLTableRowElement | null)[]>([]);
  const virtualize = rows.length > virtualizeAfter;
  // The page scrolls, not the table: rows are placed relative to where the table body starts.
  const body = useRef<HTMLTableSectionElement>(null);
  const [scrollMargin, setScrollMargin] = useState(0);
  useLayoutEffect(() => {
    if (!virtualize) return;
    // Content above the table (a selection bar, a search note) moves it, which also resizes the page.
    const measure = () => {
      const top = (body.current?.getBoundingClientRect().top ?? 0) + window.scrollY;
      setScrollMargin((current) => (Math.abs(top - current) > 1 ? top : current));
    };
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(document.body);
    return () => observer.disconnect();
  }, [virtualize]);
  const virtualizer = useWindowVirtualizer({
    count: rows.length,
    estimateSize: () => estimatedRowHeight,
    overscan: 15,
    scrollMargin,
    enabled: virtualize,
  });
  const visible = virtualize
    ? virtualizer.getVirtualItems().map((item) => item.index)
    : rows.map((_row, index) => index);
  const items = virtualizer.getVirtualItems();
  const padTop = virtualize && items.length ? items[0].start - scrollMargin : 0;
  const padBottom = virtualize && items.length ? virtualizer.getTotalSize() - (items[items.length - 1].end - scrollMargin) : 0;
  // A row reached from the keyboard may not be rendered yet: scroll to it, then focus it once it is.
  const pendingFocus = useRef<number | null>(null);
  useEffect(() => {
    if (pendingFocus.current === null) return;
    const row = rowRefs.current[pendingFocus.current];
    if (row) {
      row.focus({ preventScroll: true });
      pendingFocus.current = null;
    }
  });
  const focusRow = (index: number) => {
    const next = Math.max(0, Math.min(rows.length - 1, index));
    setFocusIndex(next);
    const row = rowRefs.current[next];
    if (row) row.focus();
    else {
      pendingFocus.current = next;
      virtualizer.scrollToIndex(next, { align: 'auto' });
    }
  };
  const onRowKey = (event: KeyboardEvent<HTMLTableRowElement>, index: number, resource: Resource) => {
    if (event.target !== event.currentTarget) return;
    const keys: Record<string, () => void> = {
      ArrowDown: () => focusRow(index + 1),
      ArrowUp: () => focusRow(index - 1),
      Home: () => focusRow(0),
      End: () => focusRow(rows.length - 1),
      Enter: () => onOpen(resource),
      ...(resource.file ? { o: () => openFile(resource), d: () => void downloadContent(resource.id, resource.file?.filename ?? resource.name) } : {}),
      ...(selectable
        ? { x: () => onToggle(resource, !selected.has(resource.id)), ' ': () => onToggle(resource, !selected.has(resource.id)) }
        : {}),
    };
    const action = keys[event.key];
    if (!action || event.altKey || event.ctrlKey || event.metaKey) return;
    event.preventDefault();
    action();
  };
  return (
    <div className="overflow-x-auto rounded-lg border bg-surface">
      <table className="w-full text-left text-[13px]" aria-rowcount={virtualize ? rows.length + 1 : undefined}>
        <thead className="border-b text-xs text-muted">
          <tr>
            {selectable && (
              <th className="w-10 px-3 py-2">
                <Checkbox
                  ref={allRef}
                  aria-label="Select all"
                  checked={all}
                  onChange={(event) => onToggleAll(event.target.checked)}
                />
              </th>
            )}
            {columns.map((column) => {
              const active = sort?.field === column ? sort.direction : undefined;
              const label = columnLabel(column, fields);
              return (
                <th
                  key={column}
                  aria-sort={active === 'asc' ? 'ascending' : active === 'desc' ? 'descending' : undefined}
                  className="px-3 py-2 font-medium whitespace-nowrap"
                >
                  {sortable.has(column) ? (
                    <button
                      type="button"
                      className={cn(
                        'group -mx-1 inline-flex items-center gap-1 rounded px-1 hover:text-foreground',
                        active && 'text-foreground',
                      )}
                      title={`Sort by ${label}`}
                      onClick={() =>
                        onSort(
                          active === undefined
                            ? { field: column, direction: 'asc' }
                            : active === 'asc'
                              ? { field: column, direction: 'desc' }
                              : undefined,
                        )
                      }
                    >
                      {label}
                      {active === 'asc' ? (
                        <ArrowUp className="size-3.5" />
                      ) : active === 'desc' ? (
                        <ArrowDown className="size-3.5" />
                      ) : (
                        <ChevronsUpDown className="size-3.5 opacity-0 group-hover:opacity-60" />
                      )}
                    </button>
                  ) : (
                    <span title="To sort by this column, make it filterable in the list's column settings.">{label}</span>
                  )}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody ref={body}>
          {padTop > 0 && (
            <tr aria-hidden style={{ height: padTop }}>
              <td colSpan={columns.length + (selectable ? 1 : 0)} />
            </tr>
          )}
          {visible.map((index) => {
            const resource = rows[index];
            const state = publishState(resource, publishing);
            const row = (
              <tr
                key={resource.id}
                data-index={index}
                data-item={resource.id}
                aria-rowindex={virtualize ? index + 2 : undefined}
                ref={(element) => {
                  rowRefs.current[index] = element;
                  if (virtualize && element) virtualizer.measureElement(element);
                }}
                tabIndex={index === current ? 0 : -1}
                onFocus={() => setFocusIndex(index)}
                onKeyDown={(event) => onRowKey(event, index, resource)}
                onClick={(event) => clicks.click(resource, event)}
                onDoubleClick={() => clicks.doubleClick(resource)}
                className={cn(
                  'group cursor-pointer border-b outline-none last:border-0 hover:bg-surface-muted/50',
                  'focus-visible:bg-surface-muted focus-visible:shadow-[inset_3px_0_0_var(--color-accent)]',
                  selected.has(resource.id) && 'bg-accent-soft/40',
                  activeId === resource.id && 'bg-accent-soft/70',
                )}
              >
                {selectable && (
                  <td className="px-3 py-2" onClick={(event) => event.stopPropagation()}>
                    <Checkbox
                      tabIndex={-1}
                      checked={selected.has(resource.id)}
                      onChange={(event) => onToggle(resource, event.target.checked)}
                      aria-label={`Select ${resource.name}`}
                    />
                  </td>
                )}
                {columns.map((column) => (
                  <td key={column} className="max-w-xs px-3 py-2 align-top">
                    {column === '$name' ? (
                      <div className="flex items-start gap-1">
                        <div className="min-w-0 flex-1">
                        <button
                          type="button"
                          tabIndex={-1}
                          className="inline-flex max-w-full items-center gap-2 text-left font-medium hover:underline"
                          onClick={(event) => {
                            event.stopPropagation();
                            clicks.click(resource, event);
                          }}
                        >
                          {resource.kind === 'folder' && <Folder className="size-4 shrink-0 text-muted" />}
                          {resource.file && <FileTypeIcon media={resource.file.content_type} />}
                          <span className="truncate">{resource.name}</span>
                          {resource.archived && <Badge>Archived</Badge>}
                          {provenance?.get(resource.id) && <Badge>{provenance.get(resource.id)}</Badge>}
                          {state && <Badge tone={state === 'Published' ? 'success' : 'warning'}>{state}</Badge>}
                        </button>
                        {resource.parent_id && locations?.get(resource.parent_id) && (
                          <span className="flex items-center gap-1 text-xs text-muted">
                            <FolderOpen className="size-3" /> in {locations.get(resource.parent_id)}
                          </span>
                        )}
                        </div>
                        {resource.file && (
                          <FileAction
                            item={resource}
                            className={cn(
                              '-my-1.5 size-7 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100',
                              activeId === resource.id && 'opacity-100',
                            )}
                          />
                        )}
                      </div>
                    ) : (
                      <Cell column={column} resource={resource} fields={fields} names={names} formatDate={format.dateTime} />
                    )}
                  </td>
                ))}
              </tr>
            );
            return renderRow ? renderRow(resource, row) : row;
          })}
          {padBottom > 0 && (
            <tr aria-hidden style={{ height: padBottom }}>
              <td colSpan={columns.length + (selectable ? 1 : 0)} />
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

function Cell({
  column,
  resource,
  fields,
  names,
  formatDate,
}: {
  column: string;
  resource: Resource;
  fields: FieldDefinition[];
  names: Map<string, string>;
  formatDate: (value: string) => string;
}) {
  if (column === '$keywords') return <span className="text-muted">{(resource.keywords ?? []).map(id => names.get(id) ?? id).join(', ') || '—'}</span>;
  if (column === '$archived_at') return <>{resource.archived_at ? formatDate(resource.archived_at) : '—'}</>;
  if (column === '$archived_by') return <>{resource.archived_by ? names.get(resource.archived_by) ?? resource.archived_by : '—'}</>;
  if (column === '$modified_at' || column === '$created_at') {
    const value = column === '$modified_at' ? resource.updated_at : resource.created_at;
    return <span className="whitespace-nowrap text-muted">{value ? formatDate(value) : '—'}</span>;
  }
  if (column === '$modified_by' || column === '$created_by') {
    const subject = column === '$modified_by' ? resource.updated_by : resource.created_by;
    return <span className="text-muted">{subject ? (names.get(subject) ?? subject) : '—'}</span>;
  }
  if (column === '$id') return <span className="font-mono text-xs text-muted">{resource.id}</span>;
  const field = fields.find((entry) => entry.key === column);
  if (!field) return <span className="text-muted">—</span>;
  return <FieldDisplay field={field} value={resource.values?.[column]} names={names} />;
}
