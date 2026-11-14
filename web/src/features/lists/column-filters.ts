import { addDays, addMonths, addYears, endOfMonth, endOfWeek, endOfYear, format as dayText, parseISO, startOfMonth, startOfWeek, startOfYear } from 'date-fns';
import { typedNumber } from '@papergo/format';
import { numericInput } from '@/api/json';
import type { FieldDefinition, FilterExpr } from '@/api/types';
import { systemFields } from './schema';

/**
 * Column filters: what a person filters a list's or library's columns by, on top of the open view. They live in the
 * URL as one condition per column and become a collection query filter, ANDed with the view's own filter.
 */

/** A value a column is filtered by: a choice, text, an id (person, item, term, keyword) or yes/no. */
export type FilterValue = string | boolean;

export type ColumnFilter =
  /** Text containing this, ignoring case. */
  | { op: 'contains'; text: string }
  /** Exactly this value: a text, a number (as JSON number text) or a day (yyyy-MM-dd). */
  | { op: 'equals'; value: string }
  /** Between two numbers or days, both included; either may be open. */
  | { op: 'range'; from?: string; to?: string }
  | { op: 'empty' }
  | { op: 'present' }
  /**
   * Any of the values (or, with `exclude`, none of them). `empty` counts items without a value as one of them, `me`
   * the person looking, and `sub` the terms below a chosen term.
   */
  | { op: 'values'; values: FilterValue[]; empty?: true; me?: true; sub?: true; exclude?: true };

export type ColumnFilterOp = ColumnFilter['op'];

/** The filters of a page, by column key, in the order they were added. */
export type ColumnFilters = Record<string, ColumnFilter>;

/** How a column is filtered, after the kind of value its field holds. */
export type FilterKind = 'name' | 'text' | 'number' | 'date' | 'datetime' | 'choice' | 'boolean' | 'person' | 'lookup' | 'term' | 'keywords';

/** Why a column cannot be filtered. */
export type Unfilterable = 'note' | 'unindexed' | 'building' | 'failed' | 'unknown';

export type Filterable = { kind: FilterKind; field: FieldDefinition; system: boolean; ops: ColumnFilterOp[] };
export type Filterability = Filterable | { kind: null; reason: Unfilterable };

/** The ways each kind of column filters, the first being the one a new filter starts with. */
const kindOps: Record<FilterKind, ColumnFilterOp[]> = {
  name: ['contains', 'equals'],
  text: ['contains', 'values', 'equals', 'empty', 'present'],
  number: ['range', 'equals', 'empty', 'present'],
  date: ['range', 'equals', 'empty', 'present'],
  datetime: ['range', 'equals', 'empty', 'present'],
  choice: ['values'],
  boolean: ['values'],
  person: ['values'],
  lookup: ['values'],
  term: ['values'],
  keywords: ['values'],
};

const typeKinds: Record<string, FilterKind> = {
  text: 'text',
  email: 'text',
  url: 'text',
  integer: 'number',
  decimal: 'number',
  number: 'number',
  currency: 'number',
  date: 'date',
  datetime: 'datetime',
  choice: 'choice',
  boolean: 'boolean',
  person: 'person',
  lookup: 'lookup',
  term: 'term',
  keywords: 'keywords',
};

/**
 * Whether and how a column can be filtered. Built-in columns always can, except the id; a field needs a ready index,
 * as every query condition on it does. Notes are long text without index rows: they cannot be filtered.
 */
export function filterability(column: string, fields: FieldDefinition[]): Filterability {
  const system = systemFields.find((field) => field.key === column);
  if (system) {
    if (column === '$id') return { kind: null, reason: 'unknown' };
    const kind: FilterKind = column === '$name' ? 'name' : (typeKinds[system.type] ?? 'text');
    // Built-in dates and people always hold a value (or the query cannot tell), so they offer no empty filter.
    const ops = kind === 'date' || kind === 'datetime' ? ['range', 'equals'] as ColumnFilterOp[] : kindOps[kind];
    return { kind, field: system, system: true, ops };
  }
  const field = fields.find((entry) => entry.key === column);
  if (!field) return { kind: null, reason: 'unknown' };
  if (field.type === 'note') return { kind: null, reason: 'note' };
  if (!field.indexed) return { kind: null, reason: 'unindexed' };
  if (field.index_status === 'building') return { kind: null, reason: 'building' };
  if (field.index_status && field.index_status !== 'ready') return { kind: null, reason: 'failed' };
  const kind = typeKinds[field.type];
  if (!kind) return { kind: null, reason: 'unknown' };
  if (kind === 'lookup' && !field.options?.lookup_container_id) return { kind: null, reason: 'unknown' };
  if (kind === 'term' && !field.options?.term_set_id) return { kind: null, reason: 'unknown' };
  return { kind, field, system: false, ops: kindOps[kind] };
}

/** Whether items without a value can be picked: custom fields, and item keywords. */
export function canPickEmpty(info: Filterability): boolean {
  return !!info.kind && (!info.system || info.field.key === '$keywords');
}

/** Whether a term filter offers "include sub-terms"; a field that applies parents always includes them. */
export function offersSubterms(info: Filterability): boolean {
  return info.kind === 'term' && !info.field.options?.apply_parents;
}

/** Whether a bound or value suits the column: a day (yyyy-MM-dd) for dates, JSON number text for numbers. */
function validBound(info: Filterable, text: string | undefined): boolean {
  if (text === undefined) return true;
  if (info.kind === 'date' || info.kind === 'datetime') return /^\d{4}-\d{2}-\d{2}$/.test(text) && !Number.isNaN(Date.parse(text));
  if (info.kind === 'number') return typedNumber(text, '.', info.field.type === 'integer') === text;
  return true;
}

/** The filter as it can apply to the column, or undefined when it cannot (a stale or hand-written URL). */
export function usableFilter(info: Filterability, filter: ColumnFilter): ColumnFilter | undefined {
  if (!info.kind || !info.ops.includes(filter.op)) return undefined;
  switch (filter.op) {
    case 'contains':
      return filter.text.trim() ? filter : undefined;
    case 'equals':
      return filter.value.trim() && validBound(info, filter.value) ? filter : undefined;
    case 'range':
      return (filter.from || filter.to) && validBound(info, filter.from) && validBound(info, filter.to) ? filter : undefined;
    case 'values': {
      const values = info.kind === 'boolean' ? filter.values.filter((value) => typeof value === 'boolean') : filter.values.filter((value) => typeof value === 'string' && value !== '');
      const next: ColumnFilter = { op: 'values', values: [...new Set(values)] };
      if (filter.empty && canPickEmpty(info)) next.empty = true;
      if (filter.me && info.kind === 'person') next.me = true;
      if (filter.sub && offersSubterms(info)) next.sub = true;
      if (filter.exclude) next.exclude = true;
      return next.values.length || next.empty || next.me ? next : undefined;
    }
    default:
      return filter;
  }
}

/** The API takes at most 50 values in one `in`. */
const valuesPerCondition = 50;

/**
 * The query condition of a column filter. Numbers are exact JSON numbers (decimals their text); a day of a date-time
 * column runs from its start in the person's time zone to the start of the next day. Throws on a number that is not one.
 */
export function columnCondition(column: string, info: Filterability, filter: ColumnFilter, dayStart: (day: string) => string): FilterExpr {
  if (!info.kind) throw new Error(`column ${column} cannot be filtered`);
  const field = column;
  const literal = (text: string): unknown =>
    info.kind !== 'number' || info.field.type === 'decimal' ? text : numericInput(text, info.field.type === 'integer');
  const range = (from?: string, to?: string): FilterExpr => {
    const parts: FilterExpr[] = [];
    if (info.kind === 'datetime') {
      if (from) parts.push({ field, op: 'gte', value: dayStart(from) });
      if (to) parts.push({ field, op: 'lt', value: dayStart(nextDay(to)) });
    } else {
      if (from) parts.push({ field, op: 'gte', value: literal(from) });
      if (to) parts.push({ field, op: 'lte', value: literal(to) });
    }
    return parts.length === 1 ? parts[0] : { and: parts };
  };
  switch (filter.op) {
    case 'contains':
      return { field, op: 'contains', value: filter.text.trim() };
    case 'equals':
      return info.kind === 'datetime' ? range(filter.value, filter.value) : { field, op: 'eq', value: literal(filter.value) };
    case 'range':
      return range(filter.from, filter.to);
    case 'empty':
      return { field, op: 'missing' };
    case 'present':
      return { field, op: 'present' };
    case 'values': {
      const parts: FilterExpr[] = [];
      if (field === '$keywords') {
        // Item keywords compare one at a time.
        parts.push(...filter.values.map((value) => ({ field, op: 'eq', value })));
      } else if (filter.sub) {
        parts.push(...filter.values.map((value) => ({ field, op: 'under', value })));
      } else {
        for (let at = 0; at < filter.values.length; at += valuesPerCondition) {
          const chunk = filter.values.slice(at, at + valuesPerCondition);
          parts.push(chunk.length === 1 ? { field, op: 'eq', value: chunk[0] } : { field, op: 'in', value: chunk });
        }
      }
      if (filter.me) parts.push({ field, op: 'eq', value_ref: 'me' });
      if (filter.empty) parts.push({ field, op: 'missing' });
      const any = parts.length === 1 ? parts[0] : { or: parts };
      return filter.exclude ? { not: any } : any;
    }
  }
}

/** The conditions of a page's column filters that apply, in their order; filters that cannot apply are left out. */
export function columnConditions(filters: ColumnFilters | undefined, fields: FieldDefinition[], dayStart: (day: string) => string, except?: string): FilterExpr[] {
  const out: FilterExpr[] = [];
  for (const [column, filter] of Object.entries(filters ?? {})) {
    if (column === except) continue;
    const info = filterability(column, fields);
    const usable = usableFilter(info, filter);
    if (!usable) continue;
    try {
      out.push(columnCondition(column, info, usable, dayStart));
    } catch {
      // A number that is not one, from an edited URL.
    }
  }
  return out;
}

/** The column filters that apply, by column. */
export function activeFilters(filters: ColumnFilters | undefined, fields: FieldDefinition[]): [string, ColumnFilter][] {
  return Object.entries(filters ?? {}).flatMap(([column, filter]) => {
    const usable = usableFilter(filterability(column, fields), filter);
    return usable ? [[column, usable] as [string, ColumnFilter]] : [];
  });
}

/** The query API's limits on a filter. */
export const filterLimits = { nodes: 32, depth: 6 };

/** Whether a filter stays within the API's limits on nodes and depth. */
export function withinLimits(filter: FilterExpr | undefined): boolean {
  let nodes = 0;
  let deepest = 0;
  const walk = (node: FilterExpr, depth: number) => {
    nodes++;
    deepest = Math.max(deepest, depth);
    for (const child of [...(node.and ?? []), ...(node.or ?? []), ...(node.not ? [node.not] : [])]) walk(child, depth + 1);
  };
  if (filter) walk(filter, 0);
  return nodes <= filterLimits.nodes && deepest <= filterLimits.depth;
}

/** Reads column filters from the URL; anything malformed is left out. */
export function parseColumnFilters(value: unknown): ColumnFilters | undefined {
  let raw = value;
  if (typeof raw === 'string') {
    try {
      raw = JSON.parse(raw);
    } catch {
      return undefined;
    }
  }
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return undefined;
  const out: ColumnFilters = {};
  const text = (entry: unknown) => (typeof entry === 'string' ? entry : undefined);
  for (const [column, entry] of Object.entries(raw as Record<string, unknown>)) {
    if (!entry || typeof entry !== 'object') continue;
    const e = entry as Record<string, unknown>;
    let filter: ColumnFilter | undefined;
    switch (e.op) {
      case 'contains':
        filter = text(e.text) ? { op: 'contains', text: text(e.text)! } : undefined;
        break;
      case 'equals':
        filter = text(e.value) ? { op: 'equals', value: text(e.value)! } : undefined;
        break;
      case 'range':
        filter = text(e.from) || text(e.to) ? { op: 'range', ...(text(e.from) ? { from: text(e.from) } : {}), ...(text(e.to) ? { to: text(e.to) } : {}) } : undefined;
        break;
      case 'empty':
      case 'present':
        filter = { op: e.op };
        break;
      case 'values': {
        const values = Array.isArray(e.values) ? e.values.filter((v): v is FilterValue => typeof v === 'string' || typeof v === 'boolean') : [];
        const next: ColumnFilter = { op: 'values', values };
        if (e.empty === true) next.empty = true;
        if (e.me === true) next.me = true;
        if (e.sub === true) next.sub = true;
        if (e.exclude === true) next.exclude = true;
        filter = next;
        break;
      }
    }
    if (filter) out[column] = filter;
  }
  return Object.keys(out).length ? out : undefined;
}

/** The filters with one column's filter changed or removed; undefined when none is left. */
export function withColumnFilter(filters: ColumnFilters | undefined, column: string, filter: ColumnFilter | undefined): ColumnFilters | undefined {
  const next = { ...(filters ?? {}) };
  if (filter) next[column] = filter;
  else delete next[column];
  return Object.keys(next).length ? next : undefined;
}

function nextDay(day: string): string {
  return dayText(addDays(parseISO(day), 1), 'yyyy-MM-dd');
}

/** The periods a date filter offers, each a range of days. */
export const periods = ['today', 'yesterday', 'thisWeek', 'lastWeek', 'thisMonth', 'lastMonth', 'thisYear', 'lastYear', 'last7Days', 'last30Days'] as const;
export type Period = (typeof periods)[number];

/** The first and last day of a period around today (yyyy-MM-dd), weeks starting on Monday. */
export function periodRange(period: Period, today: string): { from: string; to: string } {
  const day = parseISO(today);
  const text = (date: Date) => dayText(date, 'yyyy-MM-dd');
  const week = { weekStartsOn: 1 as const };
  switch (period) {
    case 'today':
      return { from: today, to: today };
    case 'yesterday':
      return { from: text(addDays(day, -1)), to: text(addDays(day, -1)) };
    case 'thisWeek':
      return { from: text(startOfWeek(day, week)), to: text(endOfWeek(day, week)) };
    case 'lastWeek':
      return { from: text(startOfWeek(addDays(day, -7), week)), to: text(endOfWeek(addDays(day, -7), week)) };
    case 'thisMonth':
      return { from: text(startOfMonth(day)), to: text(endOfMonth(day)) };
    case 'lastMonth':
      return { from: text(startOfMonth(addMonths(day, -1))), to: text(endOfMonth(addMonths(day, -1))) };
    case 'thisYear':
      return { from: text(startOfYear(day)), to: text(endOfYear(day)) };
    case 'lastYear':
      return { from: text(startOfYear(addYears(day, -1))), to: text(endOfYear(addYears(day, -1))) };
    case 'last7Days':
      return { from: text(addDays(day, -6)), to: today };
    case 'last30Days':
      return { from: text(addDays(day, -29)), to: today };
  }
}

/** The first and last day of a calendar month (yyyy-MM). */
export function monthRange(month: string): { from: string; to: string } {
  const first = parseISO(`${month}-01`);
  return { from: dayText(first, 'yyyy-MM-dd'), to: dayText(endOfMonth(first), 'yyyy-MM-dd') };
}
