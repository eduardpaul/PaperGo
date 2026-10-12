import { describe, expect, it } from 'vitest';
import { documentUploadRequest } from './requests';
import { numericInput, parseJSON, stringifyJSON } from './json';

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
