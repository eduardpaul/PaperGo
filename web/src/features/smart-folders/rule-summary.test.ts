// @vitest-environment jsdom
import { afterEach, expect, it } from 'vitest';
import type { FieldDefinition } from '@/api/types';
import { i18n, setLanguage } from '@/i18n';
import { describeFilter } from './rule-summary';

afterEach(() => setLanguage('en'));

const fields: FieldDefinition[] = [
  { id: 'a', key: 'status', label: 'Status', type: 'text' },
  { id: 'b', key: 'owner', label: 'Owner', type: 'text' },
];
const filter = {
  or: [
    { field: 'status', op: 'eq', value: 'Open' },
    { and: [{ field: 'owner', op: 'missing' }, { field: 'status', op: 'ne', value: 'Done' }] },
  ],
};

it('joins conditions as the language joins a list', async () => {
  const t = () => i18n.getFixedT(null, ['smartFolders', 'common', 'lists']) as Parameters<typeof describeFilter>[2];
  const english = describeFilter(filter, fields, t());
  expect(english).toContain(') or ((');
  expect(english).toContain(') and (');
  await setLanguage('es');
  const spanish = describeFilter(filter, fields, t());
  expect(spanish).toContain(') o ((');
  expect(spanish).toContain(') y (');
});
