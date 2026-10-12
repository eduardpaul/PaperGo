import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { keys } from '@/api/queries';
import type { Resource } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Button } from '@/components/ui/button';
import { Input, Label, Textarea } from '@/components/ui/input';
import { Select } from '@/components/ui/select';

import { CollectionSchema } from '@/features/admin/content-types';

const fieldTypes = [
  'text',
  'note',
  'email',
  'url',
  'date',
  'datetime',
  'choice',
  'number',
  'integer',
  'decimal',
  'currency',
  'boolean',
  'lookup',
  'person',
  'term',
  'keywords',
];

const multipleTypes = new Set(['choice', 'person', 'lookup', 'term', 'keywords']);

export function CollectionSettings({
  collection,
  open,
  onClose,
  onDeleted,
}: {
  collection: Resource;
  open: boolean;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(collection.name);
  const [description, setDescription] = useState(collection.description ?? '');
  const [duplicate, setDuplicate] = useState(collection.duplicate_policy ?? 'warn');
  const [publishing, setPublishing] = useState(!!collection.publishing_enabled);
  const [webdav, setWebdav] = useState(!!collection.webdav_enabled);
  const [versioning, setVersioning] = useState(collection.versioning ?? 'off');
  const [maxVersions, setMaxVersions] = useState(String(collection.max_versions ?? 50));
  const [allowFolders, setAllowFolders] = useState(collection.allow_folders !== false);
  const [field, setField] = useState({
    key: '',
    label: '',
    type: 'text',
    choices: '',
    currency: '',
    lookup: '',
    termSet: '',
  });
  const [required, setRequired] = useState(false);
  const [indexed, setIndexed] = useState(false);
  const [multiple, setMultiple] = useState(false);

  const save = useMutation({
    mutationFn: async () => {
      const body: Record<string, unknown> = {};
      if (name.trim() !== collection.name) body.name = name.trim();
      if (description !== (collection.description ?? '')) body.description = description;
      if (duplicate !== (collection.duplicate_policy ?? 'warn')) body.duplicate_policy = duplicate;
      if (publishing !== !!collection.publishing_enabled) body.publishing_enabled = publishing;
      if (collection.kind === 'library' && webdav !== !!collection.webdav_enabled) body.webdav_enabled = webdav;
      if (versioning !== (collection.versioning ?? 'off')) body.versioning = versioning;
      const max = Number(maxVersions);
      if (Number.isInteger(max) && max !== collection.max_versions) body.max_versions = max;
      if (allowFolders !== (collection.allow_folders !== false)) body.allow_folders = allowFolders;
      if (Object.keys(body).length === 0) return collection;
      return (
        await api<Resource>(`/v1/resources/${collection.id}`, { method: 'PATCH', ifMatch: collection.version, body })
      ).data;
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      toast.success('Collection saved');
      onClose();
    },
  });

  const addField = useMutation({
    mutationFn: async () => {
      const body: Record<string, unknown> = {
        key: field.key.trim(),
        label: field.label.trim(),
        type: field.type,
        required,
        indexed,
      };
      if (field.type === 'choice')
        body.choices = field.choices
          .split(',')
          .map((choice) => choice.trim())
          .filter(Boolean);
      if (field.type === 'decimal') body.scale = 2;
      const options: Record<string, unknown> = {};
      if (multipleTypes.has(field.type) && multiple) options.multiple = true;
      if (field.type === 'currency') options.currency_code = field.currency.trim().toUpperCase();
      if (field.type === 'lookup' && field.lookup.trim()) options.lookup_container_id = field.lookup.trim();
      if (field.type === 'term' && field.termSet.trim()) options.term_set_id = field.termSet.trim();
      if (Object.keys(options).length > 0) body.options = options;
      await api(`/v1/resources/${collection.id}/fields`, { method: 'POST', body });
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      toast.success('Field added');
      setField({ key: '', label: '', type: 'text', choices: '', currency: '', lookup: '', termSet: '' });
      setRequired(false);
      setIndexed(false);
      setMultiple(false);
    },
  });

  const remove = useMutation({
    mutationFn: () =>
      api(`/v1/resources/${collection.id}`, {
        method: 'DELETE',
        ifMatch: collection.version,
        query: { recursive: 'true' },
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      onDeleted();
    },
  });

  return (
    <FormDialog
      open={open}
      title="Collection settings"
      description="Name, history, publishing and fields. Adding a field needs manage access."
      submitLabel="Save"
      pending={save.isPending}
      onClose={onClose}
      onSubmit={() => save.mutate()}
    >
      <Label htmlFor="collection-name">Name</Label>
      <Input id="collection-name" value={name} onChange={(event) => setName(event.target.value)} required />
      <Label htmlFor="collection-description">Description</Label>
      <Textarea
        id="collection-description"
        value={description}
        onChange={(event) => setDescription(event.target.value)}
      />
      <Label htmlFor="duplicate-policy">Duplicate files</Label>
      <Select
        id="duplicate-policy"
        value={duplicate}
        onChange={(event) =>
          setDuplicate(event.target.value === 'allow' || event.target.value === 'block' ? event.target.value : 'warn')
        }
      >
        <option value="allow">Allow</option>
        <option value="warn">Warn</option>
        <option value="block">Block</option>
      </Select>
      <Label htmlFor="versioning">Versioning</Label>
      <Select
        id="versioning"
        value={versioning}
        onChange={(event) => setVersioning(event.target.value === 'major' ? 'major' : 'off')}
      >
        <option value="off">Off</option>
        <option value="major">Major</option>
      </Select>
      <Label htmlFor="max-versions">Versions to keep</Label>
      <Input
        id="max-versions"
        inputMode="numeric"
        value={maxVersions}
        onChange={(event) => setMaxVersions(event.target.value)}
      />
      <label className="flex items-center gap-2 text-[13px]">
        <input type="checkbox" checked={publishing} onChange={(event) => setPublishing(event.target.checked)} />
        Require publish before readers see changes
      </label>
      <label className="flex items-center gap-2 text-[13px]">
        <input type="checkbox" checked={allowFolders} onChange={(event) => setAllowFolders(event.target.checked)} />
        Allow folders
      </label>
      {collection.kind === 'library' && (
        <label className="flex items-center gap-2 text-[13px]">
          <input type="checkbox" checked={webdav} onChange={(event) => setWebdav(event.target.checked)} />
          WebDAV
        </label>
      )}
      <div className="mt-2 border-t pt-3">
        <p className="text-sm font-medium">Add a field</p>
        <p className="mt-0.5 text-xs text-muted">Keys are lowercase letters, digits and underscores.</p>
        <div className="mt-2 grid gap-2">
          <Input
            placeholder="Key"
            value={field.key}
            onChange={(event) => setField({ ...field, key: event.target.value })}
          />
          <Input
            placeholder="Label"
            value={field.label}
            onChange={(event) => setField({ ...field, label: event.target.value })}
          />
          <Select value={field.type} onChange={(event) => setField({ ...field, type: event.target.value })}>
            {fieldTypes.map((type) => (
              <option key={type} value={type}>
                {type}
              </option>
            ))}
          </Select>
          {field.type === 'choice' && (
            <Input
              placeholder="Choices, comma separated"
              value={field.choices}
              onChange={(event) => setField({ ...field, choices: event.target.value })}
            />
          )}
          {field.type === 'currency' && (
            <Input
              placeholder="Currency code"
              value={field.currency}
              onChange={(event) => setField({ ...field, currency: event.target.value })}
              maxLength={3}
            />
          )}
          {field.type === 'lookup' && (
            <Input
              placeholder="Lookup collection id"
              value={field.lookup}
              onChange={(event) => setField({ ...field, lookup: event.target.value })}
            />
          )}
          {field.type === 'term' && (
            <Input
              placeholder="Term set id"
              value={field.termSet}
              onChange={(event) => setField({ ...field, termSet: event.target.value })}
            />
          )}
          <label className="flex items-center gap-2 text-[13px]">
            <input type="checkbox" checked={required} onChange={(event) => setRequired(event.target.checked)} />{' '}
            Required
          </label>
          <label className="flex items-center gap-2 text-[13px]">
            <input type="checkbox" checked={indexed} onChange={(event) => setIndexed(event.target.checked)} /> Indexed
          </label>
          {multipleTypes.has(field.type) && (
            <label className="flex items-center gap-2 text-[13px]">
              <input type="checkbox" checked={multiple} onChange={(event) => setMultiple(event.target.checked)} />{' '}
              Multiple
            </label>
          )}
          <Button
            type="button"
            disabled={!field.key.trim() || !field.label.trim() || addField.isPending}
            onClick={() => addField.mutate()}
          >
            Add field
          </Button>
        </div>
      </div>
      <CollectionSchema collection={collection} />
      {!collection.system_key && (
        <Button type="button" variant="danger" disabled={remove.isPending} onClick={() => remove.mutate()}>
          Delete collection
        </Button>
      )}
    </FormDialog>
  );
}
