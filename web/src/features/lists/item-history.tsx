import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { stringifyJSON } from '@/api/json';
import { useApiPages } from '@/api/pages';
import type { FieldDefinition, ValidationRule } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
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
  const format = useFormat();
  const history = useApiPages<Publication>(['publications', itemId], `/v1/items/${itemId}/publications`);
  return (
    <section className="grid gap-2 border-t pt-3">
      <h3 className="font-medium">Publication history</h3>
      <QueryFeedback query={history} />
      <ol className="divide-y">
        {history.items.map((event) => (
          <li key={event.id} className="py-2 text-sm">
            <p className="font-medium">
              {event.action === 'publish' ? 'Published' : 'Unpublished'} · resource version {event.version}
            </p>
            <p className="text-xs text-muted">
              {format.dateTime(event.created_at)} · {event.published_by}
            </p>
            {event.snapshot !== undefined && (
              <details>
                <summary className="cursor-pointer text-xs">Published snapshot</summary>
                <pre className="overflow-auto whitespace-pre-wrap text-xs">{stringifyJSON(event.snapshot)}</pre>
              </details>
            )}
          </li>
        ))}
      </ol>
      {history.isSuccess && !history.items.length && <p className="text-sm text-muted">No publication events.</p>}
      {history.hasNextPage && (
        <Button disabled={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>
          Load more publications
        </Button>
      )}
    </section>
  );
}

interface ItemSchema {
  revision_number: number;
  created_at: string;
  created_by: string;
  definition: {
    fields: (Omit<FieldDefinition, 'key'> & { id: string; definition_id?: string })[];
    content_types?: { id: string; name: string; field_keys: string[]; rules?: ValidationRule[] }[];
  };
}
export function ItemSchemaView({ itemId }: { itemId: string }) {
  const [surface, setSurface] = useState('auto');
  const schema = useQuery({
    queryKey: ['resources', itemId, 'schema', surface],
    queryFn: async () => (await api<ItemSchema>(`/v1/items/${itemId}/schema`, { query: { surface } })).data,
  });
  return (
    <div className="grid gap-3">
      <Select aria-label="Schema surface" value={surface} onChange={(event) => setSurface(event.target.value)}>
        <option value="auto">Visible version</option>
        <option value="head">Current draft</option>
        <option value="published">Published</option>
      </Select>
      <QueryFeedback query={schema} />
      {schema.data && (
        <>
          <p className="text-sm">Schema revision {schema.data.revision_number}</p>
          <ul className="divide-y">
            {schema.data.definition.fields.map((field) => (
              <li key={field.id} className="py-2">
                <p className="font-medium">{field.label}</p>
                <p className="text-xs text-muted">
                  {field.id} · {field.type}
                  {field.required ? ' · required' : ''}
                  {field.indexed ? ' · indexed' : ''}
                </p>
                {field.options?.description && <p className="text-sm text-muted">{field.options.description}</p>}
              </li>
            ))}
          </ul>
          {schema.data.definition.content_types?.map((type) => (
            <details key={type.id}>
              <summary className="cursor-pointer text-sm">{type.name}</summary>
              <p className="text-xs">Fields: {type.field_keys.join(', ')}</p>
              {!!type.rules?.length && <pre className="whitespace-pre-wrap text-xs">{stringifyJSON(type.rules)}</pre>}
            </details>
          ))}
          <details>
            <summary className="cursor-pointer text-xs">Full schema definition</summary>
            <pre className="overflow-auto whitespace-pre-wrap text-xs">{stringifyJSON(schema.data.definition)}</pre>
          </details>
        </>
      )}
    </div>
  );
}
