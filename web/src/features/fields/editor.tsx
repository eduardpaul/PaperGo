import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { keywords, peopleQuery, terms } from '@/api/queries';
import { lookupOptionsRequest } from '@/api/requests';
import type { FieldDefinition, Page, Resource } from '@/api/types';
import { Combobox, type ComboboxOption } from '@/components/ui/combobox';
import { Input, Label, Textarea } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { useDebounced } from '@/lib/use-debounced';
import { useResourceNames, useTermNames } from './names';
import { usePeopleNames } from './people';
import { asList, fieldText } from './values';

export function FieldEditor({
  field,
  value,
  onChange,
}: {
  field: FieldDefinition;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  const multiple = field.options?.multiple === true;
  const id = `field-${field.key}`;
  return (
    <div className="flex flex-col gap-1.5">
      <Label id={`${id}-label`} htmlFor={id}>
        {field.label}
        {field.required ? <span className="text-danger"> *</span> : null}
      </Label>
      {field.options?.description && <p className="text-xs text-muted">{field.options.description}</p>}
      <Control id={id} field={field} multiple={multiple} value={value} onChange={onChange} />
    </div>
  );
}

function Control({
  id,
  field,
  multiple,
  value,
  onChange,
}: {
  id: string;
  field: FieldDefinition;
  multiple: boolean;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  if (field.type === 'boolean') {
    return (
      <label className="flex items-center gap-2 text-[13px]">
        <input id={id} type="checkbox" checked={value === true} onChange={(event) => onChange(event.target.checked)} />
        Yes
      </label>
    );
  }
  if (field.type === 'note') {
    return <Textarea id={id} value={fieldText(value)} onChange={(event) => onChange(event.target.value)} />;
  }
  if (field.type === 'choice') {
    return <ChoiceEditor id={id} choices={field.choices ?? []} multiple={multiple} value={value} onChange={onChange} />;
  }
  if (field.type === 'person') return <PersonEditor id={id} multiple={multiple} value={value} onChange={onChange} />;
  if (field.type === 'lookup' && field.options?.lookup_container_id) {
    return <LookupEditor id={id} containerId={field.options.lookup_container_id} multiple={multiple} value={value} onChange={onChange} />;
  }
  if ((field.type === 'term' && field.options?.term_set_id) || field.type === 'keywords') {
    return (
      <TermEditor
        id={id}
        setId={field.type === 'term' ? field.options?.term_set_id : undefined}
        keywords={field.type === 'keywords'}
        multiple={multiple}
        value={value}
        onChange={onChange}
      />
    );
  }
  if (field.type === 'date') {
    return <Input id={id} type="date" value={fieldText(value).slice(0, 10)} onChange={(event) => onChange(event.target.value)} />;
  }
  if (field.type === 'datetime') {
    return (
      <Input
        id={id}
        type="datetime-local"
        value={toLocalInput(fieldText(value))}
        onChange={(event) => onChange(event.target.value ? new Date(event.target.value).toISOString() : '')}
      />
    );
  }
  const numeric = field.type === 'number' || field.type === 'integer';
  return (
    <Input
      id={id}
      type={field.type === 'email' ? 'email' : field.type === 'url' ? 'url' : numeric ? 'text' : 'text'}
      inputMode={field.type === 'integer' ? 'numeric' : field.type === 'number' || field.type === 'decimal' || field.type === 'currency' ? 'decimal' : undefined}
      value={fieldText(value)}
      onChange={(event) => onChange(event.target.value)}
    />
  );
}

function toLocalInput(value: string): string {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  const pad = (part: number) => String(part).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function ChoiceEditor({
  id,
  choices,
  multiple,
  value,
  onChange,
}: {
  id: string;
  choices: string[];
  multiple: boolean;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  if (!multiple) {
    return (
      <Select id={id} value={fieldText(value)} onChange={(event) => onChange(event.target.value)}>
        <option value="">Select…</option>
        {choices.map((choice) => (
          <option key={choice} value={choice}>
            {choice}
          </option>
        ))}
      </Select>
    );
  }
  const selected = new Set(asList(value));
  return (
    <div className="flex flex-col gap-1">
      {choices.map((choice) => (
        <label key={choice} className="flex items-center gap-2 text-[13px]">
          <input
            type="checkbox"
            checked={selected.has(choice)}
            onChange={(event) => {
              const next = new Set(selected);
              if (event.target.checked) next.add(choice);
              else next.delete(choice);
              onChange([...next]);
            }}
          />
          {choice}
        </label>
      ))}
    </div>
  );
}

function PersonEditor({ id, multiple, value, onChange }: { id: string; multiple: boolean; value: unknown; onChange: (value: unknown) => void }) {
  const people = useQuery(peopleQuery);
  const names = usePeopleNames();
  const subjects = people.data ?? [...names.keys()];
  const option = (subject: string): ComboboxOption => ({ value: subject, label: names.get(subject) ?? subject });
  const selected = asList(value);
  return (
    <Combobox
      id={id}
      aria-labelledby={`${id}-label`}
      multiple={multiple}
      placeholder="Select a person…"
      loading={people.isPending}
      selected={selected.map(option)}
      options={subjects.map(option)}
      onChange={(chosen) => onChange(multiple ? chosen.map((entry) => entry.value) : (chosen[0]?.value ?? ''))}
    />
  );
}

/** Items of the looked-up list, searched by name as people type, however many it holds. */
function LookupEditor({
  id,
  containerId,
  multiple,
  value,
  onChange,
}: {
  id: string;
  containerId: string;
  multiple: boolean;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  const [text, setText] = useState('');
  const search = useDebounced(text);
  const call = lookupOptionsRequest(containerId, search);
  const found = useQuery({
    queryKey: ['resources', containerId, 'lookup-options', search],
    queryFn: async () => (await api<Page<Resource>>(call.path, { method: call.method, query: call.query, body: call.body })).data,
    placeholderData: keepPreviousData,
  });
  const selected = asList(value);
  // Names of the current values come by id, wherever they sit in the looked-up list.
  const names = useResourceNames([...selected].sort());
  const options = (found.data?.value ?? []).map((item) => ({ value: item.id, label: item.name }));
  const labels = new Map(options.map((entry) => [entry.value, entry.label]));
  return (
    <Combobox
      id={id}
      aria-labelledby={`${id}-label`}
      multiple={multiple}
      loading={found.isFetching}
      selected={selected.map((item) => ({ value: item, label: names?.get(item) ?? labels.get(item) ?? 'Loading…' }))}
      options={options}
      onSearch={setText}
      onChange={(chosen) => onChange(multiple ? chosen.map((entry) => entry.value) : (chosen[0]?.value ?? ''))}
      footer={found.data?.['@odata.nextLink'] ? 'More items match. Type to narrow the list.' : undefined}
    />
  );
}

function TermEditor({
  id,
  setId,
  keywords: keywordField,
  multiple,
  value,
  onChange,
}: {
  id: string;
  setId?: string;
  keywords: boolean;
  multiple: boolean;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  const [text, setText] = useState('');
  const [options, setOptions] = useState<{ id: string; name: string }[]>([]);
  // Names of terms picked here; others come from the term store.
  const [picked, setPicked] = useState<Map<string, string>>(new Map());
  const selected = asList(value);
  const stored = useTermNames([...selected].sort());
  const nameOf = (termId: string) => picked.get(termId) ?? stored?.get(termId) ?? termId;

  async function search(next: string) {
    setText(next);
    if (next.trim().length < 1) {
      setOptions([]);
      return;
    }
    const found = keywordField ? await keywords(next.trim()) : setId ? await terms(setId, next.trim()) : [];
    setOptions(found.map((term) => ({ id: term.id, name: term.name })));
  }

  function add(id: string) {
    const name = options.find((option) => option.id === id)?.name;
    if (name) setPicked((current) => new Map(current).set(id, name));
    onChange(multiple ? [...new Set([...selected, id])] : id);
    setText('');
    setOptions([]);
  }

  return (
    <div className="flex flex-col gap-2">
      {selected.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {selected.map((id) => (
            <button
              key={id}
              type="button"
              className="rounded-full bg-surface-muted px-2 py-0.5 text-xs"
              aria-label={`Remove ${nameOf(id)}`}
              onClick={() => onChange(multiple ? selected.filter((item) => item !== id) : '')}
            >
              {nameOf(id)} ×
            </button>
          ))}
        </div>
      )}
      <Input id={id} value={text} placeholder="Search…" onChange={(event) => void search(event.target.value)} />
      {options.length > 0 && (
        <ul className="overflow-hidden rounded-md border">
          {options.map((option) => (
            <li key={option.id}>
              <button type="button" className="w-full px-2 py-1.5 text-left text-[13px] hover:bg-surface-muted" onClick={() => add(option.id)}>
                {option.name}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
