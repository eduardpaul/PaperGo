import { useMutation, useQueryClient } from '@tanstack/react-query';
import {
  addMonths,
  eachDayOfInterval,
  endOfMonth,
  endOfWeek,
  format as dateKey,
  parseISO,
  startOfMonth,
  startOfWeek,
} from 'date-fns';
import { File, Folder, MoreHorizontal } from 'lucide-react';
import { useState } from 'react';
import { api } from '@/api/http';
import { keys } from '@/api/queries';
import type { FieldDefinition, Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/menu';
import { FieldDisplay } from '@/features/fields/display';
import { useFormat } from '@/lib/preferences';

type LayoutProps = { items: Resource[]; fields: FieldDefinition[]; onOpen: (item: Resource) => void };

function CardFields({ item, fields }: { item: Resource; fields: FieldDefinition[] }) {
  return (
    <div className="grid gap-1 text-xs text-muted">
      {fields.slice(0, 3).map((field) => (
        <div key={field.id}>
          <span>{field.label}: </span>
          <FieldDisplay field={field} value={item.values?.[field.key]} />
        </div>
      ))}
    </div>
  );
}

export function ItemsGallery({ items, fields, onOpen }: LayoutProps) {
  return (
    <ul className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
      {items.map((item) => (
        <li key={item.id}>
          <button
            type="button"
            className="grid w-full gap-2 rounded-lg border bg-surface p-3 text-left shadow-xs hover:border-accent"
            onClick={() => onOpen(item)}
          >
            <span className="flex items-center gap-2 font-medium">
              {item.kind === 'folder' ? <Folder className="size-4" /> : <File className="size-4" />}
              {item.name}
            </span>
            <CardFields item={item} fields={fields} />
          </button>
        </li>
      ))}
    </ul>
  );
}

/** Board cards move by drag/drop or the keyboard-accessible Move to menu. */
export function ItemsBoard({
  items,
  fields,
  groupBy,
  readOnly,
  onOpen,
}: LayoutProps & { groupBy?: FieldDefinition; readOnly?: boolean }) {
  const client = useQueryClient();
  const [over, setOver] = useState<string>();
  const move = useMutation({
    mutationFn: ({ item, value }: { item: Resource; value: string }) =>
      api(`/v1/resources/${item.id}`, {
        method: 'PATCH',
        ifMatch: item.version,
        body: { values: { [groupBy!.key]: value || null } },
      }),
    onSettled: () => client.invalidateQueries({ queryKey: keys.resources }),
  });
  if (!groupBy || groupBy.type !== 'choice' || groupBy.options?.multiple)
    return <Alert>This board needs a single-choice grouping field. Choose one in Manage views.</Alert>;
  const valueOf = (item: Resource) => String(item.values?.[groupBy.key] ?? '');
  const columns = [...new Set([...(groupBy.choices ?? []), ...items.map(valueOf), ...(groupBy.required ? [] : [''])])];
  const moveTo = (item: Resource, value: string) => {
    if (!readOnly && item.kind === 'item' && valueOf(item) !== value) move.mutate({ item, value });
  };
  return (
    <div className="flex gap-3 overflow-auto pb-2">
      {columns.map((column) => {
        const cards = items.filter((item) => valueOf(item) === column);
        return (
          <section
            key={column}
            aria-label={column || 'No value'}
            className={`w-72 shrink-0 rounded-lg border bg-surface-muted ${over === column ? 'border-accent' : ''}`}
            onDragOver={(event) => {
              event.preventDefault();
              setOver(column);
            }}
            onDragLeave={() => setOver(undefined)}
            onDrop={(event) => {
              event.preventDefault();
              setOver(undefined);
              const item = items.find((item) => item.id === event.dataTransfer.getData('text/plain'));
              if (item && !move.isPending) moveTo(item, column);
            }}
          >
            <h3 className="p-3 text-sm font-semibold">
              {column || 'No value'} <span className="text-muted">{cards.length}</span>
            </h3>
            <ul className="grid min-h-16 gap-2 p-2">
              {cards.map((item) => (
                <li
                  key={item.id}
                  draggable={!readOnly && item.kind === 'item' && !move.isPending}
                  onDragStart={(event) => event.dataTransfer.setData('text/plain', item.id)}
                  className="grid gap-2 rounded border bg-surface p-3"
                >
                  <div className="flex items-start gap-2">
                    <button type="button" className="flex-1 text-left font-medium" onClick={() => onOpen(item)}>
                      {item.name}
                    </button>
                    {!readOnly && item.kind === 'item' && (
                      <DropdownMenu>
                        <DropdownMenuTrigger aria-label={`Move ${item.name} to`} disabled={move.isPending}>
                          <MoreHorizontal className="size-4" />
                        </DropdownMenuTrigger>
                        <DropdownMenuContent>
                          <DropdownMenuLabel>Move to</DropdownMenuLabel>
                          {columns
                            .filter((value) => value !== column && (value === '' || groupBy.choices?.includes(value)))
                            .map((value) => (
                              <DropdownMenuItem key={value} onSelect={() => moveTo(item, value)}>
                                {value || 'No value'}
                              </DropdownMenuItem>
                            ))}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    )}
                  </div>
                  <CardFields item={item} fields={fields.filter((field) => field.key !== groupBy.key)} />
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}

export function calendarDay(
  item: Resource,
  field: FieldDefinition,
  dayKey: (value: string) => string,
): string | undefined {
  const value =
    field.key === '$created_at'
      ? item.created_at
      : field.key === '$modified_at'
        ? item.updated_at
        : item.values?.[field.key];
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}/.test(value)) return undefined;
  return field.type === 'date' ? value.slice(0, 10) : dayKey(value);
}

export function ItemsCalendar({
  items,
  dateField,
  onOpen,
}: Omit<LayoutProps, 'fields'> & { dateField?: FieldDefinition }) {
  const format = useFormat();
  const today = format.dayKey(new Date());
  const [cursor, setCursor] = useState(today.slice(0, 7));
  const anchor = parseISO(`${cursor}-01`);
  const days = eachDayOfInterval({
    start: startOfWeek(startOfMonth(anchor), { weekStartsOn: 1 }),
    end: endOfWeek(endOfMonth(anchor), { weekStartsOn: 1 }),
  }).map((date) => dateKey(date, 'yyyy-MM-dd'));
  if (!dateField || !['date', 'datetime'].includes(dateField.type))
    return <Alert>This calendar needs a date grouping field. Choose one in Manage views.</Alert>;
  const byDay = new Map<string, Resource[]>();
  const undated: Resource[] = [];
  for (const item of items) {
    const day = calendarDay(item, dateField, format.dayKey);
    if (day) byDay.set(day, [...(byDay.get(day) ?? []), item]);
    else undated.push(item);
  }
  return (
    <div className="grid gap-3">
      <div className="flex items-center gap-3">
        <Button aria-label="Previous month" onClick={() => setCursor(dateKey(addMonths(anchor, -1), 'yyyy-MM'))}>
          ‹
        </Button>
        <span className="font-semibold">{cursor}</span>
        <Button aria-label="Next month" onClick={() => setCursor(dateKey(addMonths(anchor, 1), 'yyyy-MM'))}>
          ›
        </Button>
        <Button onClick={() => setCursor(today.slice(0, 7))}>Today</Button>
      </div>
      <div className="overflow-auto rounded border bg-surface">
        <div className="grid min-w-[600px] grid-cols-7">
          {days.slice(0, 7).map((day) => (
            <div key={day} className="border-b p-2 text-xs text-muted">
              {format.weekday(day, 'short')}
            </div>
          ))}
          {days.map((day) => (
            <div
              key={day}
              className={`min-h-24 border-r border-b p-2 ${day.slice(0, 7) !== cursor ? 'bg-surface-muted' : ''}`}
            >
              <span className={`text-xs ${day === today ? 'font-bold text-accent' : ''}`}>{Number(day.slice(8))}</span>
              <ul className="mt-1 grid gap-1">
                {byDay.get(day)?.map((item) => (
                  <li key={item.id}>
                    <button
                      type="button"
                      className="w-full rounded px-1 text-left text-xs hover:bg-accent-soft"
                      onClick={() => onOpen(item)}
                    >
                      {item.name}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </div>
      {undated.length > 0 && (
        <div>
          <h3 className="text-sm font-medium">No {dateField.label}</h3>
          <ul>
            {undated.map((item) => (
              <li key={item.id}>
                <button type="button" className="text-sm hover:underline" onClick={() => onOpen(item)}>
                  {item.name}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
