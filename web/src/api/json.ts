import { isSafeNumber, LosslessNumber, parse, stringify } from 'lossless-json';
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

/**
 * A typed decimal as the text of a JSON number: where the number format's decimal separator is a comma,
 * "41,5" is "41.5". A dot stays a decimal point, and anything with both, or several commas, is left as typed
 * so that it is rejected rather than guessed.
 */
export function decimalText(text: string): string {
  const input = text.trim();
  return decimalMark === ',' && !input.includes('.') && input.split(',').length === 2 ? input.replace(',', '.') : input;
}

/** Convert a numeric input to a JSON number without passing its digits through float64. */
export function numericInput(text: string, integer = false): number | LosslessNumber {
  const input = integer ? text.trim() : decimalText(text);
  const valid = integer ? /^[+-]?\d+$/.test(input) : /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(input);
  if (!valid) throw new Error(i18n.t(integer ? 'home:values.invalidInteger' : 'home:values.invalidNumber'));
  const unsigned = input.replace(/^[+-]/, '');
  const [mantissa, exponent] = unsigned.split(/[eE]/);
  const [whole, fraction] = mantissa.split('.');
  const digits = (whole || '0').replace(/^0+(?=\d)/, '') + (fraction ? `.${fraction}` : '');
  return parseNumber(`${input.startsWith('-') ? '-' : ''}${digits}${exponent === undefined ? '' : `e${exponent}`}`);
}
