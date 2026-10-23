import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { allPages } from '@/api/pages';
import type { FieldDefinition, Term } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { SettingsSection } from '@/features/settings/section';
import { JsonInput } from '@/features/workflows/schema-form';
import { Field, QueryFeedback, useHasScope } from './common';

export interface RelationshipType {
  id: string;
  term: Term;
  directed: boolean;
  inverse_label?: string;
  max_incoming?: number;
  max_outgoing?: number;
  attributes?: { fields: (Omit<FieldDefinition, 'key'> & { id: string })[] };
}
export const relationshipTypesQuery = {
  queryKey: ['relationship-types'],
  queryFn: () => allPages<RelationshipType>('/v1/relationship-types'),
};

export function RelationshipTypesCatalog() {
  const listRead = useHasScope('item.read');
  const taxonomyRead = useHasScope('taxonomy.read');
  const canRead = listRead && taxonomyRead;
  const canCreate = useHasScope('item.write');
  const types = useQuery({ ...relationshipTypesQuery, enabled: canRead });
  const [creating, setCreating] = useState(false);
  if (!canRead) return <p>You need item.read and taxonomy.read to view relationship types.</p>;
  return (
    <>
      <SettingsSection
        title="Relationship types"
        description="Labels belong to the term store. Direction, limits and attributes stay fixed once created."
        actions={canCreate && <Button onClick={() => setCreating(true)}>New relationship type</Button>}
      >
        <QueryFeedback query={types} />
        <ul className="divide-y">
          {types.data?.map((type) => (
            <li key={type.id} className="py-3">
              <h3 className="font-medium">{type.term.name}</h3>
              <p className="text-xs text-muted">
                {type.directed
                  ? `Directed${type.inverse_label ? ` · inverse: ${type.inverse_label}` : ''}`
                  : 'Symmetric'}
                {type.max_incoming ? ` · maximum incoming ${type.max_incoming}` : ''}
                {type.max_outgoing ? ` · maximum outgoing ${type.max_outgoing}` : ''}
              </p>
              {!!type.attributes?.fields.length && (
                <details>
                  <summary className="text-sm">Attributes</summary>
                  <ul>
                    {type.attributes.fields.map((field) => (
                      <li key={field.id} className="text-xs">
                        {field.label} · {field.type}
                        {field.required ? ' · required' : ''}
                      </li>
                    ))}
                  </ul>
                </details>
              )}
            </li>
          ))}
        </ul>
      </SettingsSection>
      {creating && <CreateRelationshipType onClose={() => setCreating(false)} />}
    </>
  );
}

/** Direction and limits form from PaperDotNet's RelatedItems type dialog. */
export function CreateRelationshipType({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated?: (type: RelationshipType) => void;
}) {
  const client = useQueryClient();
  const [name, setName] = useState('');
  const [directed, setDirected] = useState(false);
  const [inverse, setInverse] = useState('');
  const [incoming, setIncoming] = useState('');
  const [outgoing, setOutgoing] = useState('');
  const [attributes, setAttributes] = useState<unknown[]>([]);
  const create = useMutation({
    mutationFn: () =>
      api<RelationshipType>('/v1/relationship-types', {
        method: 'POST',
        body: {
          name: name.trim(),
          directed,
          inverse_label: directed ? inverse : '',
          max_incoming: directed && incoming ? Number(incoming) : undefined,
          max_outgoing: directed && outgoing ? Number(outgoing) : undefined,
          attributes,
        },
      }),
    onSuccess: async (result) => {
      await client.invalidateQueries({ queryKey: relationshipTypesQuery.queryKey });
      await client.invalidateQueries({ queryKey: ['termStore'] });
      onCreated?.(result.data);
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title="Create relationship type"
      description="Types use the shared term store. Direction and limits stay fixed once created."
      submitLabel="Create type"
      pending={create.isPending}
      onClose={onClose}
      onSubmit={() => create.mutate()}
    >
      <Field id="relationship-name" label="Name">
        <Input id="relationship-name" required value={name} onChange={(event) => setName(event.target.value)} />
      </Field>
      <Field id="relationship-direction" label="Direction">
        <Select
          id="relationship-direction"
          value={directed ? 'directed' : 'symmetric'}
          onChange={(event) => setDirected(event.target.value === 'directed')}
        >
          <option value="symmetric">Symmetric</option>
          <option value="directed">Directed</option>
        </Select>
      </Field>
      {directed && (
        <>
          <Field id="relationship-inverse" label="Inverse label">
            <Input id="relationship-inverse" value={inverse} onChange={(event) => setInverse(event.target.value)} />
          </Field>
          <Field id="relationship-incoming" label="Maximum incoming links per item">
            <Input
              id="relationship-incoming"
              type="number"
              min={1}
              max={1000}
              value={incoming}
              onChange={(event) => setIncoming(event.target.value)}
            />
          </Field>
          <Field id="relationship-outgoing" label="Maximum outgoing links per item">
            <Input
              id="relationship-outgoing"
              type="number"
              min={1}
              max={1000}
              value={outgoing}
              onChange={(event) => setOutgoing(event.target.value)}
            />
          </Field>
        </>
      )}
      <details>
        <summary className="cursor-pointer text-sm">Attribute definitions</summary>
        <p className="my-2 text-xs text-muted">
          Optional scalar fields. Each definition has key, label, type and optional required, choices, scale and
          options. Reference fields, indexing and unique values are unsupported.
        </p>
        <JsonInput
          label="Relationship attribute definitions"
          value={attributes}
          onChange={(value) => {
            if (!Array.isArray(value)) throw new Error('Attributes must be an array.');
            setAttributes(value);
          }}
        />
      </details>
    </FormDialog>
  );
}
