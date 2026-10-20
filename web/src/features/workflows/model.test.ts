import { describe, expect, it } from 'vitest';
import { stringifyJSON } from '@/api/json';
import { normalizeForm } from './model';

describe('workflow editing', () => {
  it('normalizes typed nested launch values', () => {
    const schema = {
      type: 'object',
      properties: {
        entries: { type: 'array', items: { type: 'object', properties: { amount: { type: 'integer' } } } },
        token: { type: 'number' },
      },
    };
    expect(
      stringifyJSON(
        normalizeForm(schema, { entries: [{ amount: '9007199254740993' }], token: '2.5' }),
      ),
    ).toBe('{"entries":[{"amount":9007199254740993}],"token":2.5}');
    expect(() => normalizeForm(schema, { entries: [{ amount: '1.5' }] })).toThrow();
  });
});
