// Screen text (i18next). English is bundled and ready on import; another language loads its catalogs on demand.
// Screens use useTranslation from here, so the instance is always set up before the first render, tests included.

import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import { sourceLanguage, uiLanguage } from './languages';
import { pseudoCatalog } from './pseudo';
import { en, namespaces } from './resources';

export { Trans, useTranslation } from 'react-i18next';
export { i18n };

const LanguageKey = 'papergo.language';

void i18n.use(initReactI18next).init({
  resources: { [sourceLanguage]: en },
  lng: sourceLanguage,
  fallbackLng: sourceLanguage,
  ns: namespaces,
  defaultNS: 'common',
  initAsync: false,
  interpolation: { escapeValue: false },
  returnNull: false,
});

const catalogs = import.meta.glob<Record<string, unknown>>(['../locales/*/*.json', '!../locales/en/*.json'], {
  import: 'default',
});

async function loadCatalogs(code: string) {
  if (code === sourceLanguage || i18n.hasResourceBundle(code, 'common')) return;
  if (code === 'en-XA') {
    for (const [namespace, catalog] of Object.entries(en)) i18n.addResourceBundle(code, namespace, pseudoCatalog(catalog));
    return;
  }
  const folder = `../locales/${code}/`;
  await Promise.all(
    Object.entries(catalogs)
      .filter(([path]) => path.startsWith(folder))
      .map(async ([path, load]) => i18n.addResourceBundle(code, path.slice(folder.length, -'.json'.length), await load())),
  );
}

/** Shows the screen in the UI language for a locale (a preference, or the browser's). Never fails: English stays. */
export async function setLanguage(locale: string | null | undefined): Promise<void> {
  const language = uiLanguage(locale);
  try {
    await loadCatalogs(language.code);
    await i18n.changeLanguage(language.code);
  } catch {
    return;
  }
  document.documentElement.lang = language.code;
  document.documentElement.dir = language.dir;
  try {
    localStorage.setItem(LanguageKey, language.code);
  } catch {
    // Storage can be unavailable; the language still applies to this page.
  }
}

/** Before sign-in: the language this browser used last, else the browser's own. */
export function browserLanguage(): string | undefined {
  try {
    const stored = localStorage.getItem(LanguageKey);
    if (stored) return stored;
  } catch {
    // Storage can be unavailable (private windows).
  }
  // The first of the browser's languages that has a catalog: [de, es, en] shows Spanish, [en-US, es] English.
  const translated = (locale: string) => uiLanguage(locale).code === locale.toLowerCase().split('-')[0];
  return (navigator.languages ?? [navigator.language]).find(translated);
}
