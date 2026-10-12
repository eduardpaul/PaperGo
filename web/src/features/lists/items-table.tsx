import { Folder } from 'lucide-react';
import { useEffect, useRef } from 'react';
import type { FieldDefinition, Resource } from '@/api/types';
import { Badge } from '@/components/ui/badge';
import { Checkbox } from '@/components/ui/select';
import { FieldDisplay } from '@/features/fields/display';
import { useFormat } from '@/lib/preferences';
import { cn } from '@/lib/utils';
import { columnLabel, publishState } from './schema';

/** Rows of a list or library: a click opens the item (or the folder); the checkbox selects it. */
export function ItemsTable({
  rows,
  columns,
  fields,
  publishing,
  names,
  selectable,
  selected,
  activeId,
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
            {columns.map((column) => (
              <th key={column} className="px-3 py-2 font-medium whitespace-nowrap">
                {columnLabel(column, fields)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((resource) => {
            const state = publishState(resource, publishing);
            return (
              <tr
                key={resource.id}
                onClick={() => onOpen(resource)}
                className={cn(
                  'cursor-pointer border-b last:border-0 hover:bg-surface-muted/50',
                  selected.has(resource.id) && 'bg-accent-soft/40',
                  activeId === resource.id && 'bg-accent-soft/70',
                )}
              >
                {selectable && (
                  <td className="px-3 py-2" onClick={(event) => event.stopPropagation()}>
                    <Checkbox
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
  return <FieldDisplay field={field} value={resource.values?.[column]} names={field.type === 'person' ? names : undefined} />;
}
