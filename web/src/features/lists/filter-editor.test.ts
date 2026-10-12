import { expect, it } from 'vitest';
import { stringifyJSON } from '@/api/json';
import { normalizeFilter } from './filter-editor';
it('normalizes exact numeric literals inside nested filters without changing references', () => {
  const fields = [{ id: 'serial', key: 'serial', label: 'Serial', type: 'integer' }];
  const filter = normalizeFilter(
    {
      and: [
        { field: 'serial', op: 'in', value: ['9007199254740993', '9223372036854775807'] },
        { not: { field: 'serial', op: 'missing', value: 'discarded' } },
        { field: '$created_by', op: 'eq', value_ref: 'me' },
      ],
    },
    fields,
  );
  expect(stringifyJSON(filter)).toBe(
    '{"and":[{"field":"serial","op":"in","value":[9007199254740993,9223372036854775807]},{"not":{"field":"serial","op":"missing"}},{"field":"$created_by","op":"eq","value_ref":"me"}]}',
  );
});
