import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { childrenQuery, keywords, peopleQuery, terms, usersQuery } from '@/api/queries';
import type { FieldDefinition } from '@/api/types';
import { Input, Label, Textarea } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
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
      <Label htmlFor={id}>
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
  const users = useQuery(usersQuery);
  const names = new Map((users.data ?? []).map((user) => [user.subject, user.display_name || user.subject]));
  const options = people.data ?? [...names.keys()];
  if (!multiple) {
    return (
      <Select id={id} value={fieldText(value)} onChange={(event) => onChange(event.target.value)}>
        <option value="">Select a person…</option>
        {options.map((subject) => (
          <option key={subject} value={subject}>
            {names.get(subject) ?? subject}
          </option>
        ))}
      </Select>
    );
  }
  const selected = new Set(asList(value));
  return (
    <div id={id} className="flex max-h-40 flex-col gap-1 overflow-auto">
      {options.map((subject) => (
        <label key={subject} className="flex items-center gap-2 text-[13px]">
          <input
            type="checkbox"
            checked={selected.has(subject)}
            onChange={(event) => {
              const next = new Set(selected);
              if (event.target.checked) next.add(subject);
              else next.delete(subject);
              onChange([...next]);
            }}
          />
          {names.get(subject) ?? subject}
        </label>
      ))}
    </div>
  );
}

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
  const children = useQuery(childrenQuery(containerId));
  const items = (children.data?.value ?? []).filter((item) => item.kind === 'item');
  if (!multiple) {
    return (
      <Select id={id} value={fieldText(value)} onChange={(event) => onChange(event.target.value)}>
        <option value="">Select…</option>
        {items.map((item) => (
          <option key={item.id} value={item.id}>
            {item.name}
          </option>
        ))}
      </Select>
    );
  }
  const selected = new Set(asList(value));
  return (
    <div id={id} className="flex max-h-40 flex-col gap-1 overflow-auto">
      {items.map((item) => (
        <label key={item.id} className="flex items-center gap-2 text-[13px]">
          <input
            type="checkbox"
            checked={selected.has(item.id)}
            onChange={(event) => {
              const next = new Set(selected);
              if (event.target.checked) next.add(item.id);
              else next.delete(item.id);
              onChange([...next]);
            }}
          />
          {item.name}
        </label>
      ))}
    </div>
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
  const selected = asList(value);

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
              onClick={() => onChange(multiple ? selected.filter((item) => item !== id) : '')}
            >
              {options.find((option) => option.id === id)?.name ?? id} ×
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
