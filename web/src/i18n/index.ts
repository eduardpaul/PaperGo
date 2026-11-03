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
  // A copy: i18next changes its own list as namespaces come and go.
  ns: [...namespaces],
  defaultNS: 'common',
  initAsync: false,
  interpolation: { escapeValue: false },
  returnNull: false,
});

const catalogs = import.meta.glob<Record<string, unknown>>(['../locales/*/*.json', '!../locales/en/*.json'], {
  import: 'default',
});

/** Loads the language's namespaces that are not loaded yet; after a failed chunk, the next call loads only what is missing. */
async function loadCatalogs(code: string) {
  if (code === sourceLanguage) return;
  const missing = namespaces.filter((namespace) => !i18n.hasResourceBundle(code, namespace));
  if (code === 'en-XA') {
    for (const namespace of missing) i18n.addResourceBundle(code, namespace, pseudoCatalog(en[namespace]));
    return;
  }
  const folder = `../locales/${code}/`;
  await Promise.all(
    missing.map(async (namespace) => i18n.addResourceBundle(code, namespace, await catalogs[`${folder}${namespace}.json`]())),
  );
}

// Each call to setLanguage; only the latest one changes the screen, however long the earlier ones take to load.
let latestCall = 0;

/** Shows the screen in the UI language for a locale (a preference, or the browser's). Never fails: English stays. */
export async function setLanguage(locale: string | null | undefined): Promise<void> {
  const call = ++latestCall;
  const language = uiLanguage(locale);
  try {
    await loadCatalogs(language.code);
    if (call !== latestCall) return;
    await i18n.changeLanguage(language.code);
  } catch {
    return;
  }
  if (call !== latestCall) return;
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
