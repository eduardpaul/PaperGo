import type { ContentType, FieldDefinition, Resource } from '@/api/types';

const labels: Record<string, string> = {
  $name: 'Name',
  $id: 'ID',
  $keywords: 'Keywords',
  $created_at: 'Created',
  $created_by: 'Created by',
  $modified_at: 'Modified',
  $modified_by: 'Modified by',
};

export function columnLabel(key: string, fields: FieldDefinition[]): string {
  return labels[key] ?? fields.find((field) => field.key === key)?.label ?? key;
}

/** The fields of a content type in the type's order (the order they were added), or all fields without a type. */
export function fieldsForType(fields: FieldDefinition[], types: ContentType[], contentTypeId?: string): FieldDefinition[] {
  const selected = types.find((type) => type.id === contentTypeId) ?? types.find((type) => type.is_default);
  const ids = selected?.field_ids?.filter(Boolean);
  if (!ids?.length) return fields;
  const byId = new Map(fields.map((field) => [field.id, field]));
  return ids.flatMap((id) => byId.get(id) ?? []);
}

export function publishState(item: Resource, publishingEnabled: boolean): 'Published' | 'Draft' | 'Unpublished' | null {
  if (!publishingEnabled) return null;
  if (!item.published_revision_id) return 'Unpublished';
  if (item.head_revision_id && item.head_revision_id !== item.published_revision_id) return 'Draft';
  return 'Published';
}

const sortableSystemColumns = ['$name', '$created_at', '$created_by', '$modified_at', '$modified_by'];

/** Columns a collection query can sort by: built-in columns and filterable, ready, single-value fields. */
export function sortableColumns(fields: FieldDefinition[]): Set<string> {
  return new Set([
    ...sortableSystemColumns,
    ...fields
      .filter((field) => field.indexed && (!field.index_status || field.index_status === 'ready') && !field.options?.multiple)
      .map((field) => field.key),
  ]);
}

/** The `sort` URL parameter: a column key, with "-" first for descending. */
export function parseSort(value: string | undefined): { field: string; direction: 'asc' | 'desc' } | undefined {
  if (!value) return undefined;
  return value.startsWith('-') ? { field: value.slice(1), direction: 'desc' } : { field: value, direction: 'asc' };
}

export function sortParam(sort: { field: string; direction: 'asc' | 'desc' } | undefined): string | undefined {
  return sort ? `${sort.direction === 'desc' ? '-' : ''}${sort.field}` : undefined;
}
