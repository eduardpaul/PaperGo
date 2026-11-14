import { describe, expect, it } from 'vitest';
import { stringifyJSON } from '@/api/json';
import { andFilter, listingRequest } from '@/api/requests';
import type { FieldDefinition } from '@/api/types';
import {
  activeFilters,
  columnCondition,
  columnConditions,
  filterability,
  parseColumnFilters,
  periodRange,
  usableFilter,
  withColumnFilter,
  withinLimits,
  type ColumnFilter,
} from './column-filters';

const field = (key: string, type: string, extra: Partial<FieldDefinition> = {}): FieldDefinition => ({ id: key, key, label: key, type, indexed: true, index_status: 'ready', ...extra });

// The "Líneas" view of a receipts library, and one field of each other kind.
const fields: FieldDefinition[] = [
  field('descripcion', 'text'),
  field('producto', 'text'),
  field('categoria', 'choice', { choices: ['Quesos', 'Fruta', 'Pan'] }),
  field('cantidad', 'integer'),
  field('importe', 'decimal', { scale: 2 }),
  field('precio', 'currency', { options: { currency_code: 'EUR' } }),
  field('fecha', 'date'),
  field('pagado', 'datetime'),
  field('estado', 'choice', { choices: ['Abierto', 'Cerrado'], options: { multiple: true } }),
  field('revisado', 'boolean'),
  field('responsable', 'person'),
  field('proveedor', 'lookup', { options: { lookup_container_id: 'suppliers' } }),
  field('tema', 'term', { options: { term_set_id: 'topics' } }),
  field('region', 'term', { options: { term_set_id: 'regions', apply_parents: true } }),
  field('etiquetas', 'keywords', { options: { multiple: true } }),
  field('correo', 'email'),
  field('notas', 'note'),
  field('sin_indice', 'text', { indexed: false }),
  field('construyendo', 'text', { index_status: 'building' }),
  field('fallido', 'text', { index_status: 'failed' }),
];
const madrid = (day: string) => `${day}@Madrid`;
const condition = (column: string, filter: ColumnFilter) => JSON.parse(stringifyJSON(columnCondition(column, filterability(column, fields), filter, madrid))!);

describe('which columns filter, and how', () => {
  it('maps every field type to the filters that suit it', () => {
    const kinds = Object.fromEntries(
      [...fields.map((f) => f.key), '$name', '$created_at', '$modified_by', '$keywords', '$id', 'removed'].map((key) => {
        const info = filterability(key, fields);
        return [key, info.kind ? `${info.kind}: ${info.ops.join(' ')}` : `no: ${info.reason}`];
      }),
    );
    expect(kinds).toEqual({
      descripcion: 'text: contains values equals empty present',
      producto: 'text: contains values equals empty present',
      categoria: 'choice: values',
      cantidad: 'number: range equals empty present',
      importe: 'number: range equals empty present',
      precio: 'number: range equals empty present',
      fecha: 'date: range equals empty present',
      pagado: 'datetime: range equals empty present',
      estado: 'choice: values',
      revisado: 'boolean: values',
      responsable: 'person: values',
      proveedor: 'lookup: values',
      tema: 'term: values',
      region: 'term: values',
      etiquetas: 'keywords: values',
      correo: 'text: contains values equals empty present',
      notas: 'no: note',
      sin_indice: 'no: unindexed',
      construyendo: 'no: building',
      fallido: 'no: failed',
      $name: 'name: contains equals',
      $created_at: 'datetime: range equals',
      $modified_by: 'person: values',
      $keywords: 'keywords: values',
      $id: 'no: unknown',
      removed: 'no: unknown',
    });
  });
});

describe('column conditions', () => {
  it('picks choices, with items without one', () => {
    expect(condition('categoria', { op: 'values', values: ['Quesos', 'Fruta'] })).toEqual({ field: 'categoria', op: 'in', value: ['Quesos', 'Fruta'] });
    expect(condition('categoria', { op: 'values', values: ['Quesos'], empty: true })).toEqual({
      or: [{ field: 'categoria', op: 'eq', value: 'Quesos' }, { field: 'categoria', op: 'missing' }],
    });
    // None of the picked values: items without one stay, unless "No value" is picked too.
    expect(condition('estado', { op: 'values', values: ['Cerrado'], exclude: true })).toEqual({ not: { field: 'estado', op: 'eq', value: 'Cerrado' } });
  });

  it('ranges numbers exactly, inclusive at both ends', () => {
    expect(stringifyJSON(columnCondition('cantidad', filterability('cantidad', fields), { op: 'range', from: '9007199254740993' }, madrid))).toBe(
      '{"field":"cantidad","op":"gte","value":9007199254740993}',
    );
    expect(condition('importe', { op: 'range', from: '5', to: '20' })).toEqual({
      and: [{ field: 'importe', op: 'gte', value: '5' }, { field: 'importe', op: 'lte', value: '20' }],
    });
    expect(condition('precio', { op: 'equals', value: '2.5' })).toEqual({ field: 'precio', op: 'eq', value: 2.5 });
  });

  it('ranges dates by day and moments by the days in the person’s time zone', () => {
    expect(condition('fecha', { op: 'range', from: '2026-10-01', to: '2026-10-31' })).toEqual({
      and: [{ field: 'fecha', op: 'gte', value: '2026-10-01' }, { field: 'fecha', op: 'lte', value: '2026-10-31' }],
    });
    expect(condition('pagado', { op: 'range', from: '2026-10-01', to: '2026-10-31' })).toEqual({
      and: [{ field: 'pagado', op: 'gte', value: '2026-10-01@Madrid' }, { field: 'pagado', op: 'lt', value: '2026-11-01@Madrid' }],
    });
    expect(condition('$created_at', { op: 'equals', value: '2026-12-31' })).toEqual({
      and: [{ field: '$created_at', op: 'gte', value: '2026-12-31@Madrid' }, { field: '$created_at', op: 'lt', value: '2027-01-01@Madrid' }],
    });
  });

  it('matches text containing words, exactly, empty or present', () => {
    expect(condition('producto', { op: 'contains', text: ' kiwi ' })).toEqual({ field: 'producto', op: 'contains', value: 'kiwi' });
    expect(condition('producto', { op: 'values', values: ['Kiwi', 'Manzana'] })).toEqual({ field: 'producto', op: 'in', value: ['Kiwi', 'Manzana'] });
    expect(condition('producto', { op: 'empty' })).toEqual({ field: 'producto', op: 'missing' });
    expect(condition('producto', { op: 'present' })).toEqual({ field: 'producto', op: 'present' });
  });

  it('picks people, the person looking, terms with their sub-terms and keywords', () => {
    expect(condition('responsable', { op: 'values', values: ['ana'], me: true })).toEqual({
      or: [{ field: 'responsable', op: 'eq', value: 'ana' }, { field: 'responsable', op: 'eq', value_ref: 'me' }],
    });
    expect(condition('tema', { op: 'values', values: ['t1', 't2'], sub: true })).toEqual({
      or: [{ field: 'tema', op: 'under', value: 't1' }, { field: 'tema', op: 'under', value: 't2' }],
    });
    // Item keywords compare one at a time.
    expect(condition('$keywords', { op: 'values', values: ['k1', 'k2'] })).toEqual({
      or: [{ field: '$keywords', op: 'eq', value: 'k1' }, { field: '$keywords', op: 'eq', value: 'k2' }],
    });
    expect(condition('revisado', { op: 'values', values: [true], empty: true })).toEqual({
      or: [{ field: 'revisado', op: 'eq', value: true }, { field: 'revisado', op: 'missing' }],
    });
  });

  it('splits long value lists into the 50 values a condition takes', () => {
    const values = Array.from({ length: 60 }, (_, i) => `v${i}`);
    const built = condition('producto', { op: 'values', values });
    expect(built.or.map((part: { value: unknown[] }) => part.value.length)).toEqual([50, 10]);
  });
});

describe('filters that cannot apply', () => {
  it('drops what a column does not take, from stale or edited links', () => {
    expect(usableFilter(filterability('notas', fields), { op: 'contains', text: 'x' })).toBeUndefined();
    expect(usableFilter(filterability('categoria', fields), { op: 'contains', text: 'x' })).toBeUndefined();
    expect(usableFilter(filterability('$created_at', fields), { op: 'empty' })).toBeUndefined();
    expect(usableFilter(filterability('cantidad', fields), { op: 'range', from: '2.5' })).toBeUndefined();
    expect(usableFilter(filterability('fecha', fields), { op: 'range', from: 'yesterday' })).toBeUndefined();
    expect(usableFilter(filterability('$name', fields), { op: 'values', values: ['x'] })).toBeUndefined();
    expect(usableFilter(filterability('$modified_by', fields), { op: 'values', values: [], empty: true })).toBeUndefined();
    expect(usableFilter(filterability('region', fields), { op: 'values', values: ['r'], sub: true })).toEqual({ op: 'values', values: ['r'] });
  });

  it('reads the URL parameter, leaving out what is malformed', () => {
    const filters = { importe: { op: 'range', from: '5', to: '20' }, categoria: { op: 'values', values: ['Quesos', 3], empty: true }, broken: { op: 'nope' }, notas: { op: 'contains', text: 'x' } };
    expect(parseColumnFilters(JSON.stringify(filters))).toEqual({
      importe: { op: 'range', from: '5', to: '20' },
      categoria: { op: 'values', values: ['Quesos'], empty: true },
      notas: { op: 'contains', text: 'x' },
    });
    expect(parseColumnFilters('not json')).toBeUndefined();
    expect(activeFilters(parseColumnFilters(filters), fields).map(([column]) => column)).toEqual(['importe', 'categoria']);
  });
});

describe('column filters in the listing', () => {
  it('narrows the view’s own filter, never widens it, and keeps its depth', () => {
    const view = { query: { filter: { and: [{ field: 'estado', op: 'eq', value: 'Abierto' }, { field: 'fecha', op: 'present' }] } }, columns: ['$name', 'importe'] };
    const filters = withColumnFilter(withColumnFilter(undefined, 'importe', { op: 'range', from: '5' }), 'producto', { op: 'contains', text: 'kiwi' });
    const conditions = columnConditions(filters, fields, madrid);
    expect(listingRequest('tiques', { view, filters: conditions }).body).toEqual({
      query: {
        filter: {
          and: [
            { field: 'estado', op: 'eq', value: 'Abierto' },
            { field: 'fecha', op: 'present' },
            { field: 'importe', op: 'gte', value: '5' },
            { field: 'producto', op: 'contains', value: 'kiwi' },
          ],
        },
      },
      select: ['$name', 'importe'],
    });
    // A column's own value list leaves its filter out.
    expect(columnConditions(filters, fields, madrid, 'producto')).toEqual([{ field: 'importe', op: 'gte', value: '5' }]);
    expect(withColumnFilter(filters, 'importe', undefined)).toEqual({ producto: { op: 'contains', text: 'kiwi' } });
  });

  it('browses a folder with filters as a query of its children', () => {
    expect(listingRequest('tiques', { folder: 'f', filters: [{ field: 'producto', op: 'present' }] })).toEqual({
      path: '/v1/resources/tiques/query',
      method: 'POST',
      query: { $top: 50 },
      body: { query: { parent_id: 'f', filter: { field: 'producto', op: 'present' } } },
    });
  });

  it('knows when a filter grows past what the API takes', () => {
    const many = Array.from({ length: 31 }, (_, i) => ({ field: `f${i}`, op: 'present' }));
    expect(withinLimits(andFilter(undefined, ...many))).toBe(true);
    expect(withinLimits(andFilter(undefined, ...many, { field: 'x', op: 'present' }))).toBe(false);
    const deep = { not: { not: { not: { not: { not: { not: { field: 'x', op: 'present' } } } } } } };
    expect(withinLimits(deep)).toBe(true);
    expect(withinLimits(andFilter({ field: 'y', op: 'present' }, deep))).toBe(false);
  });
});

describe('periods', () => {
  it('runs weeks from Monday and months and years whole', () => {
    expect(periodRange('thisWeek', '2026-11-14')).toEqual({ from: '2026-11-09', to: '2026-11-15' });
    expect(periodRange('lastMonth', '2026-03-31')).toEqual({ from: '2026-02-01', to: '2026-02-28' });
    expect(periodRange('last30Days', '2026-11-14')).toEqual({ from: '2026-10-16', to: '2026-11-14' });
    expect(periodRange('lastYear', '2026-11-14')).toEqual({ from: '2025-01-01', to: '2025-12-31' });
  });
});
