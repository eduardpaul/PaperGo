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
import { useTranslation } from '@/i18n';
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
  const { t } = useTranslation('admin');
  const listRead = useHasScope('item.read');
  const taxonomyRead = useHasScope('taxonomy.read');
  const canRead = listRead && taxonomyRead;
  const canCreate = useHasScope('item.write');
  const types = useQuery({ ...relationshipTypesQuery, enabled: canRead });
  const [creating, setCreating] = useState(false);
  if (!canRead) return <p>{t('relationshipTypes.needRead')}</p>;
  return (
    <>
      <SettingsSection
        title={t('relationshipTypes.title')}
        description={t('relationshipTypes.description')}
        actions={canCreate && <Button onClick={() => setCreating(true)}>{t('relationshipTypes.new')}</Button>}
      >
        <QueryFeedback query={types} />
        <ul className="divide-y">
          {types.data?.map((type) => (
            <li key={type.id} className="py-3">
              <h3 className="font-medium">{type.term.name}</h3>
              <p className="text-xs text-muted">
                {[
                  type.directed ? t('relationshipTypes.directed') : t('relationshipTypes.symmetric'),
                  type.directed && type.inverse_label ? t('relationshipTypes.inverse', { label: type.inverse_label }) : '',
                  type.max_incoming ? t('relationshipTypes.maxIncoming', { max: type.max_incoming }) : '',
                  type.max_outgoing ? t('relationshipTypes.maxOutgoing', { max: type.max_outgoing }) : '',
                ]
                  .filter(Boolean)
                  .join(' · ')}
              </p>
              {!!type.attributes?.fields.length && (
                <details>
                  <summary className="text-sm">{t('relationshipTypes.attributes')}</summary>
                  <ul>
                    {type.attributes.fields.map((field) => (
                      <li key={field.id} className="text-xs">
                        {field.label} · {field.type}
                        {field.required ? ` · ${t('relationshipTypes.required')}` : ''}
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
  const { t } = useTranslation(['admin', 'common']);
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
      title={t('relationshipTypes.create')}
      description={t('relationshipTypes.createDescription')}
      submitLabel={t('relationshipTypes.createType')}
      pending={create.isPending}
      onClose={onClose}
      onSubmit={() => create.mutate()}
    >
      <Field id="relationship-name" label={t('common:fields.name')}>
        <Input id="relationship-name" required value={name} onChange={(event) => setName(event.target.value)} />
      </Field>
      <Field id="relationship-direction" label={t('relationshipTypes.direction')}>
        <Select
          id="relationship-direction"
          value={directed ? 'directed' : 'symmetric'}
          onChange={(event) => setDirected(event.target.value === 'directed')}
        >
          <option value="symmetric">{t('relationshipTypes.symmetric')}</option>
          <option value="directed">{t('relationshipTypes.directed')}</option>
        </Select>
      </Field>
      {directed && (
        <>
          <Field id="relationship-inverse" label={t('relationshipTypes.inverseLabel')}>
            <Input id="relationship-inverse" value={inverse} onChange={(event) => setInverse(event.target.value)} />
          </Field>
          <Field id="relationship-incoming" label={t('relationshipTypes.incoming')}>
            <Input
              id="relationship-incoming"
              type="number"
              min={1}
              max={1000}
              value={incoming}
              onChange={(event) => setIncoming(event.target.value)}
            />
          </Field>
          <Field id="relationship-outgoing" label={t('relationshipTypes.outgoing')}>
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
        <summary className="cursor-pointer text-sm">{t('relationshipTypes.definitions')}</summary>
        <p className="my-2 text-xs text-muted">
          {t('relationshipTypes.definitionsHint')}
        </p>
        <JsonInput
          label={t('relationshipTypes.definitionsLabel')}
          value={attributes}
          onChange={(value) => {
            if (!Array.isArray(value)) throw new Error(t('relationshipTypes.notArray'));
            setAttributes(value);
          }}
        />
      </details>
    </FormDialog>
  );
}
