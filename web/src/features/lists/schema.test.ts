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

it('applies the type\'s settings to its fields', () => {
  const fields: FieldDefinition[] = [
    { id: 'amount', key: 'amount', label: 'Amount', type: 'integer', required: true, options: { default_value: 1 } },
    { id: 'note', key: 'note', label: 'Note', type: 'text' },
  ];
  const types: ContentType[] = [{
    id: 'quote', key: 'quote', name: 'Quote', is_default: true,
    effective: { field_ids: ['note', 'amount'], hidden_field_ids: [], rules: [], field_settings: [{ field_id: 'amount', required: false, default_value: null }, { field_id: 'note', required: true }] },
  }];
  const [note, amount] = fieldsForType(fields, types);
  expect([note.id, note.required, amount.id, amount.required, amount.options?.default_value]).toEqual(['note', true, 'amount', false, undefined]);
  // The shared field itself stays as it is.
  expect(fields[0].required).toBe(true);
});
