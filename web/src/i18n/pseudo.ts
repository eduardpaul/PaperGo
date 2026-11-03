// The en-XA pseudo-locale: English with accented letters, about a third longer and bracketed, so text that was
// never translated, text cut off by its box and sentences built from pieces stand out. Placeholders and tags stay.

const accents: Record<string, string> = {
  a: 'å', b: 'ƀ', c: 'ç', d: 'ð', e: 'é', f: 'ƒ', g: 'ĝ', h: 'ĥ', i: 'î', j: 'ĵ', k: 'ķ', l: 'ļ', m: 'ɱ',
  n: 'ñ', o: 'ö', p: 'þ', q: 'ǫ', r: 'ŕ', s: 'š', t: 'ţ', u: 'û', v: 'ṽ', w: 'ŵ', x: 'ẋ', y: 'ý', z: 'ž',
  A: 'Å', B: 'Ɓ', C: 'Ç', D: 'Ð', E: 'É', F: 'Ƒ', G: 'Ĝ', H: 'Ĥ', I: 'Î', J: 'Ĵ', K: 'Ķ', L: 'Ļ', M: 'Ṁ',
  N: 'Ñ', O: 'Ö', P: 'Þ', Q: 'Ǫ', R: 'Ŕ', S: 'Š', T: 'Ţ', U: 'Û', V: 'Ṽ', W: 'Ŵ', X: 'Ẋ', Y: 'Ý', Z: 'Ž',
};

export function pseudo(text: string): string {
  const accented = text
    .split(/(\{\{[^}]*\}\}|<[^>]*>)/)
    .map((part, index) => (index % 2 ? part : part.replace(/[a-zA-Z]/g, (letter) => accents[letter] ?? letter)))
    .join('');
  const padding = '·'.repeat(Math.ceil(text.length * 0.35));
  return `[${accented}${padding ? ` ${padding}` : ''}]`;
}

export function pseudoCatalog<T>(catalog: T): T {
  if (typeof catalog === 'string') return pseudo(catalog) as T;
  if (catalog && typeof catalog === 'object')
    return Object.fromEntries(Object.entries(catalog).map(([key, value]) => [key, pseudoCatalog(value)])) as T;
  return catalog;
}
