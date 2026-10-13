import type { BuiltInState } from '@/api/types';

/**
 * Parameters as the built-in's schema types them: numbers, lists of
 * comma-separated values, and text. Empty values are left out, so the
 * server applies its defaults.
 */
export function typedParameters(
  properties: NonNullable<BuiltInState['parameters_schema']>['properties'],
  values: Record<string, string>,
) {
  const out: Record<string, unknown> = {};
  for (const [key, raw] of Object.entries(values)) {
    const value = raw.trim();
    if (value === '') continue;
    switch (properties?.[key]?.type) {
      case 'number':
      case 'integer': {
        const n = Number(value);
        out[key] = Number.isFinite(n) ? n : value;
        break;
      }
      case 'array':
        out[key] = value
          .split(',')
          .map((part) => part.trim())
          .filter(Boolean);
        break;
      default:
        out[key] = raw;
    }
  }
  return out;
}
