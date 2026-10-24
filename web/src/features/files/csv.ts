/** A parsed CSV file: its rows of cells, and whether the bounds cut it short. */
export interface Table {
  rows: string[][];
  delimiter: string;
  truncated: boolean;
}

/** The largest number of rows read for the table view. */
export const maxRows = 100_000;

/** The delimiter of a CSV file: the one of comma, semicolon and tab most used, outside quotes, in its first lines. */
export function detectDelimiter(text: string): string {
  const sample = text.slice(0, 64 * 1024);
  const counts = new Map<string, number>([[',', 0], [';', 0], ['\t', 0]]);
  let quoted = false;
  let lines = 0;
  for (let i = 0; i < sample.length && lines < 50; i++) {
    const c = sample[i];
    if (c === '"') quoted = !quoted;
    else if (!quoted && c === '\n') lines++;
    else if (!quoted && counts.has(c)) counts.set(c, counts.get(c)! + 1);
  }
  let best = ',';
  for (const [delimiter, count] of counts) if (count > counts.get(best)!) best = delimiter;
  return best;
}

/**
 * Parses CSV as RFC 4180 describes: quoted cells may hold delimiters,
 * doubled quotes and line breaks; lines end with LF or CRLF. A leading BOM
 * is dropped. Parsing stops after limit rows.
 */
export function parseCsv(text: string, delimiter = detectDelimiter(text), limit = maxRows): Table {
  if (text.charCodeAt(0) === 0xfeff) text = text.slice(1);
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = '';
  let quoted = false;
  let i = 0;
  const endRow = () => {
    row.push(cell);
    cell = '';
    rows.push(row);
    row = [];
  };
  while (i < text.length) {
    const c = text[i];
    if (quoted) {
      if (c === '"') {
        if (text[i + 1] === '"') {
          cell += '"';
          i += 2;
          continue;
        }
        quoted = false;
      } else {
        cell += c;
      }
      i++;
      continue;
    }
    if (c === '"' && cell === '') {
      quoted = true;
    } else if (c === delimiter) {
      row.push(cell);
      cell = '';
    } else if (c === '\n' || c === '\r') {
      if (c === '\r' && text[i + 1] === '\n') i++;
      endRow();
      if (rows.length >= limit) return { rows, delimiter, truncated: i + 1 < text.length };
    } else {
      cell += c;
    }
    i++;
  }
  if (cell !== '' || row.length > 0) endRow();
  return { rows, delimiter, truncated: false };
}
