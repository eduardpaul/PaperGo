import type { ContentType, FieldDefinition, Resource } from '@/api/types';

const labels: Record<string, string> = {
  $name: 'Name',
  $id: 'ID',
  $tags: 'Tags',
  $created_at: 'Created',
  $created_by: 'Created by',
  $modified_at: 'Modified',
  $modified_by: 'Modified by',
};

export function columnLabel(key: string, fields: FieldDefinition[]): string {
  return labels[key] ?? fields.find((field) => field.key === key)?.label ?? key;
}

export function fieldsForType(fields: FieldDefinition[], types: ContentType[], contentTypeId?: string): FieldDefinition[] {
  const selected = types.find((type) => type.id === contentTypeId) ?? types.find((type) => type.is_default);
  const ids = selected?.field_ids?.filter(Boolean);
  if (!ids?.length) return fields;
  const allowed = new Set(ids);
  return fields.filter((field) => allowed.has(field.id));
}

export function publishState(item: Resource, publishingEnabled: boolean): 'Published' | 'Draft' | 'Unpublished' | null {
  if (!publishingEnabled) return null;
  if (!item.published_revision_id) return 'Unpublished';
  if (item.head_revision_id && item.head_revision_id !== item.published_revision_id) return 'Draft';
  return 'Published';
}
