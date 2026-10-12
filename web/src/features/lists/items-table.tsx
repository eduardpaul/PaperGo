import { ArrowDown, ArrowUp, ChevronsUpDown, Folder } from 'lucide-react';
import { useEffect, useRef, useState, type KeyboardEvent } from 'react';
import type { FieldDefinition, Resource } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { Checkbox } from '@/components/ui/select';
import { FieldDisplay } from '@/features/fields/display';
import { useFormat } from '@/lib/preferences';
import { cn } from '@/lib/utils';
import type { ListingSort } from '@/api/requests';
import { columnLabel, publishState } from './schema';

/**
 * Rows of a list or library: a click opens the item (or the folder); the checkbox selects it. From the keyboard, the
 * arrow keys, Home and End move between rows, Enter opens one and x (or Space) selects it.
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
}: {
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
}) {
  const format = useFormat();
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
  const focusRow = (index: number) => {
    const next = Math.max(0, Math.min(rows.length - 1, index));
    setFocusIndex(next);
    rowRefs.current[next]?.focus();
  };
  const onRowKey = (event: KeyboardEvent<HTMLTableRowElement>, index: number, resource: Resource) => {
    if (event.target !== event.currentTarget) return;
    const keys: Record<string, () => void> = {
      ArrowDown: () => focusRow(index + 1),
      ArrowUp: () => focusRow(index - 1),
      Home: () => focusRow(0),
      End: () => focusRow(rows.length - 1),
      Enter: () => onOpen(resource),
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
      <table className="w-full text-left text-[13px]">
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
        <tbody>
          {rows.map((resource, index) => {
            const state = publishState(resource, publishing);
            return (
              <tr
                key={resource.id}
                ref={(element) => {
                  rowRefs.current[index] = element;
                }}
                tabIndex={index === current ? 0 : -1}
                onFocus={() => setFocusIndex(index)}
                onKeyDown={(event) => onRowKey(event, index, resource)}
                onClick={() => onOpen(resource)}
                className={cn(
                  'cursor-pointer border-b outline-none last:border-0 hover:bg-surface-muted/50',
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
                      <button
                        type="button"
                        tabIndex={-1}
                        className="inline-flex max-w-full items-center gap-2 text-left font-medium hover:underline"
                        onClick={(event) => {
                          event.stopPropagation();
                          onOpen(resource);
                        }}
                      >
                        {resource.kind === 'folder' && <Folder className="size-4 shrink-0 text-muted" />}
                        <span className="truncate">{resource.name}</span>
                        {state && <Badge tone={state === 'Published' ? 'success' : 'warning'}>{state}</Badge>}
                      </button>
                    ) : (
                      <Cell column={column} resource={resource} fields={fields} names={names} formatDate={format.dateTime} />
                    )}
                  </td>
                ))}
              </tr>
            );
          })}
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
  if (column === '$tags') return <span className="text-muted">{(resource.tags ?? []).join(', ') || '—'}</span>;
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
