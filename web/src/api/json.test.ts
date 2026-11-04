import { afterEach, describe, expect, it } from 'vitest';
import { documentUploadRequest } from './requests';
import { decimalText, numericInput, parseJSON, setDecimalSeparator, stringifyJSON } from './json';

describe('API numeric transport', () => {
  it('keeps counters usable while preserving nested numeric field literals', () => {
    const response = parseJSON('{"version":2,"total":50,"values":{"amount":0.123456789123456789,"serial":9007199254740993}}') as { version: number; total: number; values: Record<string, unknown> };
    expect(response.version).toBe(2);
    expect(response.total).toBe(50);
    expect(stringifyJSON(response.values)).toBe('{"amount":0.123456789123456789,"serial":9007199254740993}');
  });

  it('uploads exact numeric metadata as JSON numbers', () => {
    const request = documentUploadRequest('/v1/resources/library/documents', 'file.pdf', {
      values: { amount: numericInput('9007199254740993'), serial: numericInput('9223372036854775807', true) },
    });
    expect(request.parts[1].value).toBe('{"values":{"amount":9007199254740993,"serial":9223372036854775807}}');
  });
});

describe('typed decimal separator', () => {
  afterEach(() => setDecimalSeparator('.'));

  it('reads a comma as the decimal point where the number format uses one', () => {
    setDecimalSeparator(',');
    expect(stringifyJSON(numericInput('41,5'))).toBe('41.5');
    expect(stringifyJSON(numericInput(' -0,25 '))).toBe('-0.25');
    expect(stringifyJSON(numericInput('41.5'))).toBe('41.5');
    expect(stringifyJSON(numericInput('9007199254740993,5'))).toBe('9007199254740993.5');
    expect(decimalText('41,5')).toBe('41.5');
  });

  it('rejects what it would have to guess', () => {
    setDecimalSeparator(',');
    expect(() => numericInput('1.000,5')).toThrow();
    expect(() => numericInput('1,2,3')).toThrow();
    expect(() => numericInput('41,5', true)).toThrow();
  });

  it('keeps a comma invalid where the decimal point is a dot', () => {
    expect(() => numericInput('1,000')).toThrow();
    expect(decimalText('1,5')).toBe('1,5');
  });
});
