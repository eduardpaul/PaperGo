import { useId } from 'react';
import { decimalText, numericInput } from '@/api/json';
import { fileTypes } from '@/api/auth';
import { fileTypeLabel } from '@/features/files/file-types';
import type { FieldDefinition, FilterExpr } from '@/api/types';
import { FieldEditor } from '@/features/fields/editor';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { i18n, useTranslation } from '@/i18n';

/** The comparisons a filter condition can make, in the order the editor offers them. */
export const comparisons = ['eq', 'ne', 'in', 'missing', 'present', 'gt', 'gte', 'lt', 'lte', 'contains', 'under'] as const;
/** The moments a date condition can compare with instead of a fixed date. */
export const relativeDates = ['today', 'yesterday', 'tomorrow', 'last7Days', 'next7Days', 'last30Days', 'next30Days', 'weekStart', 'weekEnd', 'monthStart', 'monthEnd'] as const;
/** The file type filter: the kinds of file the server names, and files it does not recognize. */
export function fileTypeField(): FieldDefinition {
  return { id: '$file_type', key: '$file_type', label: i18n.t('lists:system.fileType'), type: 'choice', indexed: true, choices: [...fileTypes().map((type) => type.media_type), 'application/octet-stream'] };
}

export function normalizeFilter(filter: FilterExpr | undefined, fields: FieldDefinition[]): FilterExpr | undefined {
  if (!filter) return undefined;
  if (filter.and) return { and: filter.and.map((child) => normalizeFilter(child, fields)!) };
  if (filter.or) return { or: filter.or.map((child) => normalizeFilter(child, fields)!) };
  if (filter.not) return { not: normalizeFilter(filter.not, fields) };
  const field = fields.find((field) => field.key === filter.field);
  const normalize = (value: unknown) =>
    typeof value !== 'string'
      ? value
      : ['integer', 'number', 'currency'].includes(field?.type ?? '')
        ? numericInput(value, field?.type === 'integer')
        : field?.type === 'decimal'
          ? decimalText(value)
          : value;
  if (filter.op === 'missing' || filter.op === 'present') return { field: filter.field, op: filter.op };
  if (filter.value_ref) return { field: filter.field, op: filter.op, value_ref: filter.value_ref };
  return {
    field: filter.field,
    op: filter.op,
    value: filter.op === 'in' ? (filter.value as unknown[]).map(normalize) : normalize(filter.value),
  };
}

/** A moment as the API reads it; the same in every language. */
const datetimeExample = '2026-10-12T00:00:00Z';

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
  const { t } = useTranslation(['lists', 'common']);
  const id = useId();
  if (!filter) return <Button onClick={() => onChange(newCondition())}>{t('filter.add')}</Button>;
  const children = filter.and ?? filter.or;
  if (children) {
    const operator = filter.and ? 'and' : 'or';
    return (
      <div className="grid gap-2 rounded border p-3">
        <div className="flex gap-2">
          <Select
            aria-label={t('filter.match')}
            value={operator}
            onChange={(event) => onChange({ [event.target.value]: children })}
          >
            <option value="and">{t('filter.all')}</option>
            <option value="or">{t('filter.any')}</option>
          </Select>
          <Button onClick={() => onChange(undefined)}>{t('filter.removeGroup')}</Button>
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
          <Button onClick={() => onChange({ [operator]: [...children, newCondition()] })}>{t('filter.addCondition')}</Button>
        )}
      </div>
    );
  }
  if (filter.not)
    return (
      <div className="grid gap-2 rounded border p-3">
        <div className="flex gap-2">
          <span className="flex-1">{t('filter.not')}</span>
          <Button onClick={() => onChange(filter.not)}>{t('filter.removeNegation')}</Button>
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
        <option value="true">{t('common:answer.yes')}</option>
        <option value="false">{t('common:answer.no')}</option>
      </Select>
    ) : field?.type === 'choice' ? (
      <Select aria-label={label} value={String(v ?? '')} onChange={(event) => onValue(event.target.value)}>
        <option value="">{t('filter.choose')}</option>
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
        placeholder={field?.type === 'datetime' ? datetimeExample : t('filter.value')}
        onChange={(event) => onValue(event.target.value)}
      />
    );
  return (
    <div className="grid gap-2 rounded border p-3">
      <div className="flex flex-wrap gap-2">
        <Select
          aria-label={t('filter.field')}
          value={filter.field ?? ''}
          onChange={(event) =>
            onChange({
              field: event.target.value,
              op: 'eq',
              value: fields.find((field) => field.key === event.target.value)?.type === 'boolean' ? false : '',
            })
          }
        >
          {filter.field && !field && <option value={filter.field}>{t('filter.unavailable')}</option>}
          {fields.map((field) => (
            <option key={field.key} value={field.key}>
              {field.label}
            </option>
          ))}
        </Select>
        <Select
          aria-label={t('filter.comparison')}
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
              {comparisons.find((known) => known === operator) ? t(`filter.ops.${operator as (typeof comparisons)[number]}`) : operator}
            </option>
          ))}
        </Select>
        <Button onClick={() => onChange(undefined)}>{t('filter.removeCondition')}</Button>
      </div>
      {!['missing', 'present'].includes(op) && (
        <>
          {op !== 'in' && (
            <Select
              aria-label={t('filter.compareWith')}
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
              <option value="">{t('filter.aValue')}</option>
              {field?.type === 'person' && <option value="me">{t('filter.relative.me')}</option>}
              {['date', 'datetime'].includes(field?.type ?? '') &&
                relativeDates.map((ref) => (
                  <option key={ref} value={ref}>
                    {t('filter.utc', { date: t(`filter.relative.${ref}`) })}
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
                      t('filter.valueNumber', { number: index + 1 }),
                    )}
                    <Button onClick={() => onChange({ ...filter, value: values.filter((_, i) => i !== index) })}>
                      {t('filter.removeValue')}
                    </Button>
                  </div>
                ))}
                <Button
                  onClick={() =>
                    onChange({ ...filter, value: [...(Array.isArray(filter.value) ? filter.value : []), ''] })
                  }
                >
                  {t('filter.addValue')}
                </Button>
              </>
            ) : (
              value(filter.value, (next) => onChange({ ...filter, value: next }), t('filter.valueNumber', { number: id }))
            ))}
        </>
      )}
      {depth < 5 && (
        <div className="flex gap-2">
          <Button onClick={() => onChange({ and: [filter, newCondition()] })}>{t('filter.addGroup')}</Button>
          <Button onClick={() => onChange({ not: filter })}>{t('filter.negate')}</Button>
        </div>
      )}
    </div>
  );
}
