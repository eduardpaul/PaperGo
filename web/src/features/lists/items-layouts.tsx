import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
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
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { useApiPages } from '@/api/pages';
import { keys } from '@/api/queries';
import { viewGroupsRequest, viewRowsRequest } from '@/api/requests';
import type { FieldDefinition, FilterExpr, ListView, Page, QueryGroup, Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Alert, Skeleton } from '@/components/ui/feedback';
import { QueryFeedback } from '@/features/admin/common';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/menu';
import { FieldDisplay } from '@/features/fields/display';
import { useValueNames } from '@/features/fields/names';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';

type LayoutProps = {
  items: Resource[];
  fields: FieldDefinition[];
  /** Names of people, terms and looked-up items in the values. */
  names?: Map<string, string>;
  onOpen: (item: Resource) => void;
};

function CardFields({ item, fields, names }: { item: Resource; fields: FieldDefinition[]; names?: Map<string, string> }) {
  return (
    <div className="grid gap-1 text-xs text-muted">
      {fields.slice(0, 3).map((field) => (
        <div key={field.id}>
          <span>{field.label}: </span>
          <FieldDisplay field={field} value={item.values?.[field.key]} names={names} />
        </div>
      ))}
    </div>
  );
}

export function ItemsGallery({ items, fields, names, onOpen }: LayoutProps) {
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
            <CardFields item={item} fields={fields} names={names} />
          </button>
        </li>
      ))}
    </ul>
  );
}

/** Where a board column's items come from: a value of the grouping field, or no value. */
export function boardColumnFilter(field: string, column: string): FilterExpr {
  return column ? { field, op: 'eq', value: column } : { field, op: 'missing' };
}

const boardPageSize = 20;
const dragType = 'application/x-papergo-item';

/**
 * A board over the whole collection: column counts come from the view's groups and each column loads its own
 * cards page by page. Cards move by drag and drop or the keyboard-accessible Move to menu.
 */
export function ItemsBoard({
  collectionId,
  view,
  fields,
  groupBy,
  readOnly,
  onOpen,
}: {
  collectionId: string;
  view: ListView;
  fields: FieldDefinition[];
  groupBy?: FieldDefinition;
  readOnly?: boolean;
  onOpen: (item: Resource) => void;
}) {
  const client = useQueryClient();
  const format = useFormat();
  const [over, setOver] = useState<string>();
  const valid = !!groupBy && groupBy.type === 'choice' && !groupBy.options?.multiple;
  const groupsCall = viewGroupsRequest(collectionId, view);
  const groups = useQuery({
    queryKey: ['resources', collectionId, 'board-groups', groupsCall.body],
    enabled: valid,
    queryFn: async () =>
      (await api<Page<QueryGroup>>(groupsCall.path, { method: 'POST', query: groupsCall.query, body: groupsCall.body })).data
        .value ?? [],
  });
  const move = useMutation({
    meta: { silent: true },
    mutationFn: ({ item, value }: { item: Pick<Resource, 'id' | 'version'>; value: string }) =>
      api(`/v1/resources/${item.id}`, {
        method: 'PATCH',
        ifMatch: item.version,
        body: { values: { [groupBy!.key]: value || null } },
      }),
    onError: (error) => toast.error(problemMessage(error)),
    onSettled: () => client.invalidateQueries({ queryKey: keys.resources }),
  });
  if (!valid) return <Alert>This board needs a single-choice grouping field. Choose one in Manage views.</Alert>;
  const counts = new Map((groups.data ?? []).map((group) => [group.value == null ? '' : String(group.value), group.count]));
  const columns = [
    ...new Set([
      ...(groupBy.choices ?? []),
      ...[...counts.keys()].filter(Boolean),
      ...(!groupBy.required || counts.get('') ? [''] : []),
    ]),
  ];
  const targets = (column: string) =>
    columns.filter((value) => value !== column && (value === '' || groupBy.choices?.includes(value)));
  const moveTo = (item: Pick<Resource, 'id' | 'version'>, from: string, value: string) => {
    if (!readOnly && from !== value && !move.isPending) move.mutate({ item, value });
  };
  return (
    <div className="flex gap-3 overflow-auto pb-2">
      {groups.isError && <QueryFeedback query={groups} />}
      {columns.map((column) => (
        <section
          key={column}
          aria-label={column || 'No value'}
          className={`flex max-h-[75vh] w-72 shrink-0 flex-col rounded-lg border bg-surface-muted ${over === column ? 'border-accent' : ''}`}
          onDragOver={(event) => {
            if (readOnly || !event.dataTransfer.types.includes(dragType)) return;
            event.preventDefault();
            setOver(column);
          }}
          onDragLeave={() => setOver(undefined)}
          onDrop={(event) => {
            event.preventDefault();
            setOver(undefined);
            try {
              const dragged = JSON.parse(event.dataTransfer.getData(dragType)) as { id: string; version: number; from: string };
              moveTo(dragged, dragged.from, column);
            } catch {
              // Not a card of this board.
            }
          }}
        >
          <h3 className="p-3 text-sm font-semibold">
            {column || 'No value'}{' '}
            <span className="text-muted">{groups.isSuccess ? format.number(counts.get(column) ?? 0) : ''}</span>
          </h3>
          <BoardColumn
            collectionId={collectionId}
            view={view}
            filter={boardColumnFilter(groupBy.key, column)}
            fields={fields.filter((field) => field.key !== groupBy.key)}
            readOnly={readOnly || move.isPending}
            column={column}
            targets={targets(column)}
            onMove={(item, value) => moveTo(item, column, value)}
            onOpen={onOpen}
          />
        </section>
      ))}
    </div>
  );
}

function BoardColumn({
  collectionId,
  view,
  filter,
  fields,
  readOnly,
  column,
  targets,
  onMove,
  onOpen,
}: {
  collectionId: string;
  view: ListView;
  filter: FilterExpr;
  fields: FieldDefinition[];
  readOnly?: boolean;
  column: string;
  targets: string[];
  onMove: (item: Resource, value: string) => void;
  onOpen: (item: Resource) => void;
}) {
  const call = viewRowsRequest(collectionId, view, filter, boardPageSize);
  const cards = useApiPages<Resource>(['resources', collectionId, 'board'], call.path, { query: call.query, body: call.body });
  const names = useValueNames(fields, cards.items);
  if (cards.isPending) return <Skeleton className="m-2 h-16" />;
  if (cards.isError) return <QueryFeedback query={cards} />;
  return (
    <div className="overflow-y-auto">
      <ul className="grid min-h-16 gap-2 p-2">
        {cards.items.map((item) => (
          <li
            key={item.id}
            draggable={!readOnly && item.kind === 'item'}
            onDragStart={(event) =>
              event.dataTransfer.setData(dragType, JSON.stringify({ id: item.id, version: item.version, from: column }))
            }
            className="grid gap-2 rounded border bg-surface p-3"
          >
            <div className="flex items-start gap-2">
              <button type="button" className="flex-1 text-left font-medium" onClick={() => onOpen(item)}>
                {item.name}
              </button>
              {!readOnly && item.kind === 'item' && (
                <DropdownMenu>
                  <DropdownMenuTrigger aria-label={`Move ${item.name} to`}>
                    <MoreHorizontal className="size-4" />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent>
                    <DropdownMenuLabel>Move to</DropdownMenuLabel>
                    {targets.map((value) => (
                      <DropdownMenuItem key={value} onSelect={() => onMove(item, value)}>
                        {value || 'No value'}
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
            </div>
            <CardFields item={item} fields={fields} names={names} />
          </li>
        ))}
      </ul>
      {cards.items.length === 0 && <p className="px-3 pb-3 text-xs text-muted">Nothing here</p>}
      {cards.hasNextPage && (
        <Button
          size="sm"
          variant="ghost"
          className="mx-2 mb-2"
          disabled={cards.isFetchingNextPage}
          onClick={() => void cards.fetchNextPage()}
        >
          {cards.isFetchingNextPage ? 'Loading…' : 'Load more'}
        </Button>
      )}
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

/** Items of a month at most; a fuller month asks people to narrow the view. */
const calendarLimit = 1000;
const calendarDayLimit = 4;

/**
 * The filter for the days a calendar shows. Date fields compare days; moments compare instants, widened by the
 * largest time zone offsets so that every moment of those days in the person's time zone is included.
 */
export function calendarRangeFilter(field: FieldDefinition, first: string, last: string): FilterExpr {
  if (field.type === 'date') {
    return { and: [{ field: field.key, op: 'gte', value: first }, { field: field.key, op: 'lte', value: last }] };
  }
  const hour = 3_600_000;
  const from = new Date(Date.parse(`${first}T00:00:00Z`) - 14 * hour).toISOString();
  const to = new Date(Date.parse(`${last}T00:00:00Z`) + 24 * hour + 14 * hour).toISOString();
  return { and: [{ field: field.key, op: 'gte', value: from }, { field: field.key, op: 'lt', value: to }] };
}

export function ItemsCalendar({
  collectionId,
  view,
  dateField,
  onOpen,
}: {
  collectionId: string;
  view: ListView;
  dateField?: FieldDefinition;
  onOpen: (item: Resource) => void;
}) {
  const format = useFormat();
  const today = format.dayKey(new Date());
  const [cursor, setCursor] = useState(today.slice(0, 7));
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const anchor = parseISO(`${cursor}-01`);
  const days = eachDayOfInterval({
    start: startOfWeek(startOfMonth(anchor), { weekStartsOn: 1 }),
    end: endOfWeek(endOfMonth(anchor), { weekStartsOn: 1 }),
  }).map((date) => dateKey(date, 'yyyy-MM-dd'));
  const valid = !!dateField && ['date', 'datetime'].includes(dateField.type);
  const eventCalendar = dateField?.key === 'event_start' || dateField?.key === 'event_start_date';
  const from = new Date(Date.parse(`${days[0]}T00:00:00Z`) - 14 * 3_600_000).toISOString();
  const to = new Date(Date.parse(`${days.at(-1)}T00:00:00Z`) + 38 * 3_600_000).toISOString();
  const occurrences = useQuery({
    queryKey: ['resources', collectionId, 'calendar-occurrences', from, to, view.query],
    queryFn: async () => (await api<{ key: string; name: string; start: string; end: string; all_day: boolean; start_date?: string; item: Resource }[]>(`/v1/resources/${collectionId}/calendar/occurrences`, { method: 'POST', body: { from, to, query: view.query } })).data,
    enabled: eventCalendar,
  });
  const ranged = valid
    ? viewRowsRequest(
        collectionId,
        { ...view, query: { ...view.query, sort: [{ field: dateField.key, direction: 'asc' }] } },
        calendarRangeFilter(dateField, days[0], days.at(-1)!),
        200,
      )
    : undefined;
  const items = useApiPages<Resource>(['resources', collectionId, 'calendar'], ranged?.path ?? '', {
    query: ranged?.query,
    body: ranged?.body,
    enabled: valid && !eventCalendar,
  });
  // Custom date fields can be empty; created and modified dates never are.
  const undatedCall =
    valid && !dateField.key.startsWith('$')
      ? viewRowsRequest(collectionId, view, { field: dateField.key, op: 'missing' }, 20)
      : undefined;
  const undated = useApiPages<Resource>(['resources', collectionId, 'calendar-undated'], undatedCall?.path ?? '', {
    query: undatedCall?.query,
    body: undatedCall?.body,
    enabled: !!undatedCall,
  });
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = items;
  const loaded = items.items.length;
  useEffect(() => {
    if (hasNextPage && !isFetchingNextPage && loaded < calendarLimit) void fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, loaded, fetchNextPage]);
  if (!valid) return <Alert>This calendar needs a date grouping field. Choose one in Manage views.</Alert>;
  const byDay = new Map<string, (Resource & { occurrence_key?: string })[]>();
  const visibleItems = eventCalendar ? (occurrences.data ?? []).map((o) => ({
    ...o.item,
    name: o.name,
    occurrence_key: o.key,
    values: {
      ...o.item.values,
      event_start: o.start,
      event_all_day: o.all_day,
      event_start_date: o.all_day ? o.start_date : format.dayKey(new Date(o.start)),
    },
  })) : items.items;
  for (const item of visibleItems) {
    const day = eventCalendar && item.values?.event_all_day === true
      ? item.values.event_start_date as string | undefined
      : calendarDay(item, dateField, format.dayKey);
    if (day) byDay.set(day, [...(byDay.get(day) ?? []), item]);
  }
  const inMonth = [...byDay.entries()].filter(([day]) => day.startsWith(cursor)).reduce((sum, [, list]) => sum + list.length, 0);
  const show = (month: Date) => {
    setCursor(dateKey(month, 'yyyy-MM'));
    setExpanded(new Set());
  };
  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <Button aria-label="Previous month" onClick={() => show(addMonths(anchor, -1))}>
          ‹
        </Button>
        <span className="font-semibold">{cursor}</span>
        <Button aria-label="Next month" onClick={() => show(addMonths(anchor, 1))}>
          ›
        </Button>
        <Button onClick={() => show(parseISO(`${today.slice(0, 7)}-01`))}>Today</Button>
        <span role="status" className="text-xs text-muted">
          {(eventCalendar ? occurrences.isPending : items.isPending || (hasNextPage && loaded < calendarLimit))
            ? 'Loading…'
            : hasNextPage
              ? `Showing the first ${format.number(loaded)} items of these weeks. Filter the view to see the rest.`
              : `${format.number(inMonth)} ${inMonth === 1 ? 'item' : 'items'} this month`}
        </span>
      </div>
      {items.isError && <QueryFeedback query={items} />}
      {eventCalendar && <QueryFeedback query={occurrences} />}
      <div className="overflow-auto rounded border bg-surface">
        <div className="grid min-w-[600px] grid-cols-7">
          {days.slice(0, 7).map((day) => (
            <div key={day} className="border-b p-2 text-xs text-muted">
              {format.weekday(day, 'short')}
            </div>
          ))}
          {days.map((day) => {
            const entries = byDay.get(day) ?? [];
            const open = expanded.has(day);
            const visible = open ? entries : entries.slice(0, calendarDayLimit);
            return (
              <div
                key={day}
                className={`min-h-24 border-r border-b p-2 ${day.slice(0, 7) !== cursor ? 'bg-surface-muted' : ''}`}
              >
                <span className={`text-xs ${day === today ? 'font-bold text-accent' : ''}`}>{Number(day.slice(8))}</span>
                <ul className="mt-1 grid gap-1">
                  {visible.map((item) => (
                    <li key={`${item.id}:${item.occurrence_key ?? ''}`}>
                      <button
                        type="button"
                        className="w-full truncate rounded px-1 text-left text-xs hover:bg-accent-soft"
                        onClick={() => onOpen(item)}
                      >
                        {item.name}
                      </button>
                    </li>
                  ))}
                </ul>
                {entries.length > calendarDayLimit && (
                  <button
                    type="button"
                    className="mt-1 px-1 text-xs text-accent hover:underline"
                    aria-expanded={open}
                    onClick={() =>
                      setExpanded((current) => {
                        const next = new Set(current);
                        if (open) next.delete(day);
                        else next.add(day);
                        return next;
                      })
                    }
                  >
                    {open ? 'Show less' : `${entries.length - calendarDayLimit} more`}
                  </button>
                )}
              </div>
            );
          })}
        </div>
      </div>
      {undated.items.length > 0 && (
        <div>
          <h3 className="text-sm font-medium">No {dateField.label}</h3>
          <ul>
            {undated.items.map((item) => (
              <li key={item.id}>
                <button type="button" className="text-sm hover:underline" onClick={() => onOpen(item)}>
                  {item.name}
                </button>
              </li>
            ))}
          </ul>
          {undated.hasNextPage && (
            <Button size="sm" variant="ghost" disabled={undated.isFetchingNextPage} onClick={() => void undated.fetchNextPage()}>
              Load more
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
