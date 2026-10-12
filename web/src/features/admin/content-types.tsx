import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { contentTypesQuery, fieldsQuery, keys, permissions } from '@/api/queries';
import type { ContentType, FieldDefinition, Resource, ValidationRule } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { JsonInput } from '@/features/workflows/schema-form';
import { Field, QueryFeedback, useHasScope } from './common';
import { FieldDialog } from './field-dialog';

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
  if (!canRead) return <p>You need contentType.read to view this catalog.</p>;
  return (
    <div className="grid gap-5">
      <SettingsSection
        title="Content types"
        description="Reusable schemas shared by collections."
        actions={canManage && <Button onClick={() => setType('new')}>New content type</Button>}
      >
        <QueryFeedback query={types} />
        <ul className="divide-y">
          {types.data?.map((type) => (
            <li key={type.id} className="flex items-center gap-2 py-3">
              <span className="flex-1 font-medium">
                {type.name}{' '}
                <span className="text-xs text-muted">
                  {type.key} · {type.field_ids?.length ?? 0} fields
                </span>
              </span>
              <Button onClick={() => setType(type)}>{canManage ? 'Edit' : 'View'}</Button>
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
    </div>
  );
}

function TypeDialog({
  type,
  fields,
  canManage,
  onClose,
}: {
  type?: ContentType;
  fields: FieldDefinition[];
  canManage: boolean;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [key, setKey] = useState(type?.key ?? '');
  const [name, setName] = useState(type?.name ?? '');
  const [ids, setIds] = useState(type?.field_ids ?? []);
  const [rules, setRules] = useState<ValidationRule[]>(type?.rules ?? []);
  const save = useMutation({
    mutationFn: () =>
      api(type ? `/v1/content-types/${type.id}` : '/v1/content-types', {
        method: type ? 'PUT' : 'POST',
        ifMatch: type?.version,
        body: { key: key.trim(), name: name.trim(), field_ids: ids, rules },
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
      description="Changes apply to all attached collections."
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
        <fieldset>
          <legend>Fields</legend>
          {fields.map((field) => (
            <label key={field.id} className="flex gap-2 py-1">
              <Checkbox
                checked={ids.includes(field.id)}
                onChange={(event) =>
                  setIds((current) =>
                    event.target.checked ? [...current, field.id] : current.filter((id) => id !== field.id),
                  )
                }
              />
              {field.label}{' '}
              <span className="text-xs text-muted">
                {field.key} · {field.type}
              </span>
            </label>
          ))}
        </fieldset>
        <p className="text-sm font-medium">Validation rules</p>
        <JsonInput
          label="Content type validation rules"
          value={rules}
          onChange={(value) => {
            if (!Array.isArray(value)) throw new Error('Rules must be an array.');
            setRules(value as ValidationRule[]);
          }}
        />
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
                {field.key} · {field.type}
                {field.required ? ' · required' : ''}
                {field.index_status ? ` · index ${field.index_status}` : ''}
              </span>
            </span>
            <Button size="sm" onClick={() => onEdit(field)}>
              {canManage ? 'Edit' : 'View'}
            </Button>
            {canManage && (
              <Button size="sm" variant="danger" onClick={() => setDeleting(field)}>
                Delete
              </Button>
            )}
          </li>
        ))}
      </ul>
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(undefined)}
        title={`Delete field ${deleting?.label ?? ''}?`}
        description="This removes the shared field from all content types and collections using it. Current values are removed; retained revision schemas remain."
        confirm="Delete shared field"
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </>
  );
}

export function CollectionSchema({ collection }: { collection: Resource }) {
  const client = useQueryClient();
  const access = useQuery({
    queryKey: ['permissions', collection.id],
    queryFn: () => permissions(collection.id),
    retry: false,
  });
  const canManage = !!access.data;
  const canReadCatalog = useHasScope('contentType.read');
  const catalog = useQuery({ ...catalogTypesQuery, enabled: canReadCatalog });
  const types = useQuery(contentTypesQuery(collection.id));
  const fields = useQuery(fieldsQuery(collection.id));
  const [attachId, setAttachId] = useState('');
  const [makeDefault, setMakeDefault] = useState(false);
  const [editing, setEditing] = useState<FieldDefinition>();
  const [detaching, setDetaching] = useState<ContentType>();
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
    onSuccess: refresh,
  });
  const detach = useMutation({
    mutationFn: (type: ContentType) =>
      api(`/v1/resources/${collection.id}/content-types/${type.id}`, { method: 'DELETE', ifMatch: collection.version }),
    onSuccess: async () => {
      setDetaching(undefined);
      await refresh();
    },
  });
  return (
    <section className="grid gap-2 border-t pt-3">
      <h3 className="font-medium">Content types and shared fields</h3>
      <QueryFeedback query={types} />
      <ul>
        {types.data?.map((type) => (
          <li key={type.id} className="flex items-center gap-2 py-1">
            <span className="flex-1">
              {type.name} {type.is_default && <Badge>Default</Badge>}
            </span>
            {canManage && (
              <>
                <Button
                  size="sm"
                  disabled={type.is_default || attach.isPending}
                  onClick={() => attach.mutate({ id: type.id, isDefault: true })}
                >
                  Make default
                </Button>
                <Button size="sm" disabled={(types.data?.length ?? 0) <= 1} onClick={() => setDetaching(type)}>
                  Detach
                </Button>
              </>
            )}
          </li>
        ))}
      </ul>
      {canManage && canReadCatalog && (
        <>
          <Select
            aria-label="Attach content type"
            value={attachId}
            onChange={(event) => setAttachId(event.target.value)}
          >
            <option value="">Choose content type…</option>
            {catalog.data
              ?.filter((type) => !types.data?.some((attached) => attached.id === type.id))
              .map((type) => (
                <option key={type.id} value={type.id}>
                  {type.name}
                </option>
              ))}
          </Select>
          <label className="flex gap-2">
            <Checkbox checked={makeDefault} onChange={(event) => setMakeDefault(event.target.checked)} />
            Make default
          </label>
          <Button
            disabled={!attachId || attach.isPending}
            onClick={() => attach.mutate({ id: attachId, isDefault: makeDefault })}
          >
            Attach content type
          </Button>
        </>
      )}
      <QueryFeedback query={fields} />
      <FieldList collection={collection} fields={fields.data ?? []} canManage={canManage} onEdit={setEditing} />
      {editing && (
        <FieldDialog
          field={editing}
          collection={collection}
          canManage={canManage}
          onClose={() => setEditing(undefined)}
        />
      )}
      <ConfirmDialog
        open={!!detaching}
        onOpenChange={(open) => !open && setDetaching(undefined)}
        title={`Detach ${detaching?.name ?? ''}?`}
        description="The shared catalog definition remains. A type in use by current items cannot be detached."
        confirm="Detach type"
        busy={detach.isPending}
        error={detach.error}
        onConfirm={() => detaching && detach.mutate(detaching)}
      />
    </section>
  );
}
