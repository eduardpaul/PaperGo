import { describe, expect, it } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import { valuesPatch } from './values';

describe('valuesPatch', () => {
  const fields = [{ key: 'title' }, { key: 'tags' }];

  it('omits unchanged fields and clears empty text', () => {
    expect(valuesPatch(fields, { title: 'A', tags: ['x'] }, { title: 'A', tags: [] })).toEqual({ tags: null });
    expect(valuesPatch(fields, { title: 'A' }, { title: '' })).toEqual({ title: null });
    expect(valuesPatch(fields, { title: 'A' }, { title: 'A' })).toBeUndefined();
  });

  it('keeps false and zero', () => {
    expect(valuesPatch([{ key: 'open' }, { key: 'count' }], { open: true, count: 1 }, { open: false, count: 0 })).toEqual({
      open: false,
      count: 0,
    });
  });

  it('normalizes numeric drafts to JSON numbers on save', () => {
    const price = '12.50';
    expect(stringifyJSON(valuesPatch([{ key: 'price', type: 'currency' }], {}, { price }))).toBe('{"price":12.5}');
    expect(valuesPatch([{ key: 'price', type: 'currency' }], { price: 12.5 }, { price })).toBeUndefined();
    expect(stringifyJSON(valuesPatch([{ key: 'price', type: 'number' }], {}, { price: '12.' }))).toBe('{"price":12}');
    expect(stringifyJSON(valuesPatch([{ key: 'price', type: 'number' }], {}, { price: '0.' }))).toBe('{"price":0}');
  });

  it.each([
    ['currency', '9007199254740993'],
    ['number', '0.123456789123456789'],
    ['integer', '9223372036854775807'],
    ['integer', '-9223372036854775808'],
  ])('round-trips exact %s values through reads and edits', (type, raw) => {
    const fields = [{ key: 'value', type }];
    const current = parseJSON(`{"value":${raw}}`) as Record<string, unknown>;
    expect(String(current.value)).toBe(raw);
    expect(valuesPatch(fields, current, { value: raw })).toBeUndefined();
    expect(stringifyJSON(valuesPatch(fields, {}, { value: raw }))).toBe(`{"value":${raw}}`);
  });

  it('rejects invalid numeric drafts instead of sending them as text', () => {
    expect(() => valuesPatch([{ key: 'value', type: 'integer' }], {}, { value: '1.5' })).toThrow('integer');
    expect(() => valuesPatch([{ key: 'value', type: 'number' }], {}, { value: 'Infinity' })).toThrow('number');
  });
});
