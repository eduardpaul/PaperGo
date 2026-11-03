import { describe, expect, it } from 'vitest';
import { languages, sourceLanguage } from './languages';
import { namespaces } from './resources';

// Every language mirrors the English catalogs: the same keys, the same placeholders and tags, and each plural in
// the forms that language needs (English one/other, Spanish one/many/other).

type Catalog = { [key: string]: string | Catalog };

const files = import.meta.glob<Catalog>('../locales/*/*.json', { eager: true, import: 'default' });
const plural = /_(zero|one|two|few|many|other)$/;

function catalog(code: string): Map<string, string> {
  const entries = new Map<string, string>();
  const walk = (value: string | Catalog, path: string) => {
    if (typeof value === 'string') entries.set(path, value);
    else for (const [key, child] of Object.entries(value)) walk(child, path.endsWith(':') ? path + key : `${path}.${key}`);
  };
  for (const [path, file] of Object.entries(files)) {
    const [, folder, namespace] = path.match(/locales\/([^/]+)\/([^/]+)\.json$/)!;
    if (folder === code) walk(file, `${namespace}:`);
  }
  return entries;
}

/** The keys a language needs: plural keys in each of its CLDR categories. */
function expectedKeys(source: Map<string, string>, code: string): Map<string, string> {
  const categories = new Intl.PluralRules(code).resolvedOptions().pluralCategories;
  const expected = new Map<string, string>();
  for (const [key, value] of source) {
    if (!plural.test(key)) expected.set(key, value);
    else if (key.endsWith('_other'))
      for (const category of categories) expected.set(key.replace(plural, `_${category}`), value);
  }
  return expected;
}

/** Placeholders and tags. A plural form may leave out the count: "un archivo". */
function tokens(key: string, text: string): string {
  return [...text.matchAll(/\{\{\s*([^}\s,]+)[^}]*\}\}|<\/?([\w-]+)\s*\/?>/g)]
    .map((match) => match[1] ?? `<${match[2]}>`)
    .filter((token) => !(plural.test(key) && token === 'count'))
    .sort()
    .join();
}

describe('catalogs', () => {
  const source = catalog(sourceLanguage);

  it('has a folder for every language and a language for every folder', () => {
    const folders = new Set(Object.keys(files).map((path) => path.match(/locales\/([^/]+)\//)![1]));
    const translated = languages.filter((language) => !language.development).map((language) => language.code);
    expect([...folders].sort()).toEqual([...translated].sort());
  });

  it('registers every English namespace', () => {
    const english = Object.keys(files)
      .filter((path) => path.includes(`/locales/${sourceLanguage}/`))
      .map((path) => path.match(/([^/]+)\.json$/)![1]);
    expect([...namespaces].sort()).toEqual(english.sort());
  });

  it('has English plurals in the forms English needs', () => {
    expect([...source.keys()].sort()).toEqual([...expectedKeys(source, sourceLanguage).keys()].sort());
  });

  for (const language of languages.filter((l) => !l.development && l.code !== sourceLanguage)) {
    describe(language.name, () => {
      const translation = catalog(language.code);
      const expected = expectedKeys(source, language.code);

      it('has exactly the English keys', () => {
        const missing = [...expected.keys()].filter((key) => !translation.has(key));
        const extra = [...translation.keys()].filter((key) => !expected.has(key));
        expect({ missing, extra }).toEqual({ missing: [], extra: [] });
      });

      it('keeps the placeholders and tags', () => {
        const differing = [...expected]
          .filter(([key, english]) => translation.has(key) && tokens(key, english) !== tokens(key, translation.get(key)!))
          .map(([key]) => key);
        expect(differing).toEqual([]);
      });

      it('translates every text', () => {
        expect([...translation].filter(([, value]) => !value.trim()).map(([key]) => key)).toEqual([]);
      });
    });
  }
});
