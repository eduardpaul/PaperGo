import { useQuery } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { api } from '@/api/http';
import { parseJSON, stringifyJSON } from '@/api/json';
import { allPages } from '@/api/pages';
import type { Page, Resource, SearchContentType, Term } from '@/api/types';
import { peopleQuery } from '@/api/queries';
import { Button } from '@/components/ui/button';
import { Combobox } from '@/components/ui/combobox';
import { Input, Label, Textarea } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { typeTree } from '@/features/lists/schema';
import { formDefaults } from './model';
import type { FormSchema } from './model';

export function JsonInput({
  value,
  onChange,
  label,
}: {
  value: unknown;
  onChange: (value: unknown) => void;
  label: string;
}) {
  const [text, setText] = useState(stringifyJSON(value) ?? '');
  const [error, setError] = useState('');
  return (
    <div>
      <Textarea
        aria-label={label}
        className="font-mono text-xs"
        rows={5}
        value={text}
        onChange={(event) => {
          setText(event.target.value);
          try {
            const next = parseJSON(event.target.value);
            onChange(next);
            event.target.setCustomValidity('');
            setError('');
          } catch (cause) {
            const message = cause instanceof Error ? cause.message : 'Enter valid JSON.';
            event.target.setCustomValidity(message);
            setError(message);
          }
        }}
      />
      {error && <p className="mt-1 text-xs text-danger">{error}</p>}
    </div>
  );
}

export function SchemaFields({
  schema,
  data,
  onChange,
  workspaceId,
}: {
  schema: FormSchema;
  data: Record<string, unknown>;
  onChange: (data: Record<string, unknown>) => void;
  workspaceId: string;
}) {
  return (
    <div className="grid gap-3">
      {Object.entries(schema.properties ?? {}).map(([key, property]) => (
        <SchemaValue
          key={key}
          schema={property}
          value={data[key]}
          label={property.title ?? key}
          required={schema.required?.includes(key)}
          workspaceId={workspaceId}
          onChange={(value) => {
            const next = { ...data };
            if (value === undefined) delete next[key];
            else next[key] = value;
            onChange(next);
          }}
        />
      ))}
    </div>
  );
}

function SchemaValue({
  schema,
  value,
  onChange,
  label,
  required,
  workspaceId,
}: {
  schema: FormSchema;
  value: unknown;
  onChange: (value: unknown) => void;
  label: string;
  required?: boolean;
  workspaceId: string;
}) {
  const id = useId();
  const present = value !== undefined || required;
  const empty =
    schema.default ??
    (schema.type === 'object'
      ? formDefaults(schema)
      : schema.type === 'array'
        ? []
        : schema.type === 'boolean'
          ? false
          : '');
  return (
    <div className="grid gap-1.5">
      <div className="flex items-center gap-2">
        <Label id={`${id}-label`} htmlFor={id}>
          {label}
          {required && ' *'}
        </Label>
        {!required && (
          <label className="ml-auto flex gap-1 text-xs">
            <Checkbox
              disabled={schema.readOnly}
              checked={value !== undefined}
              onChange={(event) => onChange(event.target.checked ? empty : undefined)}
            />
            Include
          </label>
        )}
      </div>
      {schema.description && <p className="text-xs text-muted">{schema.description}</p>}
      <fieldset disabled={!present || schema.readOnly}>
        {schema['x-papergo'] ? (
          <DomainPicker
            id={id}
            disabled={!present || schema.readOnly}
            schema={schema}
            value={value}
            onChange={onChange}
            workspaceId={workspaceId}
          />
        ) : schema.enum ? (
          <Select
            id={id}
            value={schema.enum.findIndex((option) => stringifyJSON(option) === stringifyJSON(value))}
            required={required}
            onChange={(event) => onChange(schema.enum![Number(event.target.value)])}
          >
            <option value={-1}>Choose…</option>
            {schema.enum.map((option, index) => (
              <option key={index} value={index}>
                {String(option)}
              </option>
            ))}
          </Select>
        ) : schema.type === 'boolean' ? (
          <label className="flex gap-2">
            <Checkbox id={id} checked={value === true} onChange={(event) => onChange(event.target.checked)} />
            Yes
          </label>
        ) : schema.type === 'object' && schema.properties && !schema.additionalProperties ? (
          <SchemaFields
            schema={schema}
            data={(value as Record<string, unknown>) ?? {}}
            workspaceId={workspaceId}
            onChange={onChange}
          />
        ) : schema.type === 'array' ? (
          <ArrayValues schema={schema} value={value} onChange={onChange} label={label} workspaceId={workspaceId}/>
        ) : schema.type === 'object' ? (
          <JsonInput value={value ?? {}} onChange={onChange} label={label} />
        ) : (
          <Input
            id={id}
            type={schema.format === 'date' ? 'date' : schema.format === 'email' ? 'email' : 'text'}
            inputMode={schema.type === 'number' || schema.type === 'integer' ? 'decimal' : undefined}
            required={required}
            minLength={schema.minLength}
            maxLength={schema.maxLength}
            value={String(value ?? '')}
            onChange={(event) => onChange(event.target.value)}
          />
        )}
      </fieldset>
    </div>
  );
}

function DomainPicker({
  schema,
  value,
  onChange,
  workspaceId,
  id,
  disabled,
}: {
  id: string;
  disabled?: boolean;
  schema: FormSchema;
  value: unknown;
  onChange: (value: unknown) => void;
  workspaceId: string;
}) {
  const domain = schema['x-papergo']!;
  const [text, setText] = useState('');
  const people = useQuery({ ...peopleQuery, enabled: domain.kind === 'people' });
  const candidates = useQuery({
    queryKey: ['workflow-inputs', workspaceId, domain, text],
    enabled: domain.kind !== 'people',
    queryFn: async () => {
      if (domain.kind === 'keywords')
        return (await api<{ value: Term[] }>('/v1/keywords', { query: { q: text, $top: 50 } })).data.value.map(
          (term) => ({ value: term.id, label: term.name }),
        );
      if (domain.kind === 'content_type') {
        // Catalog types the workspace (or the collection) uses, as a tree.
        const types = (
          await api<SearchContentType[]>('/v1/search/content-types', {
            query: { workspace_id: workspaceId, collection_id: domain.collection_id },
          })
        ).data;
        const byId = new Map(types.map((type) => [type.id, type]));
        const path = (type: SearchContentType): string => {
          const parent = type.parent_id ? byId.get(type.parent_id) : undefined;
          return parent ? `${path(parent)} › ${type.name}` : type.name;
        };
        return typeTree(types)
          .map(({ type }) => ({ value: type.id, label: path(type) }))
          .filter((option) => option.label.toLowerCase().includes(text.toLowerCase()));
      }
      if (domain.kind === 'terms') {
        if (domain.select === 'sets')
          return (await allPages<{ id: string; name: string }>('/v1/term-sets', { group_id: domain.group_id }))
            .filter((set) => set.name.toLowerCase().includes(text.toLowerCase()))
            .map((set) => ({ value: set.id, label: set.name }));
        if (domain.term_ids?.length)
          return (
            await api<{ value: Term[] }>('/v1/terms', { query: { ids: domain.term_ids.join(',') } })
          ).data.value.map((term) => ({ value: term.id, label: term.name }));
        const setIds = domain.term_set_id
          ? [domain.term_set_id]
          : (await allPages<{ id: string }>('/v1/term-sets', { group_id: domain.group_id })).map((set) => set.id);
        return (await Promise.all(setIds.map((setId) => allPages<Term>(`/v1/term-sets/${setId}/terms`, { q: text }))))
          .flat()
          .map((term) => ({ value: term.id, label: term.name }));
      }
      const resources = domain.collection_id
        ? ((
            await api<Page<Resource>>(`/v1/resources/${domain.collection_id}/query`, {
              method: 'POST',
              query: { $top: 100 },
              body: { query: { search: text || undefined } },
            })
          ).data.value ?? [])
        : ((
            await api<Page<Resource>>('/v1/resources', {
              query: { workspace_id: workspaceId, q: text || undefined, $top: 200 },
            })
          ).data.value ?? []);
      return resources
        .filter((resource) =>
          domain.kind === 'collection'
            ? resource.kind === 'list' || resource.kind === 'library'
            : resource.kind === 'item',
        )
        .map((resource) => ({ value: resource.id, label: resource.name }));
    },
  });
  const options =
    domain.kind === 'people'
      ? (people.data ?? []).map((subject) => ({ value: subject, label: subject }))
      : (candidates.data ?? []);
  const selected = (Array.isArray(value) ? value : value ? [value] : []).map(
    (id) => options.find((option) => option.value === id) ?? { value: String(id), label: String(id) },
  );
  return (
    <Combobox
      aria-labelledby={`${id}-label`}
      id={id}
      disabled={disabled}
      multiple={schema.type === 'array'}
      selected={selected}
      options={options}
      loading={candidates.isFetching || people.isFetching}
      onSearch={setText}
      onChange={(values) => onChange(schema.type === 'array' ? values.map((option) => option.value) : values[0]?.value)}
    />
  );
}

function ArrayValues({schema,value,onChange,label,workspaceId}: {
 schema:FormSchema; value:unknown; onChange:(value:unknown)=>void; label:string; workspaceId:string;
}) {
 const prefix=useId();
 const entries=Array.isArray(value)?value:[];
 const [ids,setIds]=useState(()=>entries.map((_,i)=>`${prefix}:${i}`));
 const [serial,setSerial]=useState(entries.length);
 function move(index:number,offset:number) {
  const next=[...entries],keys=[...ids];
  [next[index],next[index+offset]]=[next[index+offset],next[index]];
  [keys[index],keys[index+offset]]=[keys[index+offset],keys[index]];
  setIds(keys);onChange(next);
 }
 return <div className="grid gap-2">
  {entries.map((entry,index)=><div key={ids[index]??`${prefix}:external:${index}`} className="rounded border p-2">
   <SchemaValue schema={schema.items??{type:'string'}} value={entry}
    onChange={next=>onChange(entries.map((current,i)=>i===index?next:current))}
    label={`${label} ${index+1}`} required workspaceId={workspaceId}/>
   <div className="flex gap-2">
    <Button size="sm" disabled={index===0} onClick={()=>move(index,-1)}>Move up</Button>
    <Button size="sm" disabled={index===entries.length-1} onClick={()=>move(index,1)}>Move down</Button>
    <Button size="sm" onClick={()=>{setIds(ids.filter((_,i)=>i!==index));onChange(entries.filter((_,i)=>i!==index))}}>Remove</Button>
   </div>
  </div>)}
  <Button size="sm" disabled={schema.maxItems!==undefined && entries.length>=schema.maxItems} onClick={()=>{
   setIds([...ids,`${prefix}:${serial}`]);setSerial(serial+1);
   onChange([...entries,schema.items?.default??(schema.items?.type==='object'?formDefaults(schema.items):schema.items?.type==='boolean'?false:schema.items?.type==='array'?[]:'')]);
  }}>Add {label.toLowerCase()}</Button>
 </div>;
}
