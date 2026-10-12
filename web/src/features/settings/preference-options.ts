// Choices for the preference editors (PLT-17, PLT-18). The server accepts any culture, IANA time zone and d/M/y
// pattern; these are the common ones, and a value set elsewhere (API, organization default) is always kept as a choice.

export const cultures = [
  'en',
  'en-GB',
  'en-US',
  'de',
  'de-AT',
  'de-CH',
  'fr',
  'fr-CH',
  'es',
  'it',
  'nl',
  'pt',
  'pt-BR',
  'pl',
  'cs',
  'sv',
  'da',
  'nb',
  'fi',
  'ro',
  'hu',
  'tr',
  'el',
  'uk',
  'ru',
  'ja',
  'zh',
  'ko',
];

export const dateFormats = ['yyyy-MM-dd', 'dd.MM.yyyy', 'dd/MM/yyyy', 'MM/dd/yyyy', 'd.M.yyyy', 'd/M/yyyy'];

/** The time zones this browser knows (IANA names), UTC first. */
export function timeZones(): string[] {
  const zones = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : [];
  return ['UTC', ...zones.filter((z) => z !== 'UTC')];
}

/** "Deutsch (Deutschland)" style names, in the user's language when the browser knows it. */
export function cultureName(code: string, displayLanguage?: string): string {
  try {
    const name = new Intl.DisplayNames([displayLanguage ?? 'en', 'en'], { type: 'language' }).of(code);
    return name && name !== code ? `${name} (${code})` : code;
  } catch {
    return code;
  }
}

/** A value from elsewhere stays selectable even when it is not one of the common choices. */
export function withValue(options: string[], ...values: (string | null | undefined)[]): string[] {
  const extra = values.filter((v): v is string => !!v && !options.includes(v));
  return [...options, ...new Set(extra)];
}
