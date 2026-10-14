import { describe, expect, it } from 'vitest';
import { typedParameters } from './builtins';

describe('built-in parameters', () => {
  it('types numbers and lists and leaves out empty values', () => {
    const properties = {
      target_height: { type: 'number' },
      quality: { type: 'integer' },
      approvers: { type: 'array' },
      field: { title: 'Field' },
    };
    expect(typedParameters(properties, { target_height: '16', quality: ' 75 ', approvers: 'alice, , bob', field: 'expires' })).toEqual({
      target_height: 16,
      quality: 75,
      approvers: ['alice', 'bob'],
      field: 'expires',
    });
    // Empty values get the server's defaults; text the server rejects is sent as typed.
    expect(typedParameters(properties, { target_height: '', approvers: ' ', quality: 'high' })).toEqual({ quality: 'high' });
  });
});
