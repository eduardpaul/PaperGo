import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/http';
import { templatesQuery } from '@/api/queries';
import type { FieldDefinition, FilterExpr, Page, SmartFolder } from '@/api/types';
import { useTermNames, useValueNames } from '@/features/fields/names';
import { comparisonLabels, relativeLabels, systemFields, fileTypeField } from '@/features/lists/filter-editor';
import { fileTypeLabel } from '@/features/files/file-types';
import type { SmartQueryField } from './editor-dialog';

export function describeFilter(filter: FilterExpr | undefined, fields: FieldDefinition[], names = new Map<string, string>()): string {
  if (!filter) return 'All visible items';
  if (filter.and) return filter.and.map(child => `(${describeFilter(child, fields, names)})`).join(' and ');
  if (filter.or) return filter.or.map(child => `(${describeFilter(child, fields, names)})`).join(' or ');
  if (filter.not) return `Does not match (${describeFilter(filter.not, fields, names)})`;
  const field = fields.find(field => field.key === filter.field);
  const label = field?.label ?? 'Custom field';
  const op = filter.op || 'eq';
  if (op === 'missing' || op === 'present') return `${label} ${comparisonLabels[op]}`;
  const value = (entry: unknown) => {
    if (typeof entry === 'boolean') return entry ? 'Yes' : 'No';
    if (field?.key === '$file_type') return fileTypeLabel(String(entry));
    if (['person', 'lookup', 'term', 'keywords'].includes(field?.type ?? '')) return names.get(String(entry)) ?? 'Selected value';
    return String(entry ?? '');
  };
  const expected = filter.value_ref ? relativeLabels[filter.value_ref] ?? 'Relative value' : Array.isArray(filter.value) ? filter.value.map(value).join(', ') : value(filter.value);
  return `${label} ${comparisonLabels[op] ?? 'matches'} ${expected}`;
}

export function RuleSummary({ folder }: { folder: SmartFolder }) {
  const metadata = useQuery({ queryKey: ['resources', 'smart-folder-fields', folder.workspace_id, undefined], queryFn: async () => (await api<Page<SmartQueryField>>('/v1/search/fields', { query: { workspace_id: folder.workspace_id } })).data.value });
  const fields: FieldDefinition[] = [...systemFields, fileTypeField(), ...(metadata.data ?? []).map(field => ({ id: field.key, key: field.key, label: field.label, type: field.type, options: { term_set_id: field.term_set_id, lookup_container_id: field.lookup_container_id } }))];
  const values: Record<string, unknown[]> = {};
  const visit = (filter?: FilterExpr) => {
    if (!filter) return;
    for (const child of filter.and ?? filter.or ?? (filter.not ? [filter.not] : [])) visit(child);
    if (filter.field) values[filter.field] = [...(values[filter.field] ?? []), ...(Array.isArray(filter.value) ? filter.value : [filter.value]).filter(value => value !== undefined)];
  };
  visit(folder.definition.filter);
  const names = useValueNames(fields, [{ values }]);
  const terms = useTermNames(folder.definition.terms ?? []);
  const contentTypes = useQuery({ queryKey: ['search', 'content-types', folder.workspace_id, undefined], queryFn: async () => (await api<{ key: string; name: string }[]>('/v1/search/content-types', { query: { workspace_id: folder.workspace_id } })).data });
  const templates = useQuery({ ...templatesQuery, enabled: !!folder.definition.templates?.length });
  return <section aria-label="Rules" className="mb-4 rounded-lg border bg-surface p-3 text-sm">
    <h2 className="font-medium">Items matching these rules appear automatically</h2>
    <p>{describeFilter(folder.definition.filter, fields, names)}</p>
    {folder.definition.collection_kind && <p className="text-muted">{folder.definition.collection_kind === 'library' ? 'Libraries' : 'Lists'} only</p>}
    {!!folder.definition.collections?.length && <p className="text-muted">Collections: {folder.definition.collections.join(', ')}</p>}
    {!!folder.definition.content_types?.length && <p className="text-muted">Content types: {folder.definition.content_types.map(value => contentTypes.data?.find(type => type.key.toLowerCase() === value.toLowerCase() || type.name.toLowerCase() === value.toLowerCase())?.name ?? 'Selected content type').join(', ')}</p>}
    {!!folder.definition.templates?.length && <p className="text-muted">Templates: {folder.definition.templates.map(key => templates.data?.find(template => template.key === key)?.name ?? 'Selected template').join(', ')}</p>}
    {!!folder.definition.terms?.length && <p className="text-muted">{folder.definition.term_match === 'any' ? 'Any' : 'All'} keywords: {folder.definition.terms.map(id => terms.get(id) ?? 'Selected keyword').join(', ')}</p>}
    {folder.definition.mode === 'classification' && <p className="text-muted">Classifying applies the folder’s keywords and equality values. Other rules must already match.</p>}
  </section>;
}
