import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { Plus } from 'lucide-react';
import { contentTypesQuery, fieldsQuery, keys } from '@/api/queries';
import type { ContentType, FieldDefinition, Resource, ValidationRule } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { JsonInput } from '@/features/workflows/schema-form';
import { Field, QueryFeedback, useHasScope } from './common';
import { SystemProperties } from '@/features/lists/system-properties';
import { treeLabel, typeTree } from '@/features/lists/schema';
import { FieldDialog, fieldTypeLabel } from './field-dialog';

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

export function ContentTypeCatalog() {
  const canRead = useHasScope('contentType.read');
  const canManage = useHasScope('contentType.manage');
  const types = useQuery({ ...catalogTypesQuery, enabled: canRead });
  const fields = useQuery({ ...catalogFieldsQuery, enabled: canRead });
  const [type, setType] = useState<ContentType | 'new'>();
  const [field, setField] = useState<FieldDefinition | 'new'>();
  const [deleting, setDeleting] = useState<ContentType>();
  const client = useQueryClient();
  const remove = useMutation({
    mutationFn: (type: ContentType) => api(`/v1/content-types/${type.id}`, { method: 'DELETE', ifMatch: type.version }),
    onSuccess: async () => {
      setDeleting(undefined);
      await client.invalidateQueries({ queryKey: ['catalog'] });
    },
  });
  if (!canRead) return <p>You need contentType.read to view this catalog.</p>;
  return (
    <div className="grid gap-5">
      <SettingsSection
        title="Content types"
        description="Reusable schemas shared by collections. A type inherits every field and rule of the type it derives from; lists customize their own copy."
        actions={canManage && <Button onClick={() => setType('new')}>New content type</Button>}
      >
        <QueryFeedback query={types} />
        <ul className="divide-y">
          {typeTree(types.data ?? []).map(({ type, depth }) => (
            <li key={type.id} className="flex items-center gap-2 py-3" style={{ paddingLeft: `${depth * 1.25}rem` }}>
              <span className="flex-1 font-medium">
                {type.name}{' '}
                <span className="text-xs text-muted">
                  {type.key} · {type.effective?.field_ids.length ?? type.field_ids?.length ?? 0} fields
                  {(type.field_ids?.length ?? 0) !== (type.effective?.field_ids.length ?? 0) && ` (${type.field_ids?.length ?? 0} own)`}
                </span>
              </span>
              <Button onClick={() => setType(type)}>{canManage ? 'Edit' : 'View'}</Button>
              {canManage && type.parent_id && (
                <Button variant="ghost" onClick={() => setDeleting(type)}>
                  Delete
                </Button>
              )}
            </li>
          ))}
        </ul>
      </SettingsSection>
      <SettingsSection
        title="Field catalog"
        description="Editing or deleting a field changes every content type and collection that uses it."
        actions={canManage && <Button onClick={() => setField('new')}>New field</Button>}
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
        title={`Delete content type ${deleting?.name ?? ''}?`}
        description="Only a type without subtypes that no list or library uses, now or in its items' history, can be deleted. Workflow rules and smart folders that name it stop matching."
        confirm="Delete content type"
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
}

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
  const inherited = parent?.effective?.field_ids ?? [];
  const inheritedHidden = new Set(parent?.effective?.hidden_field_ids ?? []);
  const byId = new Map(fields.map((field) => [field.id, field]));
  const label = (field: FieldDefinition) => (
    <>
      {field.label} <span className="text-xs text-muted">{fieldTypeLabel(field.type)}</span>
    </>
  );
  return (
    <>
      {parent && (
        <fieldset>
          <legend>Inherited from {parent.name}</legend>
          {inherited.length === 0 && <p className="text-sm text-muted">{parent.name} has no fields.</p>}
          {inherited.flatMap((id) => byId.get(id) ?? []).map((field) => {
            const shown = !inheritedHidden.has(field.id) && !schema.hidden.includes(field.id);
            // A field forms must fill cannot be hidden, but can always be shown again.
            const mandatory = field.required && field.options?.default_value === undefined;
            return (
              <label key={field.id} className="flex gap-2 py-1">
                <Checkbox
                  aria-label={`Show ${field.label} in forms`}
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
                <span className="text-xs text-muted">{shown ? 'Shown in forms' : 'Hidden in forms'}</span>
              </label>
            );
          })}
        </fieldset>
      )}
      <fieldset>
        <legend>{parent ? 'Own fields' : 'Fields'}</legend>
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
      <p className="text-sm font-medium">Validation rules</p>
      {!!parent?.effective?.rules.length && (
        <p className="text-sm text-muted">{parent.effective.rules.length} inherited rules also apply.</p>
      )}
      <JsonInput
        label="Content type validation rules"
        value={schema.rules}
        onChange={(value) => {
          if (!Array.isArray(value)) throw new Error('Rules must be an array.');
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
  const client = useQueryClient();
  const root = types.find((entry) => !entry.parent_id);
  const [key, setKey] = useState(type?.key ?? '');
  const [name, setName] = useState(type?.name ?? '');
  const [parentId, setParentId] = useState(type ? (type.parent_id ?? '') : (root?.id ?? ''));
  const [schema, setSchema] = useState<Schema>({ ids: type?.field_ids ?? [], hidden: type?.hidden_field_ids ?? [], rules: type?.rules ?? [] });
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
          parent_id: type ? undefined : parentId || undefined,
          field_ids: schema.ids.filter((id) => !parent?.effective?.field_ids.includes(id)),
          hidden_field_ids: schema.hidden,
          rules: schema.rules,
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
      title={type ? `Content type ${type.name}` : 'New content type'}
      description="Changes apply to every type deriving from this one and to every list using them."
      submitLabel="Save content type"
      submitDisabled={!canManage}
      pending={save.isPending}
      onClose={onClose}
      onSubmit={() => canManage && save.mutate()}
    >
      <fieldset className="grid gap-3" disabled={!canManage}>
        <Field id="content-type-key" label="Key">
          <Input
            id="content-type-key"
            disabled={!!type}
            required
            pattern="[a-z][a-z0-9_]*"
            value={key}
            onChange={(event) => setKey(event.target.value)}
          />
        </Field>
        <Field id="content-type-name" label="Name">
          <Input id="content-type-name" required value={name} onChange={(event) => setName(event.target.value)} />
        </Field>
        {(!type || type.parent_id) && (
          <Field id="content-type-parent" label="Derives from">
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
  const client = useQueryClient();
  const catalog = useQuery(catalogTypesQuery);
  const fields = useQuery(catalogFieldsQuery);
  const parent = catalog.data?.find((entry) => entry.id === type.parent_id);
  const [schema, setSchema] = useState<Schema>({ ids: type.field_ids ?? [], hidden: type.hidden_field_ids ?? [], rules: type.rules ?? [] });
  const save = useMutation({
    mutationFn: () =>
      api(`/v1/resources/${collection.id}/content-types/${type.id}`, {
        method: 'PUT',
        ifMatch: type.version,
        body: { field_ids: schema.ids, hidden_field_ids: schema.hidden, rules: schema.rules },
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: keys.resources });
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title={`Customize ${type.name} in ${collection.name}`}
      description={`Only this list changes. Everything ${type.name} defines stays, and stays in step with the catalog.`}
      submitLabel="Save"
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
                {collection ? '' : `${field.key} · `}
                {fieldTypeLabel(field.type)}
                {field.required ? ' · Required' : ''}
                {field.indexed ? (field.index_status && field.index_status !== 'ready' ? ' · Preparing filters…' : ' · Filterable') : ''}
              </span>
            </span>
            <Button size="sm" variant="ghost" onClick={() => onEdit(field)}>
              {canManage ? 'Edit' : 'View'}
            </Button>
            {canManage && (
              <Button size="sm" variant="ghost" onClick={() => setDeleting(field)}>
                {collection ? 'Remove' : 'Delete'}
              </Button>
            )}
          </li>
        ))}
      </ul>
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(undefined)}
        title={collection ? `Remove column ${deleting?.label ?? ''}?` : `Delete field ${deleting?.label ?? ''}?`}
        description={
          collection
            ? 'This removes the column from this collection, with its current values; other lists keep it. Earlier versions keep theirs. A column a catalog content type provides stays: hide it in the content type instead.'
            : 'This removes the shared field from every content type, list and library that uses it, with its current values. Earlier versions keep theirs.'
        }
        confirm={collection ? 'Remove column' : 'Delete shared field'}
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </>
  );
}

/** The columns of a list or library: shared organization fields attached through its content types. */
export function CollectionColumns({ collection, canManage }: { collection: Resource; canManage: boolean }) {
  const fields = useQuery(fieldsQuery(collection.id));
  const [editing, setEditing] = useState<FieldDefinition | 'new'>();
  return (
    <SettingsSection
      title="Columns"
      description="The information kept for each item. Columns are shared fields: editing one changes it everywhere it is used, removing one removes it from this collection only."
      actions={
        canManage && (
          <Button variant="primary" onClick={() => setEditing('new')}>
            <Plus /> Add column
          </Button>
        )
      }
    >
      <QueryFeedback query={fields} />
      <SystemProperties />
      <h3 className="mt-3 font-medium">Custom fields</h3>
      <FieldList collection={collection} fields={fields.data ?? []} canManage={canManage} onEdit={setEditing} />
      {fields.isSuccess && fields.data.length === 0 && (
        <p className="text-sm text-muted">No custom fields defined. System properties are available on every item.</p>
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
  // Each attached list type copies one catalog type.
  const available = typeTree(catalog.data?.filter((type) => !types.data?.some((attached) => attached.parent_id === type.id)) ?? []);
  return (
    <SettingsSection
      title="Content types"
      description="Kinds of items this list holds. Each comes from the catalog with its columns; customizing one changes it for this list only. New items use the default type."
    >
      <QueryFeedback query={types} />
      <ul className="divide-y">
        {types.data?.map((type) => (
          <li key={type.id} className="flex items-center gap-2 py-2">
            <span className="flex-1 text-[13px] font-medium">
              {type.name} {type.is_default && <Badge>Default</Badge>}
              {(!!type.field_ids?.length || !!type.hidden_field_ids?.length || !!type.rules?.length) && (
                <span className="ml-2 text-xs text-muted">Customized</span>
              )}
            </span>
            {canManage && (
              <>
                {canReadCatalog && (
                  <Button size="sm" variant="ghost" onClick={() => setCustomizing(type)}>
                    {canManageTypes ? 'Customize' : 'View type'}
                  </Button>
                )}
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={type.is_default || attach.isPending || !type.parent_id}
                  onClick={() => type.parent_id && attach.mutate({ id: type.parent_id, isDefault: true })}
                >
                  Make default
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={(types.data?.length ?? 0) <= 1}
                  onClick={() => setDetaching(type)}
                >
                  Remove
                </Button>
              </>
            )}
          </li>
        ))}
      </ul>
      {canManage && canReadCatalog && available.length > 0 && (
        <div className="mt-3 flex flex-col gap-2 border-t pt-3 sm:flex-row sm:items-center">
          <Select
            aria-label="Add content type"
            className="sm:max-w-xs"
            value={attachId}
            onChange={(event) => setAttachId(event.target.value)}
          >
            <option value="">Add a content type…</option>
            {available.map(({ type, depth }) => (
              <option key={type.id} value={type.id}>
                {treeLabel(type.name, depth)}
              </option>
            ))}
          </Select>
          <label className="flex items-center gap-2 text-[13px]">
            <Checkbox checked={makeDefault} onChange={(event) => setMakeDefault(event.target.checked)} />
            Make default
          </label>
          <Button
            disabled={!attachId || attach.isPending}
            onClick={() => attach.mutate({ id: attachId, isDefault: makeDefault })}
          >
            Add
          </Button>
        </div>
      )}
      <ConfirmDialog
        open={!!detaching}
        onOpenChange={(open) => !open && setDetaching(undefined)}
        title={`Remove ${detaching?.name ?? ''} from this list?`}
        description="The content type stays available to other lists, and this list keeps its customizations if you add it again. A type still used by items cannot be removed."
        confirm="Remove content type"
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
