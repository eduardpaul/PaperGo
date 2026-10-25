import { useId } from 'react';
import { numericInput } from '@/api/json';
import { fileTypes } from '@/api/auth';
import { fileTypeLabel } from '@/features/files/file-types';
import type { FieldDefinition, FilterExpr } from '@/api/types';
import { FieldEditor } from '@/features/fields/editor';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';

export const systemFields: FieldDefinition[] = [
  ['$name', 'Name', 'text'],
  ['$id', 'ID', 'text'],
  ['$keywords', 'Keywords', 'keywords'],
  ['$created_at', 'Created', 'datetime'],
  ['$modified_at', 'Modified', 'datetime'],
  ['$created_by', 'Created by', 'person'],
  ['$modified_by', 'Modified by', 'person'],
  ['$archived_at', 'Archived', 'datetime'],
  ['$archived_by', 'Archived by', 'person'],
].map(([key, label, type]) => ({ id: key, key, label, type, indexed: true, options: key === '$keywords' ? { multiple: true } : undefined }));

export const comparisonLabels: Record<string, string> = { eq: 'is', ne: 'is not', in: 'is one of', missing: 'is empty', present: 'has a value', gt: 'is after / greater than', gte: 'is on or after / at least', lt: 'is before / less than', lte: 'is on or before / at most', contains: 'contains', under: 'is in this topic' };
export const relativeLabels: Record<string, string> = { today: 'Today', yesterday: 'Yesterday', tomorrow: 'Tomorrow', last7Days: 'Past week', next7Days: 'Next week', last30Days: 'Past month', next30Days: 'Next month', weekStart: 'Start of week', weekEnd: 'End of week', monthStart: 'Start of month', monthEnd: 'End of month', me: 'Current user' };
/** The file type filter: the kinds of file the server names, and files it does not recognize. */
export function fileTypeField(): FieldDefinition {
  return { id: '$file_type', key: '$file_type', label: 'File type', type: 'choice', indexed: true, choices: [...fileTypes().map((type) => type.media_type), 'application/octet-stream'] };
}

export function normalizeFilter(filter: FilterExpr | undefined, fields: FieldDefinition[]): FilterExpr | undefined {
  if (!filter) return undefined;
  if (filter.and) return { and: filter.and.map((child) => normalizeFilter(child, fields)!) };
  if (filter.or) return { or: filter.or.map((child) => normalizeFilter(child, fields)!) };
  if (filter.not) return { not: normalizeFilter(filter.not, fields) };
  const field = fields.find((field) => field.key === filter.field);
  const normalize = (value: unknown) =>
    typeof value === 'string' && ['integer', 'number', 'currency'].includes(field?.type ?? '')
      ? numericInput(value, field?.type === 'integer')
      : value;
  if (filter.op === 'missing' || filter.op === 'present') return { field: filter.field, op: filter.op };
  if (filter.value_ref) return { field: filter.field, op: filter.op, value_ref: filter.value_ref };
  return {
    field: filter.field,
    op: filter.op,
    value: filter.op === 'in' ? (filter.value as unknown[]).map(normalize) : normalize(filter.value),
  };
}

const newCondition = (): FilterExpr => ({ field: '$name', op: 'eq', value: '' });
export function FilterEditor({
  filter,
  fields,
  onChange,
  depth = 0,
}: {
  filter?: FilterExpr;
  fields: FieldDefinition[];
  onChange: (filter?: FilterExpr) => void;
  depth?: number;
}) {
  const id = useId();
  if (!filter) return <Button onClick={() => onChange(newCondition())}>Add filter</Button>;
  const children = filter.and ?? filter.or;
  if (children) {
    const operator = filter.and ? 'and' : 'or';
    return (
      <div className="grid gap-2 rounded border p-3">
        <div className="flex gap-2">
          <Select
            aria-label="Match conditions"
            value={operator}
            onChange={(event) => onChange({ [event.target.value]: children })}
          >
            <option value="and">All conditions</option>
            <option value="or">Any condition</option>
          </Select>
          <Button onClick={() => onChange(undefined)}>Remove group</Button>
        </div>
        {children.map((child, index) => (
          <FilterEditor
            key={index}
            filter={child}
            fields={fields}
            depth={depth + 1}
            onChange={(next) => {
              const values = children.flatMap((current, i) => (i === index ? (next ? [next] : []) : [current]));
              onChange(values.length ? { [operator]: values } : undefined);
            }}
          />
        ))}
        {depth < 5 && (
          <Button onClick={() => onChange({ [operator]: [...children, newCondition()] })}>Add condition</Button>
        )}
      </div>
    );
  }
  if (filter.not)
    return (
      <div className="grid gap-2 rounded border p-3">
        <div className="flex gap-2">
          <span className="flex-1">Not</span>
          <Button onClick={() => onChange(filter.not)}>Remove negation</Button>
        </div>
        <FilterEditor
          filter={filter.not}
          fields={fields}
          depth={depth + 1}
          onChange={(next) => onChange(next ? { not: next } : undefined)}
        />
      </div>
    );
  const field = fields.find((field) => field.key === filter.field);
  const op = filter.op || 'eq';
  const operators =
    filter.field === '$keywords'
      ? ['eq', 'ne']
      : [
          'eq',
          'ne',
          'in',
          'missing',
          'present',
          ...([
            'integer',
            'number',
            'currency',
            'decimal',
            'date',
            'datetime',
            'text',
            'note',
            'email',
            'url',
            'person',
          ].includes(field?.type ?? '')
            ? ['gt', 'gte', 'lt', 'lte']
            : []),
          ...(['text', 'note', 'email', 'url'].includes(field?.type ?? '') ? ['contains'] : []),
          ...(['term', 'keywords'].includes(field?.type ?? '') ? ['under'] : []),
        ];
  const value = (v: unknown, onValue: (v: unknown) => void, label: string) =>
    field && ['date', 'datetime', 'person', 'lookup', 'term', 'keywords'].includes(field.type) ? <FieldEditor id={`filter-${id}-${label.replaceAll(' ', '-')}`} field={{ ...field, label, required: false, options: { ...field.options, multiple: false } }} value={v} onChange={onValue} /> : field?.type === 'boolean' ? (
      <Select
        aria-label={label}
        value={String(v ?? false)}
        onChange={(event) => onValue(event.target.value === 'true')}
      >
        <option value="true">Yes</option>
        <option value="false">No</option>
      </Select>
    ) : field?.type === 'choice' ? (
      <Select aria-label={label} value={String(v ?? '')} onChange={(event) => onValue(event.target.value)}>
        <option value="">Choose…</option>
        {field.choices?.map((choice) => (
          <option key={choice} value={choice}>
            {field.key === '$file_type' ? fileTypeLabel(choice) : choice}
          </option>
        ))}
      </Select>
    ) : (
      <Input
        aria-label={label}
        value={String(v ?? '')}
        type={field?.type === 'date' ? 'date' : 'text'}
        inputMode={['integer', 'number', 'currency', 'decimal'].includes(field?.type ?? '') ? 'decimal' : undefined}
        placeholder={field?.type === 'datetime' ? '2026-10-12T00:00:00Z' : 'Value'}
        onChange={(event) => onValue(event.target.value)}
      />
    );
  return (
    <div className="grid gap-2 rounded border p-3">
      <div className="flex flex-wrap gap-2">
        <Select
          aria-label="Filter field"
          value={filter.field ?? ''}
          onChange={(event) =>
            onChange({
              field: event.target.value,
              op: 'eq',
              value: fields.find((field) => field.key === event.target.value)?.type === 'boolean' ? false : '',
            })
          }
        >
          {filter.field && !field && <option value={filter.field}>Field unavailable in this scope</option>}
          {fields.map((field) => (
            <option key={field.key} value={field.key}>
              {field.label}
            </option>
          ))}
        </Select>
        <Select
          aria-label="Comparison"
          value={op}
          onChange={(event) =>
            onChange({
              field: filter.field,
              op: event.target.value,
              value:
                event.target.value === 'in'
                  ? [filter.value ?? '']
                  : Array.isArray(filter.value)
                    ? filter.value[0]
                    : filter.value,
            })
          }
        >
          {operators.map((operator) => (
            <option key={operator} value={operator}>
              {comparisonLabels[operator] ?? operator}
            </option>
          ))}
        </Select>
        <Button onClick={() => onChange(undefined)}>Remove condition</Button>
      </div>
      {!['missing', 'present'].includes(op) && (
        <>
          {op !== 'in' && (
            <Select
              aria-label="Compare with"
              value={filter.value_ref ?? ''}
              onChange={(event) =>
                onChange({
                  field: filter.field,
                  op,
                  value_ref: event.target.value || undefined,
                  value: event.target.value ? undefined : '',
                })
              }
            >
              <option value="">A value</option>
              {field?.type === 'person' && <option value="me">Current user</option>}
              {['date', 'datetime'].includes(field?.type ?? '') &&
                [
                  'today',
                  'yesterday',
                  'tomorrow',
                  'last7Days',
                  'next7Days',
                  'last30Days',
                  'next30Days',
                  'weekStart',
                  'weekEnd',
                  'monthStart',
                  'monthEnd',
                ].map((ref) => (
                  <option key={ref} value={ref}>
                    {relativeLabels[ref]} (UTC)
                  </option>
                ))}
            </Select>
          )}
          {!filter.value_ref &&
            (op === 'in' ? (
              <>
                {(Array.isArray(filter.value) ? filter.value : []).map((entry, index, values) => (
                  <div key={index} className="flex gap-2">
                    {value(
                      entry,
                      (next) =>
                        onChange({ ...filter, value: values.map((current, i) => (i === index ? next : current)) }),
                      `Filter value ${index + 1}`,
                    )}
                    <Button onClick={() => onChange({ ...filter, value: values.filter((_, i) => i !== index) })}>
                      Remove value
                    </Button>
                  </div>
                ))}
                <Button
                  onClick={() =>
                    onChange({ ...filter, value: [...(Array.isArray(filter.value) ? filter.value : []), ''] })
                  }
                >
                  Add value
                </Button>
              </>
            ) : (
              value(filter.value, (next) => onChange({ ...filter, value: next }), `Filter value ${id}`)
            ))}
        </>
      )}
      {depth < 5 && (
        <div className="flex gap-2">
          <Button onClick={() => onChange({ and: [filter, newCondition()] })}>Add condition group</Button>
          <Button onClick={() => onChange({ not: filter })}>Negate</Button>
        </div>
      )}
    </div>
  );
}
