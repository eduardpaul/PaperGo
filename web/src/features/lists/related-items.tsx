import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { useApiPages } from '@/api/pages';
import type { FieldDefinition, Relationship, Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Combobox } from '@/components/ui/combobox';
import { Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { CreateRelationshipType, relationshipTypesQuery } from '@/features/admin/relationship-types';
import { QueryFeedback, useHasScope } from '@/features/admin/common';
import { FieldEditor } from '@/features/fields/editor';
import { valuesPatch } from '@/features/fields/values';

export function RelatedItems({ item, canWrite }: { item: Resource; canWrite: boolean }) {
  const client = useQueryClient();
  const types = useQuery(relationshipTypesQuery);
  const canCreate = useHasScope('item.write');
  const [typeId, setTypeId] = useState('');
  const [filterType, setFilterType] = useState('');
  const [direction, setDirection] = useState('both');
  const [directed, setDirected] = useState(false);
  const [q, setQ] = useState('');
  const [target, setTarget] = useState('');
  const [creating, setCreating] = useState(false);
  const [metadata, setMetadata] = useState<Record<string, unknown>>({});
  const selectedType = types.data?.find((type) => type.id === typeId);
  const fields: FieldDefinition[] =
    selectedType?.attributes?.fields.map((field) => ({ ...field, key: field.id })) ?? [];
  const links = useApiPages<Relationship>(['relationships', item.id], `/v1/items/${item.id}/relationships`, {
    query: { type: filterType, direction },
  });
  const matches = useApiPages<Resource>(['resources', item.workspace_id, 'link-targets'], '/v1/resources', {
    enabled: !!q,
    query: { workspace_id: item.workspace_id, q },
  });
  const options = matches.items
    .filter((resource) => resource.kind === 'item' && resource.id !== item.id)
    .map((resource) => ({ value: resource.id, label: resource.name }));
  const create = useMutation({
    mutationFn: () =>
      api(`/v1/items/${item.id}/relationships`, {
        method: 'POST',
        body: {
          type: typeId || undefined,
          directed: selectedType ? selectedType.directed : directed,
          target_id: target.trim(),
          metadata: valuesPatch(fields, {}, metadata) ?? {},
        },
      }),
    onSuccess: async () => {
      setTarget('');
      setMetadata({});
      await client.invalidateQueries({ queryKey: ['relationships', item.id] });
    },
  });
  const remove = useMutation({
    mutationFn: (link: Relationship) => api(`/v1/items/${item.id}/relationships/${link.id}`, { method: 'DELETE' }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['relationships', item.id] }),
  });
  return (
    <div className="grid gap-3">
      <Select
        aria-label="Filter relationship type"
        value={filterType}
        onChange={(event) => setFilterType(event.target.value)}
      >
        <option value="">All types</option>
        {types.data?.map((type) => (
          <option key={type.id} value={type.id}>
            {type.term.name}
          </option>
        ))}
      </Select>
      <Select
        aria-label="Filter relationship direction"
        value={direction}
        onChange={(event) => setDirection(event.target.value)}
      >
        <option value="both">Both directions</option>
        <option value="outgoing">Outgoing</option>
        <option value="incoming">Incoming</option>
      </Select>
      <QueryFeedback query={links} />
      <ul className="divide-y">
        {links.items.map((link) => (
          <li key={link.id} className="flex items-center gap-2 py-2">
            <div className="flex-1">
              <p className="font-medium">{link.related_item?.name ?? link.target_id}</p>
              <p className="text-xs text-muted">
                {types.data?.find((type) => type.id === link.type_id)?.term.name ?? 'Related'} ·{' '}
                {link.directed ? 'Directed' : 'Symmetric'}
              </p>
            </div>
            {canWrite && (
              <Button size="sm" variant="ghost" disabled={remove.isPending} onClick={() => remove.mutate(link)}>
                Remove
              </Button>
            )}
          </li>
        ))}
      </ul>
      {!links.isPending && links.items.length === 0 && <p className="text-sm text-muted">No links yet.</p>}
      {links.hasNextPage && (
        <Button onClick={() => void links.fetchNextPage()} disabled={links.isFetchingNextPage}>
          More links
        </Button>
      )}
      {canWrite && (
      <form
        className="grid gap-2 border-t pt-3"
        onSubmit={(event) => {
          event.preventDefault();
          create.mutate();
        }}
      >
        <Label htmlFor="link-type">Relationship type</Label>
        <Select
          id="link-type"
          value={typeId}
          onChange={(event) => {
            setTypeId(event.target.value);
            setMetadata({});
          }}
        >
          <option value="">Related (untyped)</option>
          {types.data?.map((type) => (
            <option key={type.id} value={type.id}>
              {type.term.name}
            </option>
          ))}
        </Select>
        {canCreate && <Button onClick={() => setCreating(true)}>New relationship type</Button>}
        {!typeId && (
          <label className="flex gap-2">
            <input type="checkbox" checked={directed} onChange={(event) => setDirected(event.target.checked)} />
            Directed link
          </label>
        )}
        <Label id="link-target-label">Item to link</Label>
        <Combobox
          aria-labelledby="link-target-label"
          selected={
            target ? [options.find((option) => option.value === target) ?? { value: target, label: target }] : []
          }
          options={options}
          onSearch={setQ}
          onChange={(options) => setTarget(options[0]?.value ?? '')}
        />
        {q && <QueryFeedback query={matches} />}
        {matches.hasNextPage && <Button onClick={() => void matches.fetchNextPage()}>More target items</Button>}
        {fields.map((field) => (
          <FieldEditor
            key={field.key}
            field={field}
            value={metadata[field.key]}
            onChange={(value) => setMetadata((current) => ({ ...current, [field.key]: value }))}
          />
        ))}
        <Button type="submit" disabled={!target.trim() || create.isPending}>
          Add link
        </Button>
      </form>
      )}
      {creating && (
        <CreateRelationshipType onClose={() => setCreating(false)} onCreated={(type) => setTypeId(type.id)} />
      )}
    </div>
  );
}
