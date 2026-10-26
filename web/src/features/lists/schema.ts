import type { ContentType, FieldDefinition, Resource } from '@/api/types';

const labels: Record<string, string> = {
  $name: 'Name',
  $id: 'ID',
  $keywords: 'Keywords',
  $created_at: 'Created',
  $created_by: 'Created by',
  $modified_at: 'Modified',
  $modified_by: 'Modified by',
  $archived_at: 'Archived',
  $archived_by: 'Archived by',
};

export function columnLabel(key: string, fields: FieldDefinition[]): string {
  return labels[key] ?? fields.find((field) => field.key === key)?.label ?? key;
}

/**
 * The fields forms show for a content type: inherited fields first, then the
 * type's own, without the hidden ones. Without a type, all fields.
 */
export function fieldsForType(fields: FieldDefinition[], types: ContentType[], contentTypeId?: string): FieldDefinition[] {
  const selected = types.find((type) => type.id === contentTypeId) ?? types.find((type) => type.is_default);
  if (!selected) return fields;
  const hidden = new Set(selected.effective?.hidden_field_ids ?? []);
  const ids = (selected.effective?.field_ids ?? selected.field_ids ?? []).filter((id) => id && !hidden.has(id));
  const byId = new Map(fields.map((field) => [field.id, field]));
  return ids.flatMap((id) => byId.get(id) ?? []);
}

/** Types in tree order, each after its parent, with its depth below the listed roots. */
export function typeTree<T extends { id: string; name: string; parent_id?: string | null }>(types: T[]): { type: T; depth: number }[] {
  const ids = new Set(types.map((type) => type.id));
  const children = new Map<string, T[]>();
  for (const type of types) {
    const parent = type.parent_id && ids.has(type.parent_id) ? type.parent_id : '';
    children.set(parent, [...(children.get(parent) ?? []), type]);
  }
  const out: { type: T; depth: number }[] = [];
  const walk = (parent: string, depth: number) => {
    for (const type of [...(children.get(parent) ?? [])].sort((a, b) => a.name.localeCompare(b.name))) {
      out.push({ type, depth });
      walk(type.id, depth + 1);
    }
  };
  walk('', 0);
  return out;
}

/** A picker label that shows a type's place in its tree. */
export function treeLabel(name: string, depth: number): string {
  return `${'\u00a0\u00a0'.repeat(depth)}${depth ? '└ ' : ''}${name}`;
}

export function publishState(item: Resource, publishingEnabled: boolean): 'Published' | 'Draft' | 'Unpublished' | null {
  if (!publishingEnabled) return null;
  if (!item.published_revision_id) return 'Unpublished';
  if (item.head_revision_id && item.head_revision_id !== item.published_revision_id) return 'Draft';
  return 'Published';
}

const sortableSystemColumns = ['$archived_at', '$archived_by', '$name', '$created_at', '$created_by', '$modified_at', '$modified_by'];

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
