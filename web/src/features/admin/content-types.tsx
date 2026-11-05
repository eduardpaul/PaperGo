import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { Plus } from 'lucide-react';
import { contentTypesQuery, fieldsQuery, keys } from '@/api/queries';
import type { ContentType, FieldDefinition, FieldSetting, Resource, ValidationRule } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { JsonInput } from '@/features/workflows/schema-form';
import { Field, QueryFeedback, useHasScope } from './common';
import { SystemProperties } from '@/features/lists/system-properties';
import { reorder, treeLabel, typeTree, withSetting } from '@/features/lists/schema';
import { FieldEditor } from '@/features/fields/editor';
import { FieldDialog, fieldTypeLabel } from './field-dialog';
import { useTranslation } from '@/i18n';

/** Catalog types have at most this many ancestors; list types take one more level. */
const maxCatalogAncestors = 7;

export const catalogTypesQuery = {
  queryKey: ['catalog', 'content-types'],
  queryFn: async () => (await api<ContentType[]>('/v1/content-types')).data,
};
export const catalogFieldsQuery = {
  queryKey: ['catalog', 'fields'],
  queryFn: async () => (await api<FieldDefinition[]>('/v1/fields')).data,
};

const NO_GROUP = '\u0000none';

export function ContentTypeCatalog() {
  const { t } = useTranslation(['admin', 'common']);
  const canRead = useHasScope('contentType.read');
  const canManage = useHasScope('contentType.manage');
  const types = useQuery({ ...catalogTypesQuery, enabled: canRead });
  const fields = useQuery({ ...catalogFieldsQuery, enabled: canRead });
  const [type, setType] = useState<ContentType | 'new'>();
  const [field, setField] = useState<FieldDefinition | 'new'>();
  const [deleting, setDeleting] = useState<ContentType>();
  // '' shows every type, NO_GROUP those without a group.
  const [shownGroup, setShownGroup] = useState('');
  const groups = [...new Set((types.data ?? []).map((entry) => entry.group).filter((entry): entry is string => !!entry))].sort();
  const shown = (types.data ?? []).filter((entry) => !shownGroup || (shownGroup === NO_GROUP ? !entry.group : entry.group === shownGroup));
  const client = useQueryClient();
  const remove = useMutation({
    mutationFn: (type: ContentType) => api(`/v1/content-types/${type.id}`, { method: 'DELETE', ifMatch: type.version }),
    onSuccess: async () => {
      setDeleting(undefined);
      await client.invalidateQueries({ queryKey: ['catalog'] });
    },
  });
  if (!canRead) return <p>{t('contentTypes.needRead')}</p>;
  return (
    <div className="grid gap-5">
      <SettingsSection
        title={t('contentTypes.title')}
        description={t('contentTypes.description')}
        actions={canManage && <Button onClick={() => setType('new')}>{t('contentTypes.new')}</Button>}
      >
        <QueryFeedback query={types} />
        {groups.length > 0 && (
          <Field id="content-type-group-filter" label={t('contentTypes.group')}>
            <Select id="content-type-group-filter" value={shownGroup} onChange={(event) => setShownGroup(event.target.value)}>
              <option value="">{t('contentTypes.allGroups')}</option>
              {groups.map((entry) => (
                <option key={entry} value={entry}>
                  {entry}
                </option>
              ))}
              <option value={NO_GROUP}>{t('contentTypes.noGroup')}</option>
            </Select>
          </Field>
        )}
        <ul className="divide-y">
          {typeTree(shown).map(({ type, depth }) => (
            <li key={type.id} className="flex items-center gap-2 py-3" style={{ paddingInlineStart: `${depth * 1.25}rem` }}>
              <span className="flex-1 font-medium">
                {type.name}{' '}
                {type.group && <Badge>{type.group}</Badge>}{' '}
                <span className="text-xs text-muted">
                  {type.key} · {t('contentTypes.fieldCount', { count: type.effective?.field_ids.length ?? type.field_ids?.length ?? 0 })}
                  {(type.field_ids?.length ?? 0) !== (type.effective?.field_ids.length ?? 0) && ` ${t('contentTypes.ownCount', { count: type.field_ids?.length ?? 0 })}`}
                </span>
              </span>
              <Button onClick={() => setType(type)}>{canManage ? t('common:actions.edit') : t('contentTypes.view')}</Button>
              {canManage && type.parent_id && (
                <Button variant="ghost" onClick={() => setDeleting(type)}>
                  {t('common:actions.delete')}
                </Button>
              )}
            </li>
          ))}
        </ul>
      </SettingsSection>
      <SettingsSection
        title={t('contentTypes.fieldCatalog')}
        description={t('contentTypes.fieldCatalogDescription')}
        actions={canManage && <Button onClick={() => setField('new')}>{t('contentTypes.newField')}</Button>}
      >
        <QueryFeedback query={fields} />
        <FieldList fields={fields.data ?? []} canManage={canManage} onEdit={setField} />
      </SettingsSection>
      {type && (
        <TypeDialog
          type={type === 'new' ? undefined : type}
          types={types.data ?? []}
          fields={fields.data ?? []}
          canManage={canManage}
          onClose={() => setType(undefined)}
        />
      )}
      {field && (
        <FieldDialog
          field={field === 'new' ? undefined : field}
          canManage={canManage}
          onClose={() => setField(undefined)}
        />
      )}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(undefined)}
        title={t('contentTypes.deleteTitle', { name: deleting?.name ?? '' })}
        description={t('contentTypes.deleteDescription')}
        confirm={t('contentTypes.deleteConfirm')}
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </div>
  );
}

/** What a type defines itself, against what its parent gives it. */
interface Schema {
  ids: string[];
  hidden: string[];
  rules: ValidationRule[];
  order: string[];
  settings: FieldSetting[];
}

const schemaOf = (type?: ContentType): Schema => ({
  ids: type?.field_ids ?? [],
  hidden: type?.hidden_field_ids ?? [],
  rules: type?.rules ?? [],
  order: type?.field_order ?? [],
  settings: type?.field_settings ?? [],
});

/** The type's fields in its form order: inherited ones, then its own. */
const schemaFieldIds = (schema: Schema, parent?: ContentType) => {
  const inherited = parent?.effective?.field_ids ?? [];
  return reorder([...inherited, ...schema.ids.filter((id) => !inherited.includes(id))], schema.order);
};

/** A type's write body: what it defines itself, for the fields it has. */
const schemaBody = (schema: Schema, parent?: ContentType) => {
  const all = schemaFieldIds(schema, parent);
  return {
    field_ids: schema.ids.filter((id) => !parent?.effective?.field_ids.includes(id)),
    hidden_field_ids: schema.hidden,
    rules: schema.rules,
    field_order: schema.order.filter((id) => all.includes(id)),
    field_settings: schema.settings.filter((setting) => all.includes(setting.field_id)),
  };
};

/**
 * Edits a type's own fields, hidden inherited fields and own rules. Inherited
 * fields stay; a required field without a default stays visible.
 */
function SchemaFields({
  fields,
  parent,
  schema,
  onChange,
}: {
  fields: FieldDefinition[];
  parent?: ContentType;
  schema: Schema;
  onChange: (schema: Schema) => void;
}) {
  const { t } = useTranslation('admin');
  const inherited = parent?.effective?.field_ids ?? [];
  const inheritedHidden = new Set(parent?.effective?.hidden_field_ids ?? []);
  const byId = new Map(fields.map((field) => [field.id, field]));
  // A field as the type inherits it, and as its own setting changes it.
  const inheritedField = (field: FieldDefinition) =>
    withSetting(field, parent?.effective?.field_settings?.find((setting) => setting.field_id === field.id));
  const ownSetting = (id: string) => schema.settings.find((setting) => setting.field_id === id);
  const all = schemaFieldIds(schema, parent);
  const layout = all.flatMap((id) => byId.get(id) ?? []);
  // Fields given an own default that has no value yet.
  const [drafts, setDrafts] = useState<string[]>([]);
  const setSetting = (id: string, change: Partial<FieldSetting>) => {
    const next: FieldSetting = { ...ownSetting(id), ...change, field_id: id };
    if (next.required === undefined) delete next.required;
    if (next.default_value === undefined) delete next.default_value;
    const others = schema.settings.filter((setting) => setting.field_id !== id);
    onChange({ ...schema, settings: Object.keys(next).length > 1 ? [...others, next] : others });
  };
  // Swaps a shown field with its neighbour. The type orders only the fields
  // up to the last one out of inherited order, so the rest keep following
  // what it inherits.
  const move = (index: number, by: number) => {
    const next = [...all];
    const [from, to] = [next.indexOf(layout[index].id), next.indexOf(layout[index + by].id)];
    [next[from], next[to]] = [next[to], next[from]];
    const inherited = schemaFieldIds({ ...schema, order: [] }, parent);
    let length = 0;
    while (reorder(inherited, next.slice(0, length)).some((id, i) => id !== next[i])) length++;
    onChange({ ...schema, order: next.slice(0, length) });
  };
  const label = (field: FieldDefinition) => (
    <>
      {field.label} <span className="text-xs text-muted">{fieldTypeLabel(field.type)}</span>
    </>
  );
  return (
    <>
      {parent && (
        <fieldset>
          <legend>{t('contentTypes.inheritedFrom', { name: parent.name })}</legend>
          {inherited.length === 0 && <p className="text-sm text-muted">{t('contentTypes.noFields', { name: parent.name })}</p>}
          {inherited.flatMap((id) => byId.get(id) ?? []).map((field) => {
            const shown = !inheritedHidden.has(field.id) && !schema.hidden.includes(field.id);
            // A field forms must fill cannot be hidden, but can always be shown again.
            const effective = withSetting(inheritedField(field), ownSetting(field.id));
            const mandatory = effective.required && effective.options?.default_value === undefined;
            return (
              <label key={field.id} className="flex gap-2 py-1">
                <Checkbox
                  aria-label={t('contentTypes.showInForms', { label: field.label })}
                  disabled={inheritedHidden.has(field.id) || (shown && mandatory)}
                  checked={shown}
                  onChange={(event) =>
                    onChange({
                      ...schema,
                      hidden: event.target.checked ? schema.hidden.filter((id) => id !== field.id) : [...schema.hidden, field.id],
                    })
                  }
                />
                {label(field)}
                <span className="text-xs text-muted">{shown ? t('contentTypes.shownInForms') : t('contentTypes.hiddenInForms')}</span>
              </label>
            );
          })}
        </fieldset>
      )}
      <fieldset>
        <legend>{parent ? t('contentTypes.ownFields') : t('contentTypes.fields')}</legend>
        {fields
          .filter((field) => !inherited.includes(field.id))
          .map((field) => (
            <label key={field.id} className="flex gap-2 py-1">
              <Checkbox
                checked={schema.ids.includes(field.id)}
                onChange={(event) =>
                  onChange({
                    ...schema,
                    ids: event.target.checked ? [...schema.ids, field.id] : schema.ids.filter((id) => id !== field.id),
                  })
                }
              />
              {label(field)}
            </label>
          ))}
      </fieldset>
      {all.length > 0 && (
        <fieldset>
          <legend>{t('contentTypes.formLayout')}</legend>
          <p className="text-xs text-muted">{t('contentTypes.formLayoutHint')}</p>
          {layout.map((field, index) => {
            const setting = ownSetting(field.id);
            const base = inheritedField(field);
            const ownDefault = drafts.includes(field.id) || (setting?.default_value !== undefined && setting.default_value !== null);
            return (
              <div key={field.id} className="grid gap-1 border-b py-2 last:border-b-0">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="flex-1 text-sm">{label(field)}</span>
                  <Button size="sm" variant="ghost" aria-label={t('contentTypes.moveUp', { label: field.label })} disabled={index === 0} onClick={() => move(index, -1)}>
                    ↑
                  </Button>
                  <Button size="sm" variant="ghost" aria-label={t('contentTypes.moveDown', { label: field.label })} disabled={index === layout.length - 1} onClick={() => move(index, 1)}>
                    ↓
                  </Button>
                  <Select
                    aria-label={t('contentTypes.requiredLabel', { label: field.label })}
                    className="w-auto"
                    value={setting?.required === undefined ? '' : setting.required ? 'required' : 'optional'}
                    onChange={(event) => setSetting(field.id, { required: event.target.value ? event.target.value === 'required' : undefined })}
                  >
                    <option value="">{base.required ? t('contentTypes.requiredInherited') : t('contentTypes.optionalInherited')}</option>
                    <option value="required">{t('contentTypes.required')}</option>
                    <option value="optional">{t('contentTypes.optional')}</option>
                  </Select>
                  <Select
                    aria-label={t('contentTypes.defaultLabel', { label: field.label })}
                    className="w-auto"
                    value={setting?.default_value === null ? 'none' : ownDefault ? 'own' : ''}
                    onChange={(event) => {
                      // An own default starts from the inherited one, or empty
                      // until a value is entered.
                      const choice = event.target.value;
                      setDrafts(choice === 'own' ? [...drafts, field.id] : drafts.filter((id) => id !== field.id));
                      setSetting(field.id, { default_value: choice === 'own' ? base.options?.default_value : choice === 'none' ? null : undefined });
                    }}
                  >
                    <option value="">{base.options?.default_value !== undefined ? t('contentTypes.inheritedDefault') : t('contentTypes.noDefaultInherited')}</option>
                    <option value="own">{t('contentTypes.ownDefault')}</option>
                    <option value="none">{t('contentTypes.noDefault')}</option>
                  </Select>
                </div>
                {ownDefault && (
                  <FieldEditor
                    id={`default-${field.key}`}
                    field={{ ...field, label: t('contentTypes.defaultField', { label: field.label }), required: false }}
                    value={setting?.default_value ?? undefined}
                    onChange={(value) => setSetting(field.id, { default_value: value ?? undefined })}
                  />
                )}
              </div>
            );
          })}
        </fieldset>
      )}
      <p className="text-sm font-medium">{t('contentTypes.rules')}</p>
      {!!parent?.effective?.rules.length && (
        <p className="text-sm text-muted">{t('contentTypes.inheritedRules', { count: parent.effective.rules.length })}</p>
      )}
      <JsonInput
        label={t('contentTypes.rulesLabel')}
        value={schema.rules}
        onChange={(value) => {
          if (!Array.isArray(value)) throw new Error(t('contentTypes.rulesNotArray'));
          onChange({ ...schema, rules: value as ValidationRule[] });
        }}
      />
    </>
  );
}

function TypeDialog({
  type,
  types,
  fields,
  canManage,
  onClose,
}: {
  type?: ContentType;
  types: ContentType[];
  fields: FieldDefinition[];
  canManage: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation(['admin', 'common']);
  const client = useQueryClient();
  const root = types.find((entry) => !entry.parent_id);
  const [key, setKey] = useState(type?.key ?? '');
  const [name, setName] = useState(type?.name ?? '');
  const [group, setGroup] = useState(type?.group ?? '');
  const groups = [...new Set(types.map((entry) => entry.group).filter((entry): entry is string => !!entry))].sort();
  const [parentId, setParentId] = useState(type ? (type.parent_id ?? '') : (root?.id ?? ''));
  const [schema, setSchema] = useState<Schema>(schemaOf(type));
  const parent = types.find((entry) => entry.id === parentId);
  // An existing type keeps its parent, shown with its ancestry. A new type
  // derives from a type that leaves it at most maxCatalogAncestors ancestors.
  const parents = type
    ? types
        .filter((entry) => entry.id === type.parent_id)
        .map((entry) => ({
          id: entry.id,
          label: [...(entry.ancestor_ids ?? [])].reverse().map((id) => types.find((other) => other.id === id)?.name ?? id).concat(entry.name).join(' › '),
        }))
    : typeTree(types)
        .filter(({ type: entry }) => (entry.ancestor_ids?.length ?? 0) < maxCatalogAncestors)
        .map(({ type: entry, depth }) => ({ id: entry.id, label: treeLabel(entry.name, depth) }));
  const save = useMutation({
    mutationFn: () =>
      api(type ? `/v1/content-types/${type.id}` : '/v1/content-types', {
        method: type ? 'PUT' : 'POST',
        ifMatch: type?.version,
        body: {
          key: key.trim(),
          name: name.trim(),
          group: group.trim() || undefined,
          parent_id: type ? undefined : parentId || undefined,
          ...schemaBody(schema, parent),
        },
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['catalog'] });
      await client.invalidateQueries({ queryKey: keys.resources });
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title={type ? t('contentTypes.typeTitle', { name: type.name }) : t('contentTypes.new')}
      description={t('contentTypes.typeDescription')}
      submitLabel={t('contentTypes.save')}
      submitDisabled={!canManage}
      pending={save.isPending}
      onClose={onClose}
      onSubmit={() => canManage && save.mutate()}
    >
      <fieldset className="grid gap-3" disabled={!canManage}>
        <Field id="content-type-key" label={t('contentTypes.key')}>
          <Input
            id="content-type-key"
            disabled={!!type}
            required
            pattern="[a-z][a-z0-9_]*"
            value={key}
            onChange={(event) => setKey(event.target.value)}
          />
        </Field>
        <Field id="content-type-name" label={t('common:fields.name')}>
          <Input id="content-type-name" required value={name} onChange={(event) => setName(event.target.value)} />
        </Field>
        <Field id="content-type-group" label={t('contentTypes.group')}>
          <Input id="content-type-group" list="content-type-groups" maxLength={100} value={group} onChange={(event) => setGroup(event.target.value)} />
          <datalist id="content-type-groups">
            {groups.map((entry) => (
              <option key={entry} value={entry} />
            ))}
          </datalist>
        </Field>
        {(!type || type.parent_id) && (
          <Field id="content-type-parent" label={t('contentTypes.derivesFrom')}>
            <Select
              id="content-type-parent"
              disabled={!!type}
              value={parentId}
              onChange={(event) => {
                // Own fields and hidden fields are relative to the parent.
                const next = types.find((entry) => entry.id === event.target.value)?.effective;
                setParentId(event.target.value);
                setSchema((current) => ({
                  ...current,
                  ids: current.ids.filter((id) => !next?.field_ids.includes(id)),
                  hidden: current.hidden.filter((id) => next?.field_ids.includes(id) && !next.hidden_field_ids.includes(id)),
                }));
              }}
            >
              {parents.map((entry) => (
                <option key={entry.id} value={entry.id}>
                  {entry.label}
                </option>
              ))}
            </Select>
          </Field>
        )}
        <SchemaFields fields={fields} parent={parent} schema={schema} onChange={setSchema} />
      </fieldset>
    </FormDialog>
  );
}

/** Customizes a list type: own columns, hidden inherited ones and own rules. */
function ListTypeDialog({
  collection,
  type,
  canManage,
  onClose,
}: {
  collection: Resource;
  type: ContentType;
  canManage: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation(['admin', 'common']);
  const client = useQueryClient();
  const catalog = useQuery(catalogTypesQuery);
  const fields = useQuery(catalogFieldsQuery);
  const parent = catalog.data?.find((entry) => entry.id === type.parent_id);
  const [schema, setSchema] = useState<Schema>(schemaOf(type));
  const save = useMutation({
    mutationFn: () =>
      api(`/v1/resources/${collection.id}/content-types/${type.id}`, {
        method: 'PUT',
        ifMatch: type.version,
        body: schemaBody(schema, parent),
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: keys.resources });
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title={t('contentTypes.customizeTitle', { type: type.name, collection: collection.name })}
      description={t('contentTypes.customizeDescription', { type: type.name })}
      submitLabel={t('common:actions.save')}
      submitDisabled={!canManage}
      pending={save.isPending}
      onClose={onClose}
      onSubmit={() => canManage && save.mutate()}
    >
      <QueryFeedback query={catalog} />
      <fieldset className="grid gap-3" disabled={!canManage}>
        {parent && <SchemaFields fields={fields.data ?? []} parent={parent} schema={schema} onChange={setSchema} />}
      </fieldset>
    </FormDialog>
  );
}

function FieldList({
  fields,
  canManage,
  onEdit,
  collection,
}: {
  fields: FieldDefinition[];
  canManage: boolean;
  onEdit: (field: FieldDefinition) => void;
  collection?: Resource;
}) {
  const { t } = useTranslation(['admin', 'common']);
  const client = useQueryClient();
  const [deleting, setDeleting] = useState<FieldDefinition>();
  const remove = useMutation({
    mutationFn: (field: FieldDefinition) =>
      api(collection ? `/v1/resources/${collection.id}/fields/${field.id}` : `/v1/fields/${field.id}`, {
        method: 'DELETE',
        ifMatch: collection?.version ?? field.version,
      }),
    onSuccess: async () => {
      setDeleting(undefined);
      await client.invalidateQueries({ queryKey: keys.resources });
      await client.invalidateQueries({ queryKey: ['catalog'] });
    },
  });
  return (
    <>
      <ul className="divide-y">
        {fields.map((field) => (
          <li key={field.id} className="flex items-center gap-2 py-2">
            <span className="flex-1">
              <span className="font-medium">{field.label}</span>
              <span className="block text-xs text-muted">
                {[
                  collection ? '' : field.key,
                  fieldTypeLabel(field.type),
                  field.required ? t('contentTypes.required') : '',
                  field.indexed
                    ? field.index_status && field.index_status !== 'ready'
                      ? t('contentTypes.preparingFilters')
                      : t('contentTypes.filterable')
                    : '',
                ]
                  .filter(Boolean)
                  .join(' · ')}
              </span>
            </span>
            <Button size="sm" variant="ghost" onClick={() => onEdit(field)}>
              {canManage ? t('common:actions.edit') : t('contentTypes.view')}
            </Button>
            {canManage && (
              <Button size="sm" variant="ghost" onClick={() => setDeleting(field)}>
                {collection ? t('common:actions.removeItem') : t('common:actions.delete')}
              </Button>
            )}
          </li>
        ))}
      </ul>
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(undefined)}
        title={
          collection
            ? t('contentTypes.removeColumnTitle', { label: deleting?.label ?? '' })
            : t('contentTypes.deleteFieldTitle', { label: deleting?.label ?? '' })
        }
        description={
          collection
            ? t('contentTypes.removeColumnDescription')
            : t('contentTypes.deleteFieldDescription')
        }
        confirm={collection ? t('contentTypes.removeColumn') : t('contentTypes.deleteSharedField')}
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </>
  );
}

/** The columns of a list or library: shared organization fields attached through its content types. */
export function CollectionColumns({ collection, canManage }: { collection: Resource; canManage: boolean }) {
  const { t } = useTranslation('admin');
  const fields = useQuery(fieldsQuery(collection.id));
  const [editing, setEditing] = useState<FieldDefinition | 'new'>();
  return (
    <SettingsSection
      title={t('contentTypes.columns')}
      description={t('contentTypes.columnsDescription')}
      actions={
        canManage && (
          <Button variant="primary" onClick={() => setEditing('new')}>
            <Plus /> {t('contentTypes.addColumn')}
          </Button>
        )
      }
    >
      <QueryFeedback query={fields} />
      <SystemProperties />
      <h3 className="mt-3 font-medium">{t('contentTypes.customFields')}</h3>
      <FieldList collection={collection} fields={fields.data ?? []} canManage={canManage} onEdit={setEditing} />
      {fields.isSuccess && fields.data.length === 0 && (
        <p className="text-sm text-muted">{t('contentTypes.noCustomFields')}</p>
      )}
      {editing && (
        <FieldDialog
          field={editing === 'new' ? undefined : editing}
          collection={collection}
          canManage={canManage}
          onClose={() => setEditing(undefined)}
        />
      )}
    </SettingsSection>
  );
}

/** Content types attached to a list or library; each item uses one and gets its columns. */
export function CollectionContentTypes({ collection, canManage }: { collection: Resource; canManage: boolean }) {
  const { t } = useTranslation(['admin', 'common']);
  const client = useQueryClient();
  const canReadCatalog = useHasScope('contentType.read');
  const canManageTypes = useHasScope('contentType.manage');
  const catalog = useQuery({ ...catalogTypesQuery, enabled: canReadCatalog });
  const types = useQuery(contentTypesQuery(collection.id));
  const [attachId, setAttachId] = useState('');
  const [makeDefault, setMakeDefault] = useState(false);
  const [detaching, setDetaching] = useState<ContentType>();
  const [customizing, setCustomizing] = useState<ContentType>();
  const refresh = async () => {
    await client.invalidateQueries({ queryKey: keys.resources });
  };
  const attach = useMutation({
    mutationFn: ({ id, isDefault }: { id: string; isDefault: boolean }) =>
      api(`/v1/resources/${collection.id}/content-types`, {
        method: 'POST',
        ifMatch: collection.version,
        body: { content_type_id: id, is_default: isDefault },
      }),
    onSuccess: async () => {
      setAttachId('');
      await refresh();
    },
  });
  const detach = useMutation({
    mutationFn: (type: ContentType) =>
      api(`/v1/resources/${collection.id}/content-types/${type.id}`, { method: 'DELETE', ifMatch: collection.version }),
    onSuccess: async () => {
      setDetaching(undefined);
      await refresh();
    },
  });
  // The New menu offers these list types, in order.
  const menu = (types.data ?? []).filter((type) => type.in_new_menu !== false).map((type) => type.id);
  const setMenu = useMutation({
    mutationFn: (ids: string[]) =>
      api(`/v1/resources/${collection.id}/content-type-menu`, { method: 'PUT', ifMatch: collection.version, body: { content_type_ids: ids } }),
    onSuccess: refresh,
  });
  const move = (id: string, by: number) => {
    const next = [...menu];
    const at = next.indexOf(id);
    next.splice(at, 1);
    next.splice(at + by, 0, id);
    setMenu.mutate(next);
  };
  // Each attached list type copies one catalog type.
  const available = typeTree(catalog.data?.filter((type) => !types.data?.some((attached) => attached.parent_id === type.id)) ?? []);
  return (
    <SettingsSection
      title={t('contentTypes.title')}
      description={t('contentTypes.listDescription')}
    >
      <QueryFeedback query={types} />
      <QueryFeedback query={setMenu} />
      <ul className="divide-y">
        {types.data?.map((type) => (
          <li key={type.id} className="flex flex-wrap items-center gap-2 py-2">
            <span className="min-w-40 flex-1 text-[13px] font-medium">
              {type.name} {type.is_default && <Badge>{t('contentTypes.default')}</Badge>}
              {type.in_new_menu === false && <span className="ms-2 text-xs text-muted">{t('contentTypes.notInNewMenu')}</span>}
              {(!!type.field_ids?.length || !!type.hidden_field_ids?.length || !!type.rules?.length) && (
                <span className="ms-2 text-xs text-muted">{t('contentTypes.customized')}</span>
              )}
            </span>
            {canManage && (
              <>
                {(types.data?.length ?? 0) > 1 && (
                  <>
                    <label className="flex items-center gap-1 text-xs">
                      <Checkbox
                        aria-label={t('contentTypes.offerInNewMenu', { name: type.name })}
                        checked={type.in_new_menu !== false}
                        disabled={type.is_default || setMenu.isPending}
                        onChange={(event) => setMenu.mutate(event.target.checked ? [...menu, type.id] : menu.filter((id) => id !== type.id))}
                      />
                      {t('contentTypes.newMenu')}
                    </label>
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-label={t('contentTypes.moveUp', { label: type.name })}
                      disabled={menu.indexOf(type.id) <= 0 || setMenu.isPending}
                      onClick={() => move(type.id, -1)}
                    >
                      ↑
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-label={t('contentTypes.moveDown', { label: type.name })}
                      disabled={!menu.includes(type.id) || menu.indexOf(type.id) === menu.length - 1 || setMenu.isPending}
                      onClick={() => move(type.id, 1)}
                    >
                      ↓
                    </Button>
                  </>
                )}
                {canReadCatalog && (
                  <Button size="sm" variant="ghost" onClick={() => setCustomizing(type)}>
                    {canManageTypes ? t('contentTypes.customize') : t('contentTypes.viewType')}
                  </Button>
                )}
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={type.is_default || attach.isPending || !type.parent_id}
                  onClick={() => type.parent_id && attach.mutate({ id: type.parent_id, isDefault: true })}
                >
                  {t('contentTypes.makeDefault')}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={(types.data?.length ?? 0) <= 1}
                  onClick={() => setDetaching(type)}
                >
                  {t('common:actions.removeItem')}
                </Button>
              </>
            )}
          </li>
        ))}
      </ul>
      {canManage && canReadCatalog && available.length > 0 && (
        <div className="mt-3 flex flex-col gap-2 border-t pt-3 sm:flex-row sm:items-center">
          <Select
            aria-label={t('contentTypes.addContentType')}
            className="sm:max-w-xs"
            value={attachId}
            onChange={(event) => setAttachId(event.target.value)}
          >
            <option value="">{t('contentTypes.addContentTypePlaceholder')}</option>
            {available.map(({ type, depth }) => (
              <option key={type.id} value={type.id}>
                {type.group
                  ? t('contentTypes.typeInGroup', { type: treeLabel(type.name, depth), group: type.group })
                  : treeLabel(type.name, depth)}
              </option>
            ))}
          </Select>
          <label className="flex items-center gap-2 text-[13px]">
            <Checkbox checked={makeDefault} onChange={(event) => setMakeDefault(event.target.checked)} />
            {t('contentTypes.makeDefault')}
          </label>
          <Button
            disabled={!attachId || attach.isPending}
            onClick={() => attach.mutate({ id: attachId, isDefault: makeDefault })}
          >
            {t('contentTypes.add')}
          </Button>
        </div>
      )}
      <ConfirmDialog
        open={!!detaching}
        onOpenChange={(open) => !open && setDetaching(undefined)}
        title={t('contentTypes.detachTitle', { name: detaching?.name ?? '' })}
        description={t('contentTypes.detachDescription')}
        confirm={t('contentTypes.detachConfirm')}
        busy={detach.isPending}
        error={detach.error}
        onConfirm={() => detaching && detach.mutate(detaching)}
      />
      {customizing && (
        <ListTypeDialog collection={collection} type={customizing} canManage={canManage && canManageTypes} onClose={() => setCustomizing(undefined)} />
      )}
    </SettingsSection>
  );
}
