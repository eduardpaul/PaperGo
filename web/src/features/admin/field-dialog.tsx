import { organizationPreferencesQuery, resourceQuery } from '@/api/queries';
import { CurrencyPicker } from '@/features/fields/currency-picker';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { collectionChildren, keys } from '@/api/queries';
import type { FieldDefinition, FieldOptions, Resource } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Input, Label, Textarea } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { FieldEditor } from '@/features/fields/editor';
import { valuesPatch } from '@/features/fields/values';
import { termSetsQuery } from './taxonomy';
import { Field } from './common';

export const fieldTypes = [
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
const typeLabels: Record<string, string> = {
  text: 'Single line of text',
  note: 'Multiple lines of text',
  email: 'Email address',
  url: 'Link',
  date: 'Date',
  datetime: 'Date and time',
  choice: 'Choice',
  number: 'Number',
  integer: 'Whole number',
  decimal: 'Decimal number',
  currency: 'Currency',
  boolean: 'Yes/No',
  lookup: 'Lookup (item in another list)',
  person: 'Person',
  term: 'Taxonomy (term set)',
  keywords: 'Keywords',
};

export function fieldTypeLabel(type: string) {
  return typeLabels[type] ?? type;
}

/** A field key from its label: lowercase letters, digits and underscores, starting with a letter. */
export function keyFromLabel(label: string) {
  const key = label
    .normalize('NFKD')
    .replaceAll(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .replaceAll(/[^a-z0-9]+/g, '_')
    .replaceAll(/^_+|_+$/g, '')
    .slice(0, 64);
  return /^[a-z]/.test(key) ? key : key ? `field_${key}` : '';
}

const multipleTypes = new Set(['choice', 'person', 'lookup', 'term', 'keywords']);

/** Shared organization field editor, adapted from PaperDotNet's content type editor. */
export function FieldDialog({
  field,
  collection,
  onClose,
  canManage = true,
}: {
  field?: FieldDefinition;
  collection?: Resource;
  onClose: () => void;
  canManage?: boolean;
}) {
  const client = useQueryClient();
  const [key, setKey] = useState(field?.key ?? '');
  const [keyEdited, setKeyEdited] = useState(!!field);
  const [label, setLabel] = useState(field?.label ?? '');
  const [type, setType] = useState(field?.type ?? 'text');
  const [scale, setScale] = useState(field?.scale ?? 2);
  const [required, setRequired] = useState(!!field?.required);
  const [indexed, setIndexed] = useState(!!field?.indexed);
  const [choices, setChoices] = useState((field?.choices ?? []).join('\n'));
  const [options, setOptions] = useState<FieldOptions>(field?.options ?? {});
  const [hasDefault, setHasDefault] = useState(field?.options?.default_value !== undefined);
  const [defaultValue, setDefaultValue] = useState<unknown>(field?.options?.default_value);
  const termSets = useQuery({ ...termSetsQuery, enabled: type === 'term' });
  const lookupTargets = useQuery({
    queryKey: ['resources', collection?.workspace_id, 'collections'],
    queryFn: () => collectionChildren(collection!.workspace_id),
    enabled: type === 'lookup' && !!collection,
  });
  const workspace = useQuery({ ...resourceQuery(collection?.workspace_id ?? ''), enabled: !field && !!collection?.workspace_id });
  const organization = useQuery({ ...organizationPreferencesQuery, enabled: !field });
  const currencyCode = options.currency_code ?? (workspace.data?.default_currency || organization.data?.default_currency || 'USD');
  const effectiveOptions = type === 'currency' ? { ...options, currency_code: currencyCode } : options;
  const draft: FieldDefinition = {
    id: field?.id ?? 'new',
    key,
    label: 'Default value',
    type,
    scale,
    choices: choices
      .split('\n')
      .map((choice) => choice.trim())
      .filter(Boolean),
    options: { ...effectiveOptions, default_value: undefined },
  };
  const patchOptions = (patch: Partial<FieldOptions>) => setOptions((current) => ({ ...current, ...patch }));
  const save = useMutation({
    mutationFn: () => {
      const normalizedDefault = hasDefault ? valuesPatch([draft], {}, { [key]: defaultValue })?.[key] : undefined;
      const body = {
        label: label.trim(),
        required,
        indexed: indexed || type === 'keywords',
        choices: type === 'choice' ? draft.choices : undefined,
        options: { ...effectiveOptions, default_value: normalizedDefault },
      };
      return field
        ? api(collection ? `/v1/resources/${collection.id}/fields/${field.id}` : `/v1/fields/${field.id}`, {
            method: 'PATCH',
            ifMatch: collection?.version ?? field.version,
            body,
          })
        : api(collection ? `/v1/resources/${collection.id}/fields` : '/v1/fields', {
            method: 'POST',
            body: { ...body, key: key.trim(), type, scale: type === 'decimal' ? scale : undefined },
          });
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: keys.resources });
      await client.invalidateQueries({ queryKey: ['catalog'] });
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title={field ? `${collection ? 'Column' : 'Field'} ${field.label}` : collection ? 'Add a column' : 'New field'}
      description={
        collection
          ? 'Columns are shared organization fields. Editing one updates every list and library that uses it.'
          : 'Organization fields are shared. Editing a field updates every collection using it.'
      }
      submitLabel="Save field"
      submitDisabled={!canManage || (type === 'term' && !options.term_set_id)}
      pending={save.isPending}
      onClose={onClose}
      onSubmit={() => canManage && save.mutate()}
    >
      <fieldset className="grid gap-3" disabled={!canManage}>
        <Field id="catalog-field-label" label="Name">
          <Input
            id="catalog-field-label"
            required
            autoFocus={!field}
            value={label}
            onChange={(event) => {
              setLabel(event.target.value);
              if (!keyEdited) setKey(keyFromLabel(event.target.value));
            }}
          />
        </Field>
        <Field
          id="catalog-field-key"
          label="Key"
          hint={field ? 'The key cannot be changed.' : 'Used by the API, filters and workflows. Lowercase letters, digits and underscores.'}
        >
          <Input
            id="catalog-field-key"
            required
            pattern="[a-z][a-z0-9_]*"
            disabled={!!field}
            value={key}
            onChange={(event) => {
              setKey(event.target.value);
              setKeyEdited(true);
            }}
          />
        </Field>
        <Field id="catalog-field-type" label="Type">
          <Select
            id="catalog-field-type"
            disabled={!!field}
            value={type}
            onChange={(event) => {
              setType(event.target.value);
              setOptions({});
              setHasDefault(false);
              setDefaultValue(undefined);
            }}
          >
            {fieldTypes.map((value) => (
              <option key={value} value={value}>
                {fieldTypeLabel(value)}
              </option>
            ))}
          </Select>
        </Field>
        {type === 'decimal' && (
          <Field id="field-scale" label="Decimal scale">
            <Input
              id="field-scale"
              type="number"
              min={0}
              max={28}
              disabled={!!field}
              value={scale}
              onChange={(event) => setScale(Number(event.target.value))}
            />
          </Field>
        )}
        {type === 'choice' && (
          <Field id="field-choices" label="Choices, one per line">
            <Textarea
              id="field-choices"
              required
              value={choices}
              onChange={(event) => setChoices(event.target.value)}
            />
          </Field>
        )}
        <Field id="field-description" label="Description">
          <Textarea
            id="field-description"
            value={options.description ?? ''}
            onChange={(event) => patchOptions({ description: event.target.value || undefined })}
          />
        </Field>
        <Field id="field-search-weight" label="In search">
          <Select
            id="field-search-weight"
            value={options.search_weight ?? 'normal'}
            onChange={(event) =>
              patchOptions({
                search_weight: event.target.value === 'normal' ? undefined : (event.target.value as FieldOptions['search_weight']),
              })
            }
          >
            <option value="normal">Searchable</option>
            <option value="high">Searchable, ranked with names</option>
            <option value="none">Not searchable (filters only)</option>
          </Select>
        </Field>
        {['text', 'note', 'email', 'url'].includes(type) && (
          <Field id="field-length" label="Maximum length">
            <Input
              id="field-length"
              type="number"
              min={1}
              value={options.max_length ?? ''}
              onChange={(event) =>
                patchOptions({ max_length: event.target.value ? Number(event.target.value) : undefined })
              }
            />
          </Field>
        )}
        {['number', 'integer', 'decimal', 'currency'].includes(type) && (
          <>
            <Field id="field-min" label="Minimum">
              <Input
                id="field-min"
                inputMode="decimal"
                value={options.minimum ?? ''}
                onChange={(event) => patchOptions({ minimum: event.target.value || undefined })}
              />
            </Field>
            <Field id="field-max" label="Maximum">
              <Input
                id="field-max"
                inputMode="decimal"
                value={options.maximum ?? ''}
                onChange={(event) => patchOptions({ maximum: event.target.value || undefined })}
              />
            </Field>
          </>
        )}
        {type === 'currency' && (
          <div className="grid gap-1.5"><Label id="field-currency-label">Currency</Label>
            <CurrencyPicker id="field-currency" labelledBy="field-currency-label" value={currencyCode} onChange={code => patchOptions({ currency_code: code || undefined })} />
            {!field && <p className="text-xs text-muted">Starts with the workspace default, or the organization default when no override is set.</p>}
          </div>
        )}
        {type === 'lookup' && (
          <Field id="field-lookup" label="Look up items in">
            {collection ? (
              <Select
                id="field-lookup"
                required
                value={options.lookup_container_id ?? ''}
                onChange={(event) => patchOptions({ lookup_container_id: event.target.value })}
              >
                <option value="">Choose a list…</option>
                {lookupTargets.data?.map((target) => (
                  <option key={target.id} value={target.id}>
                    {target.name}
                  </option>
                ))}
              </Select>
            ) : (
              <Input
                id="field-lookup"
                required
                placeholder="List or library id"
                value={options.lookup_container_id ?? ''}
                onChange={(event) => patchOptions({ lookup_container_id: event.target.value })}
              />
            )}
          </Field>
        )}
        {type === 'term' && (
          <Field id="field-term-set" label="Term set" hint="Choose the term set that defines this taxonomy field.">
            <Select
              id="field-term-set"
              required
              value={options.term_set_id ?? ''}
              onChange={(event) => patchOptions({ term_set_id: event.target.value })}
            >
              <option value="">Choose…</option>
              {termSets.data?.map((set) => (
                <option key={set.id} value={set.id}>
                  {set.name}
                </option>
              ))}
            </Select>
          </Field>
        )}
        <label className="flex gap-2">
          <Checkbox checked={required} onChange={(event) => setRequired(event.target.checked)} />
          Required
        </label>
        <label className="flex gap-2">
          <Checkbox
            checked={indexed || type === 'keywords'}
            disabled={type === 'keywords'}
            onChange={(event) => setIndexed(event.target.checked)}
          />
          Use for filtering, sorting and grouping in views{type === 'keywords' ? ' (always on for keywords)' : ''}
        </label>
        <label className="flex gap-2">
          <Checkbox checked={!!options.unique} onChange={(event) => patchOptions({ unique: event.target.checked })} />
          Unique within each collection
        </label>
        {multipleTypes.has(type) && (
          <label className="flex gap-2">
            <Checkbox
              checked={!!options.multiple}
              disabled={!!field}
              onChange={(event) => {
                patchOptions({ multiple: event.target.checked });
                setDefaultValue(undefined);
              }}
            />
            Allow multiple values
          </label>
        )}
        <label className="flex gap-2">
          <Checkbox checked={hasDefault} onChange={(event) => setHasDefault(event.target.checked)} />
          Set a default value
        </label>
        {hasDefault && <FieldEditor key={type} field={draft} value={defaultValue} onChange={setDefaultValue} />}
        <Label className="text-xs text-muted">Saved keys, types and decimal scales cannot be changed.</Label>
      </fieldset>
    </FormDialog>
  );
}
