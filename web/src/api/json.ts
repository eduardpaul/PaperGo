import { isSafeNumber, LosslessNumber, parse, stringify } from 'lossless-json';

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

/** Convert a numeric input to a JSON number without passing its digits through float64. */
export function numericInput(text: string, integer = false): number | LosslessNumber {
  const input = text.trim();
  const valid = integer ? /^[+-]?\d+$/.test(input) : /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(input);
  if (!valid) throw new Error(integer ? 'Enter a valid integer.' : 'Enter a valid number.');
  const unsigned = input.replace(/^[+-]/, '');
  const [mantissa, exponent] = unsigned.split(/[eE]/);
  const [whole, fraction] = mantissa.split('.');
  const digits = (whole || '0').replace(/^0+(?=\d)/, '') + (fraction ? `.${fraction}` : '');
  return parseNumber(`${input.startsWith('-') ? '-' : ''}${digits}${exponent === undefined ? '' : `e${exponent}`}`);
}
