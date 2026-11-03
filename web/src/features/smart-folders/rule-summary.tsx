import { useQuery } from '@tanstack/react-query';
import type { TFunction } from 'i18next';
import { api } from '@/api/http';
import { templatesQuery } from '@/api/queries';
import type { FieldDefinition, FilterExpr, Page, SmartFolder } from '@/api/types';
import { useTermNames, useValueNames } from '@/features/fields/names';
import { comparisons, relativeDates, fileTypeField } from '@/features/lists/filter-editor';
import { systemFields } from '@/features/lists/schema';
import { fileTypeLabel } from '@/features/files/file-types';
import { i18n, useTranslation } from '@/i18n';
import type { SmartQueryField } from './editor-dialog';

// The filter editor's comparisons and relative values, in its words.
const relatives = [...relativeDates, 'me'] as const;
const known = <T extends string>(list: readonly T[], value: string | undefined): value is T => list.includes(value as T);

export function describeFilter(filter: FilterExpr | undefined, fields: FieldDefinition[], t: TFunction<['smartFolders', 'common', 'lists']>, names = new Map<string, string>()): string {
  if (!filter) return t('rules.all');
  // Joined as the language joins a list: "(A), (B) and (C)", "(A), (B) y (C)".
  const join = (children: FilterExpr[], type: 'conjunction' | 'disjunction') =>
    new Intl.ListFormat(i18n.language, { type }).format(children.map(child => `(${describeFilter(child, fields, t, names)})`));
  if (filter.and) return join(filter.and, 'conjunction');
  if (filter.or) return join(filter.or, 'disjunction');
  if (filter.not) return t('rules.not', { rule: describeFilter(filter.not, fields, t, names) });
  const field = fields.find(field => field.key === filter.field);
  const label = field?.label ?? t('rules.customField');
  const op = filter.op || 'eq';
  const comparison = known(comparisons, op) ? t(`lists:filter.ops.${op}`) : t('rules.otherComparison');
  if (op === 'missing' || op === 'present') return t('rules.presence', { field: label, comparison });
  const value = (entry: unknown) => {
    if (typeof entry === 'boolean') return entry ? t('common:answer.yes') : t('common:answer.no');
    if (field?.key === '$file_type') return fileTypeLabel(String(entry));
    if (['person', 'lookup', 'term', 'keywords'].includes(field?.type ?? '')) return names.get(String(entry)) ?? t('rules.selectedValue');
    return String(entry ?? '');
  };
  const expected = filter.value_ref ? known(relatives, filter.value_ref) ? t(`lists:filter.relative.${filter.value_ref}`) : t('rules.relativeValue') : Array.isArray(filter.value) ? filter.value.map(value).join(', ') : value(filter.value);
  return t('rules.condition', { field: label, comparison, value: expected });
}

export function RuleSummary({ folder }: { folder: SmartFolder }) {
  const { t } = useTranslation(['smartFolders', 'common', 'lists']);
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
  const keywordNames = (folder.definition.terms ?? []).map(id => terms.get(id) ?? t('rules.selectedKeyword')).join(', ');
  return <section aria-label={t('rules.label')} className="mb-4 rounded-lg border bg-surface p-3 text-sm">
    <h2 className="font-medium">{t('rules.title')}</h2>
    <p>{describeFilter(folder.definition.filter, fields, t, names)}</p>
    {folder.definition.collection_kind && <p className="text-muted">{folder.definition.collection_kind === 'library' ? t('rules.librariesOnly') : t('rules.listsOnly')}</p>}
    {!!folder.definition.collections?.length && <p className="text-muted">{t('rules.collections', { names: folder.definition.collections.join(', ') })}</p>}
    {!!folder.definition.content_types?.length && <p className="text-muted">{t('rules.contentTypes', { names: folder.definition.content_types.map(value => contentTypes.data?.find(type => type.key.toLowerCase() === value.toLowerCase() || type.name.toLowerCase() === value.toLowerCase())?.name ?? t('rules.selectedContentType')).join(', ') })}</p>}
    {!!folder.definition.templates?.length && <p className="text-muted">{t('rules.templates', { names: folder.definition.templates.map(key => templates.data?.find(template => template.key === key)?.name ?? t('rules.selectedTemplate')).join(', ') })}</p>}
    {!!folder.definition.terms?.length && <p className="text-muted">{folder.definition.term_match === 'any' ? t('rules.anyKeywords', { names: keywordNames }) : t('rules.allKeywords', { names: keywordNames })}</p>}
    {folder.definition.mode === 'classification' && <p className="text-muted">{t('rules.classifying')}</p>}
  </section>;
}
