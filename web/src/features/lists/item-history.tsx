import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { stringifyJSON } from '@/api/json';
import { useApiPages } from '@/api/pages';
import type { FieldDefinition, Resource, ValidationRule } from '@/api/types';
import { Button } from '@/components/ui/button';
import { SystemProperties } from './system-properties';
import { Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { useTranslation } from '@/i18n';
import { useFormat } from '@/lib/preferences';

interface Publication {
  id: string;
  action: string;
  version: number;
  revision_id?: string;
  created_at: string;
  published_by: string;
  snapshot?: unknown;
}
export function Publications({ itemId }: { itemId: string }) {
  const { t } = useTranslation('lists');
  const format = useFormat();
  const history = useApiPages<Publication>(['publications', itemId], `/v1/items/${itemId}/publications`);
  return (
    <section className="grid gap-2 border-t pt-3">
      <h3 className="font-medium">{t('history.title')}</h3>
      <QueryFeedback query={history} />
      <ol className="divide-y">
        {history.items.map((event) => (
          <li key={event.id} className="py-2 text-sm">
            <p className="font-medium">
              {event.action === 'publish' ? t('history.published', { version: event.version }) : t('history.unpublished', { version: event.version })}
            </p>
            <p className="text-xs text-muted">
              {format.dateTime(event.created_at)} · {event.published_by}
            </p>
            {event.snapshot !== undefined && (
              <details>
                <summary className="cursor-pointer text-xs">{t('history.snapshot')}</summary>
                <pre className="overflow-auto whitespace-pre-wrap text-xs">{stringifyJSON(event.snapshot)}</pre>
              </details>
            )}
          </li>
        ))}
      </ol>
      {history.isSuccess && !history.items.length && <p className="text-sm text-muted">{t('history.none')}</p>}
      {history.hasNextPage && (
        <Button disabled={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>
          {t('history.more')}
        </Button>
      )}
    </section>
  );
}

interface ItemSchema {
  item: Resource;
  file?: { filename: string; content_type: string; size: number };
  revision_number: number;
  created_at: string;
  created_by: string;
  definition: {
    fields: (Omit<FieldDefinition, 'key'> & { id: string; definition_id?: string })[];
    content_types?: { id: string; name: string; field_keys: string[]; rules?: ValidationRule[] }[];
  };
}
export function ItemSchemaView({ itemId }: { itemId: string }) {
  const { t } = useTranslation('lists');
  const [surface, setSurface] = useState('auto');
  const schema = useQuery({
    queryKey: ['resources', itemId, 'schema', surface],
    queryFn: async () => (await api<ItemSchema>(`/v1/items/${itemId}/schema`, { query: { surface } })).data,
  });
  return (
    <div className="grid gap-3">
      <Select aria-label={t('schema.surface')} value={surface} onChange={(event) => setSurface(event.target.value)}>
        <option value="auto">{t('schema.visible')}</option>
        <option value="head">{t('schema.draft')}</option>
        <option value="published">{t('publish.Published')}</option>
      </Select>
      <QueryFeedback query={schema} />
      {schema.data && (
        <>
          <p className="text-sm">{t('schema.revision', { revision: schema.data.revision_number })}</p>
          <SystemProperties item={schema.data.item} file={schema.data.file} contentType={schema.data.definition.content_types?.find(type => type.id === schema.data!.item.content_type_id)?.name} keywords={schema.data.definition.fields.filter(field => field.type === 'keywords').flatMap(field => { const value = schema.data!.item.values?.[field.id]; return Array.isArray(value) ? value.filter((id): id is string => typeof id === 'string') : []; })} />
          <h3 className="font-medium">{t('schema.customFields')}</h3>
          {!schema.data.definition.fields.length && <p className="text-sm text-muted">{t('schema.noCustomFields')}</p>}
          <ul className="divide-y">
            {schema.data.definition.fields.map((field) => (
              <li key={field.id} className="py-2">
                <p className="font-medium">{field.label}</p>
                <p className="text-xs text-muted">
                  {field.id} · {field.type}
                  {field.required ? ` · ${t('schema.required')}` : ''}
                  {field.indexed ? ` · ${t('schema.indexed')}` : ''}
                </p>
                {field.options?.description && <p className="text-sm text-muted">{field.options.description}</p>}
              </li>
            ))}
          </ul>
          {schema.data.definition.content_types?.map((type) => (
            <details key={type.id}>
              <summary className="cursor-pointer text-sm">{type.name}</summary>
              <p className="text-xs">{t('schema.fields', { fields: type.field_keys.join(', ') })}</p>
              {!!type.rules?.length && <pre className="whitespace-pre-wrap text-xs">{stringifyJSON(type.rules)}</pre>}
            </details>
          ))}
          <details>
            <summary className="cursor-pointer text-xs">{t('schema.full')}</summary>
            <pre className="overflow-auto whitespace-pre-wrap text-xs">{stringifyJSON(schema.data.definition)}</pre>
          </details>
        </>
      )}
    </div>
  );
}
