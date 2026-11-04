import { describe, expect, it } from 'vitest';
import { stringifyJSON } from '@/api/json';
import { normalizeForm, type FormSchema } from './model';

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

  it('fills dates that default to now with the moment the form is sent and leaves cleared dates out', () => {
    const schema: FormSchema = {
      type: 'object',
      required: ['depth_cm'],
      properties: {
        depth_cm: { type: 'number' },
        measured_at: { type: 'string', format: 'date-time', 'x-papergo-default': 'now' },
        day: { type: 'string', format: 'date', 'x-papergo-default': 'now' },
        due: { type: 'string', format: 'date' },
      },
    };
    const now = new Date(2026, 10, 4, 23, 45);
    expect(normalizeForm(schema, { depth_cm: '41', measured_at: '', due: '' }, now)).toEqual({
      depth_cm: 41,
      measured_at: now.toISOString(),
      day: '2026-11-04',
    });
    expect(normalizeForm(schema, { depth_cm: '41', measured_at: '2026-11-02T17:05:00.000Z', day: '2026-11-02' }, now)).toEqual({
      depth_cm: 41,
      measured_at: '2026-11-02T17:05:00.000Z',
      day: '2026-11-02',
    });
  });
});
