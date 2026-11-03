// The UI languages. Each has a catalog folder in src/locales; English is the source and the fallback.
// Adding a language: copy src/locales/en to src/locales/<code>, translate the values and add a line here.

export interface Language {
  code: string;
  /** The language's name in that language, as the language picker shows it. */
  name: string;
  dir: 'ltr' | 'rtl';
  /** Only offered by the development server. */
  development?: boolean;
}

export const sourceLanguage = 'en';

export const languages: readonly Language[] = [
  { code: 'en', name: 'English', dir: 'ltr' },
  { code: 'es', name: 'Español', dir: 'ltr' },
  // English with accented, lengthened text: shows untranslated text and layouts that break with longer words.
  { code: 'en-XA', name: 'Ƥšéûðö', dir: 'ltr', development: true },
];

/** The languages this build offers. */
export function offeredLanguages(): readonly Language[] {
  return languages.filter((language) => !language.development || import.meta.env.DEV);
}

/** The UI language for a locale: the exact one, else its base language (es-MX → es), else English. */
export function uiLanguage(locale: string | null | undefined): Language {
  const offered = offeredLanguages();
  const lower = locale?.toLowerCase();
  const base = lower?.split('-')[0];
  return (
    offered.find((language) => language.code.toLowerCase() === lower) ??
    offered.find((language) => language.code === base) ??
    offered.find((language) => language.code === sourceLanguage)!
  );
}

/**
 * The locale for screen text and the words in dates (yesterday, Monday): the preference itself when its language is
 * translated, so es-MX stays es-MX; otherwise the UI language the screen falls back to.
 */
export function uiLocale(locale: string | null | undefined): string {
  const language = uiLanguage(locale);
  return locale && locale.toLowerCase().split('-')[0] === language.code.split('-')[0] && !language.development
    ? locale
    : language.code;
}
