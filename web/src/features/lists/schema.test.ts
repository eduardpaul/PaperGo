import { expect, it } from 'vitest';
import type { ContentType, FieldDefinition } from '@/api/types';
import { fieldsForType } from './schema';

const field = (id: string): FieldDefinition => ({ id, key: id, label: id, type: 'text' });

it('orders fields as the content type does, not as the field list does', () => {
  const fields = ['amount', 'due', 'owner', 'status'].map(field);
  const types: ContentType[] = [{ id: 'type', key: 'item', name: 'Item', is_default: true, field_ids: ['amount', 'status', 'due'] }];
  expect(fieldsForType(fields, types).map((entry) => entry.id)).toEqual(['amount', 'status', 'due']);
  expect(fieldsForType(fields, []).map((entry) => entry.id)).toEqual(['amount', 'due', 'owner', 'status']);
});
