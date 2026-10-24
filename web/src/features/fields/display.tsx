import { isLosslessNumber } from 'lossless-json';
import type { FieldDefinition } from '@/api/types';
import { TrustedLink } from '@/features/files/trust';
import { useFormat } from '@/lib/preferences';
import { asList, fieldText } from './values';

export function FieldDisplay({ field, value, names }: { field: FieldDefinition; value: unknown; names?: Map<string, string> }) {
  const format = useFormat();
  if (value === undefined || value === null || value === '') return <span className="text-muted">—</span>;
  if (field.type === 'boolean') return <>{value ? 'Yes' : 'No'}</>;
  if (field.type === 'date') return <>{format.date(fieldText(value))}</>;
  if (field.type === 'datetime') return <>{format.dateTime(fieldText(value))}</>;
  if (field.type === 'number' && (typeof value === 'number' || isLosslessNumber(value))) {
    return <>{format.number(String(value), { maximumFractionDigits: 28 })}</>;
  }
  if (field.type === 'currency') {
    return field.options?.currency_code ? <>{format.currency(fieldText(value), field.options.currency_code)}</> : <>{fieldText(value)}</>;
  }
  if (field.options?.multiple || Array.isArray(value) || field.type === 'person' || field.type === 'lookup' || field.type === 'term' || field.type === 'keywords') {
    const items = asList(value);
    if (items.length === 0) return <span className="text-muted">—</span>;
    return <>{items.map((item) => names?.get(item) ?? item).join(', ')}</>;
  }
  if (field.type === 'url') {
    const href = fieldText(value);
    // A link inside a row or card opens itself, not the item.
    const stop = (event: React.SyntheticEvent) => event.stopPropagation();
    return (
      <span className="line-clamp-2 break-all" onClick={stop} onDoubleClick={stop} onKeyDown={stop}>
        <TrustedLink href={href}>{href}</TrustedLink>
      </span>
    );
  }
  return <span className="line-clamp-2 whitespace-pre-wrap">{fieldText(value)}</span>;
}
