import { isSafeNumber, LosslessNumber, parse, stringify } from 'lossless-json';
import { typedNumber, withDecimalPoint } from '@papergo/format';
import { i18n } from '@/i18n';

/** Keep ordinary API counters as numbers and retain exact field values as numeric literals. */
function parseNumber(value: string): number | LosslessNumber {
  return isSafeNumber(value) ? Number(value) : new LosslessNumber(value);
}

export function parseJSON(text: string): unknown {
  return parse(text, null, { parseNumber });
}

export function stringifyJSON(value: unknown): string | undefined {
  return stringify(value);
}

let decimalMark = '.';

/** Sets the decimal separator people type: the one of their number format preference. */
export function setDecimalSeparator(mark: string): void {
  decimalMark = mark;
}

/** A typed decimal as the text of a JSON number; see withDecimalPoint. */
export function decimalText(text: string): string {
  return withDecimalPoint(text, decimalMark);
}

/** Convert a numeric input to a JSON number without passing its digits through float64. */
export function numericInput(text: string, integer = false): number | LosslessNumber {
  const number = typedNumber(text, decimalMark, integer);
  if (number === undefined) throw new Error(i18n.t(integer ? 'home:values.invalidInteger' : 'home:values.invalidNumber'));
  return parseNumber(number);
}
