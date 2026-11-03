import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowDown, ArrowUp, Pencil, Plus, X } from 'lucide-react';
import { useState } from 'react';
import { api } from '@/api/http';
import { allPages } from '@/api/pages';
import { keys } from '@/api/queries';
import { useAccess } from './access';
import type { ContentType, FieldDefinition, ListView, NavigationFieldSetting, NavigationMatch, QuerySpec, ViewSettings } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input, Label } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { useTranslation } from '@/i18n';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { columnLabel, systemFields } from './schema';
import { FilterEditor, normalizeFilter } from './filter-editor';

const layouts = ['table', 'board', 'calendar', 'gallery', 'navigation'] as const;
const isLayout = (value: string): value is (typeof layouts)[number] => layouts.some((layout) => layout === value);

/** The most fields a navigation view browses. */
const maxBrowsed = 5;

/** The list with the entry at index moved by the given offset. */
function moved<T>(list: T[], index: number, by: number) {
  const next = [...list];
  const [entry] = next.splice(index, 1);
  next.splice(index + by, 0, entry);
  return next;
}

export function ViewsEditor({
  collectionId,
  fields,
  contentTypes,
}: {
  collectionId: string;
  fields: FieldDefinition[];
  contentTypes: ContentType[];
}) {
  const { t } = useTranslation(['lists', 'common']);
  const client = useQueryClient();
  const { canManage } = useAccess(collectionId);
  const views = useQuery({
    queryKey: keys.views(collectionId),
    queryFn: () => allPages<ListView>(`/v1/resources/${collectionId}/views`),
  });
  const [editing, setEditing] = useState<ListView | 'new'>();
  const [deleting, setDeleting] = useState<ListView>();
  const remove = useMutation({
    mutationFn: (view: ListView) => api(`/v1/views/${view.id}`, { method: 'DELETE', ifMatch: view.version }),
    onSuccess: async () => {
      setDeleting(undefined);
      await client.invalidateQueries({ queryKey: keys.resources });
    },
  });
  return (
    <>
      <SettingsSection
        title={t('views.title')}
        description={t('views.description')}
        actions={
          canManage && (
            <Button variant="primary" onClick={() => setEditing('new')}>
              <Plus /> {t('views.new')}
            </Button>
          )
        }
      >
        <QueryFeedback query={views} />
        {views.isSuccess && views.data.length === 0 && (
          <p className="text-sm text-muted">
            {t('views.none')}
          </p>
        )}
        <ul className="divide-y">
          {views.data?.map((view) => (
            <li key={view.id} className="flex items-center gap-3 py-3">
              <span className="flex-1 font-medium">
                {view.name} <Badge>{view.layout && isLayout(view.layout) ? t(`views.layoutNames.${view.layout}`) : view.layout}</Badge>
                {view.is_default && <Badge className="ml-2">{t('views.default')}</Badge>}
              </span>
              <Button size="sm" variant="ghost" onClick={() => setEditing(view)}>
                <Pencil /> {canManage && !view.system_key ? t('common:actions.edit') : t('views.view')}
              </Button>
              {canManage && !view.system_key && (
                <Button size="sm" variant="ghost" onClick={() => setDeleting(view)}>
                  {t('common:actions.delete')}
                </Button>
              )}
            </li>
          ))}
        </ul>
      </SettingsSection>
      {editing && (
        <ViewDialog
          collectionId={collectionId}
          fields={fields}
          contentTypes={contentTypes}
          view={editing === 'new' ? undefined : editing}
          canManage={canManage && (editing === 'new' || !editing.system_key)}
          onClose={() => setEditing(undefined)}
        />
      )}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(undefined)}
        title={t('general.deleteTitle', { name: deleting?.name ?? '' })}
        description={t('views.deleteDescription')}
        confirm={t('views.delete')}
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </>
  );
}

function ViewDialog({
  collectionId,
  fields,
  contentTypes,
  view,
  canManage,
  onClose,
}: {
  collectionId: string;
  fields: FieldDefinition[];
  contentTypes: ContentType[];
  view?: ListView;
  canManage: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation(['lists', 'common']);
  const client = useQueryClient();
  const [name, setName] = useState(view?.name ?? '');
  const [columns, setColumns] = useState(view?.columns ?? ['$name']);
  const [layout, setLayout] = useState(view?.layout ?? 'table');
  const [isDefault, setIsDefault] = useState(!!view?.is_default);
  const [query, setQuery] = useState<QuerySpec>(view?.query ?? {});
  const [settings, setSettings] = useState<ViewSettings>(view?.settings ?? {});
  const queryable = [
    ...systemFields,
    ...fields.filter((field) => field.indexed && (!field.index_status || field.index_status === 'ready')),
  ];
  const sortable = queryable.filter((field) => field.key !== '$keywords' && !field.options?.multiple);
  const grouping =
    layout === 'board'
      ? sortable.filter((field) => field.type === 'choice')
      : layout === 'calendar'
        ? sortable.filter((field) => ['date', 'datetime'].includes(field.type))
        : sortable;
  // A navigation view browses item keywords or a taxonomy or keywords column as folders.
  const navigable = [
    { key: '$keywords', label: t('columns.keywords'), type: 'keywords', options: { apply_parents: true } } as FieldDefinition,
    ...queryable.filter((field) => field.key !== '$keywords' && (field.type === 'term' || field.type === 'keywords')),
  ];
  const browsed = settings.fields ?? [];
  const setBrowsed = (next: NavigationFieldSetting[]) => setSettings((current) => ({ ...current, fields: next }));
  const validLayout =
    layout === 'navigation'
      ? browsed.length > 0
      : !['board', 'calendar'].includes(layout) || grouping.some((field) => field.key === query.group_by);
  const patchQuery = (patch: Partial<QuerySpec>) => setQuery((current) => ({ ...current, ...patch }));
  const save = useMutation({
    mutationFn: () =>
      api(view ? `/v1/views/${view.id}` : `/v1/resources/${collectionId}/views`, {
        method: view ? 'PUT' : 'POST',
        ifMatch: view?.version,
        body: {
          name: name.trim(),
          columns,
          layout,
          // Matching only combines the terms of several trees.
          settings: layout === 'navigation' ? { ...settings, match: browsed.length > 1 ? settings.match : undefined } : {},
          is_default: isDefault,
          query: { ...query, filter: normalizeFilter(query.filter, queryable) },
        },
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: keys.resources });
      onClose();
    },
  });
  const move = (index: number, by: number) => setColumns((current) => moved(current, index, by));
  return (
    <FormDialog
      open
      title={view ? t('views.viewTitle', { name: view.name }) : t('views.new')}
      submitLabel={t('views.save')}
      submitDisabled={!canManage || columns.length === 0 || !validLayout}
      pending={save.isPending}
      onClose={onClose}
      onSubmit={() => canManage && save.mutate()}
    >
      <fieldset disabled={!canManage} className="grid gap-3">
        <Label htmlFor="view-name">{t('common:fields.name')}</Label>
        <Input id="view-name" required value={name} onChange={(event) => setName(event.target.value)} />
        <Label htmlFor="view-layout">{t('views.layout')}</Label>
        <Select
          id="view-layout"
          value={layout}
          onChange={(event) => {
            setLayout(event.target.value);
            patchQuery({ group_by: undefined });
          }}
        >
          {layouts.map((value) => (
            <option key={value} value={value}>
              {t(`views.layouts.${value}`)}
            </option>
          ))}
        </Select>
        {layout === 'navigation' && (
          <fieldset className="grid gap-2 rounded-lg border p-3">
            <legend className="px-1 font-medium">{t('views.navigation')}</legend>
            <p className="text-xs text-muted">{t('views.navigationHint')}</p>
            <ul aria-label={t('views.browsed')} className="grid gap-2">
              {browsed.map((entry, index) => {
                const field = navigable.find((candidate) => candidate.key === entry.field);
                const label = field?.label ?? entry.field;
                // Items tagged below a term hold the term itself where parents apply, so subterms always count.
                const parentsApply = !!field?.options?.apply_parents;
                const patch = (next: Partial<NavigationFieldSetting>) =>
                  setBrowsed(browsed.map((current, i) => (i === index ? { ...current, ...next } : current)));
                return (
                  <li key={entry.field} aria-label={label} className="grid gap-1 rounded border p-2">
                    <div className="flex items-center gap-2">
                      <span className="flex-1 font-medium">{label}</span>
                      <Button size="icon" aria-label={t('views.up', { name: label })} disabled={index === 0} onClick={() => setBrowsed(moved(browsed, index, -1))}>
                        <ArrowUp />
                      </Button>
                      <Button
                        size="icon"
                        aria-label={t('views.down', { name: label })}
                        disabled={index === browsed.length - 1}
                        onClick={() => setBrowsed(moved(browsed, index, 1))}
                      >
                        <ArrowDown />
                      </Button>
                      <Button size="icon" aria-label={t('common:actions.remove', { name: label })} onClick={() => setBrowsed(browsed.filter((_, i) => i !== index))}>
                        <X />
                      </Button>
                    </div>
                    <label className="flex gap-2">
                      <Checkbox
                        checked={parentsApply || !!entry.include_subterms}
                        disabled={parentsApply}
                        onChange={(event) => patch({ include_subterms: event.target.checked || undefined })}
                      />
                      {t('views.subterms')}
                    </label>
                    <p className="text-xs text-muted">
                      {parentsApply
                        ? t('views.parentsApply', { name: label })
                        : entry.include_subterms
                          ? t('views.withSubterms')
                          : t('views.withoutSubterms')}
                    </p>
                  </li>
                );
              })}
            </ul>
            {browsed.length < maxBrowsed && (
              <Select
                aria-label={t('views.addBrowsed')}
                value=""
                onChange={(event) => event.target.value && setBrowsed([...browsed, { field: event.target.value }])}
              >
                <option value="">{browsed.length ? t('views.addAnother') : t('views.chooseBrowsed')}</option>
                {navigable
                  .filter((field) => !browsed.some((entry) => entry.field === field.key))
                  .map((field) => (
                    <option key={field.key} value={field.key}>
                      {field.label}
                    </option>
                  ))}
              </Select>
            )}
            {browsed.length > 1 && (
              <>
                <Label htmlFor="view-match">{t('views.match')}</Label>
                <Select
                  id="view-match"
                  value={settings.match ?? 'all'}
                  onChange={(event) => setSettings((current) => ({ ...current, match: event.target.value as NavigationMatch }))}
                >
                  <option value="all">{t('views.matchAll')}</option>
                  <option value="any">{t('views.matchAny')}</option>
                </Select>
                <p className="text-xs text-muted">
                  {settings.match === 'any' ? t('views.matchAnyHint') : t('views.matchAllHint')}
                </p>
              </>
            )}
            <label className="flex gap-2">
              <Checkbox
                checked={!!settings.hide_empty}
                onChange={(event) => setSettings((current) => ({ ...current, hide_empty: event.target.checked || undefined }))}
              />
              {t('views.hideEmpty')}
            </label>
          </fieldset>
        )}
        <Label htmlFor="view-archive">{t('views.items')}</Label>
        <Select id="view-archive" value={query.archive ?? 'active'} onChange={event => patchQuery({ archive: event.target.value as QuerySpec['archive'] })}>
          <option value="active">{t('views.archive.active')}</option><option value="archived">{t('views.archive.archived')}</option><option value="all">{t('views.archive.all')}</option>
        </Select>
        <label className="flex gap-2">
          <Checkbox checked={isDefault} onChange={(event) => setIsDefault(event.target.checked)} />
          {t('views.defaultView')}
        </label>
        <fieldset>
          <legend className="font-medium">{t('columnPicker.columns')}</legend>
          <ul>
            {[
              ...columns,
              ...[...systemFields, ...fields].map((field) => field.key).filter((key) => !columns.includes(key)),
            ].map((key) => {
              const index = columns.indexOf(key);
              return (
                <li key={key} className="flex items-center gap-2 py-1">
                  <label className="flex flex-1 gap-2">
                    <Checkbox
                      checked={index >= 0}
                      onChange={(event) =>
                        setColumns((current) =>
                          event.target.checked ? [...current, key] : current.filter((value) => value !== key),
                        )
                      }
                    />
                    {columnLabel(key, fields)}
                  </label>
                  {index >= 0 && (
                    <>
                      <Button
                        size="icon"
                        aria-label={t('views.up', { name: key })}
                        disabled={index === 0}
                        onClick={() => move(index, -1)}
                      >
                        <ArrowUp />
                      </Button>
                      <Button
                        size="icon"
                        aria-label={t('views.down', { name: key })}
                        disabled={index === columns.length - 1}
                        onClick={() => move(index, 1)}
                      >
                        <ArrowDown />
                      </Button>
                    </>
                  )}
                </li>
              );
            })}
          </ul>
        </fieldset>
        <Label htmlFor="view-type">{t('system.contentType')}</Label>
        <Select
          id="view-type"
          value={query.content_type_id ?? ''}
          onChange={(event) => patchQuery({ content_type_id: event.target.value || undefined })}
        >
          <option value="">{t('related.allTypes')}</option>
          {contentTypes.map((type) => (
            <option key={type.id} value={type.id}>
              {type.name}
            </option>
          ))}
        </Select>
        {layout !== 'navigation' && (
          <>
            <Label htmlFor="view-group">{t('views.group')}</Label>
            <Select
              id="view-group"
              value={query.group_by ?? ''}
              onChange={(event) => patchQuery({ group_by: event.target.value || undefined })}
            >
              <option value="">{t('common:select.none')}</option>
              {grouping.map((field) => (
                <option key={field.key} value={field.key}>
                  {field.label}
                </option>
              ))}
            </Select>
          </>
        )}
        <fieldset>
          <legend className="mb-2 font-medium">{t('views.sort')}</legend>
          {(query.sort ?? []).map((sort, index) => (
            <div key={index} className="mb-2 flex gap-2">
              <Select
                aria-label={t('views.sortField', { number: index + 1 })}
                value={sort.field}
                onChange={(event) =>
                  patchQuery({
                    sort: query.sort!.map((current, i) =>
                      i === index ? { ...current, field: event.target.value } : current,
                    ),
                  })
                }
              >
                {sortable.map((field) => (
                  <option key={field.key} value={field.key}>
                    {field.label}
                  </option>
                ))}
              </Select>
              <Select
                aria-label={t('views.sortDirection', { number: index + 1 })}
                value={sort.direction}
                onChange={(event) =>
                  patchQuery({
                    sort: query.sort!.map((current, i) =>
                      i === index ? { ...current, direction: event.target.value } : current,
                    ),
                  })
                }
              >
                <option value="asc">{t('views.ascending')}</option>
                <option value="desc">{t('views.descending')}</option>
              </Select>
              <Button onClick={() => patchQuery({ sort: query.sort!.filter((_, i) => i !== index) })}>
                {t('views.removeSort')}
              </Button>
            </div>
          ))}
          <Button
            onClick={() => patchQuery({ sort: [...(query.sort ?? []), { field: '$modified_at', direction: 'desc' }] })}
          >
            {t('views.addSort')}
          </Button>
        </fieldset>
        <FilterEditor filter={query.filter} fields={queryable} onChange={(filter) => patchQuery({ filter })} />
        <Label htmlFor="view-search">{t('views.search')}</Label>
        <Input
          id="view-search"
          value={query.search ?? ''}
          onChange={(event) => patchQuery({ search: event.target.value || undefined })}
        />
        <Label htmlFor="view-keyword">{t('views.keyword')}</Label>
        <Input
          id="view-keyword"
          value={query.keyword ?? ''}
          onChange={(event) => patchQuery({ keyword: event.target.value || undefined })}
        />
        <Label htmlFor="view-parent">{t('moveDialog.folder')}</Label>
        <Input
          id="view-parent"
          value={query.parent_id ?? ''}
          placeholder={t('views.folderPlaceholder')}
          onChange={(event) => patchQuery({ parent_id: event.target.value || undefined })}
        />
      </fieldset>
    </FormDialog>
  );
}
