// Dates, times and numbers in the user's preferences (PLT-17): time zone, date pattern, 12/24h, number culture.
// Screens format through a Formatter, never with toLocaleString, so every value follows the same settings.

export interface FormatPreferences {
  language: string;
  timeZone: string;
  /** Pattern of d, M and y with ., /, - or spaces, e.g. yyyy-MM-dd or dd.MM.yyyy */
  dateFormat: string;
  timeFormat: '24h' | '12h';
  numberFormat: string;
}

export const defaultPreferences: FormatPreferences = {
  language: 'en',
  timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
  dateFormat: 'yyyy-MM-dd',
  timeFormat: '24h',
  numberFormat: 'en',
};

export interface Formatter {
  readonly preferences: FormatPreferences;
  date(value: Date | string | null | undefined): string;
  time(value: Date | string | null | undefined): string;
  dateTime(value: Date | string | null | undefined): string;
  /** "in 3 hours", "yesterday" */
  relative(value: Date | string | null | undefined, now?: Date): string;
  number(value: number | string | null | undefined, options?: Intl.NumberFormatOptions): string;
  currency(value: number | null | undefined, currencyCode: string): string;
  fileSize(bytes: number | null | undefined): string;
  /** The calendar day (yyyy-MM-dd) of a moment in the user's time zone. */
  dayKey(value: Date | string): string;
  /** The hour (0–23) of a moment in the user's time zone. */
  hour(value: Date | string): number;
  /** The weekday name of a moment in the user's time zone and language. */
  weekday(value: Date | string, style?: 'long' | 'short'): string;
}

type Parts = { year: string; month: string; day: string; hour: string; minute: string };

function toDate(value: Date | string): Date {
  return value instanceof Date ? value : new Date(value);
}

function isDateOnly(value: Date | string): value is string {
  return typeof value === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(value);
}

export function createFormatter(preferences: Partial<FormatPreferences> = {}): Formatter {
  const p: FormatPreferences = { ...defaultPreferences, ...stripEmpty(preferences) };
  const partsFormat = safe(
    () =>
      new Intl.DateTimeFormat('en-US', {
        timeZone: p.timeZone,
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        hourCycle: 'h23',
      }),
    () =>
      new Intl.DateTimeFormat('en-US', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        hourCycle: 'h23',
      }),
  );
  const relativeFormat = safe(
    () => new Intl.RelativeTimeFormat(p.language, { numeric: 'auto' }),
    () => new Intl.RelativeTimeFormat('en', { numeric: 'auto' }),
  );

  const parts = (value: Date | string): Parts => {
    // A date without a time (e.g. a due date) is a calendar day, not a moment: no time zone shift.
    if (isDateOnly(value)) {
      const [year, month, day] = value.split('-') as [string, string, string];
      return { year, month, day, hour: '00', minute: '00' };
    }

    const result: Record<string, string> = {};
    for (const part of partsFormat.formatToParts(toDate(value))) result[part.type] = part.value;
    return result as unknown as Parts;
  };

  const date = (value: Date | string) => {
    const { year, month, day } = parts(value);
    return p.dateFormat.replace(/y+|M+|d+/g, (token) => {
      switch (token[0]) {
        case 'y':
          return token.length <= 2 ? year.slice(-2) : year;
        case 'M':
          return token.length === 1 ? String(Number(month)) : month;
        default:
          return token.length === 1 ? String(Number(day)) : day;
      }
    });
  };

  const time = (value: Date | string) => {
    const { hour, minute } = parts(value);
    if (p.timeFormat === '24h') return `${hour}:${minute}`;
    const h = Number(hour);
    return `${h % 12 === 0 ? 12 : h % 12}:${minute} ${h < 12 ? 'AM' : 'PM'}`;
  };

  const number = (value: number | string, options?: Intl.NumberFormatOptions) =>
    safe(
      // Intl accepts decimal strings exactly; TypeScript's ES2023 signature only lists number/bigint.
      () => new Intl.NumberFormat(p.numberFormat, options).format(value as number),
      () => new Intl.NumberFormat('en', options).format(value as number),
    );

  return {
    preferences: p,
    date: (value) => (value == null || value === '' ? '' : date(value)),
    time: (value) => (value == null || value === '' || isDateOnly(value) ? '' : time(value)),
    dateTime: (value) =>
      value == null || value === '' ? '' : isDateOnly(value) ? date(value) : `${date(value)} ${time(value)}`,
    relative: (value, now = new Date()) => {
      if (value == null || value === '') return '';
      const seconds = (toDate(value).getTime() - now.getTime()) / 1000;
      const units: [Intl.RelativeTimeFormatUnit, number][] = [
        ['year', 31536000],
        ['month', 2592000],
        ['week', 604800],
        ['day', 86400],
        ['hour', 3600],
        ['minute', 60],
      ];
      for (const [unit, size] of units) {
        if (Math.abs(seconds) >= size) return relativeFormat.format(Math.round(seconds / size), unit);
      }
      return relativeFormat.format(0, 'minute');
    },
    number: (value, options) => (value == null ? '' : number(value, options)),
    currency: (value, currencyCode) =>
      value == null ? '' : number(value, { style: 'currency', currency: currencyCode || 'EUR' }),
    fileSize: (bytes) => {
      if (bytes == null) return '';
      const units = ['B', 'KB', 'MB', 'GB', 'TB'];
      let size = bytes;
      let unit = 0;
      while (size >= 1024 && unit < units.length - 1) {
        size /= 1024;
        unit++;
      }
      return `${number(size, { maximumFractionDigits: unit === 0 ? 0 : 1 })} ${units[unit]}`;
    },
    dayKey: (value) => {
      const { year, month, day } = parts(value);
      return `${year}-${month}-${day}`;
    },
    hour: (value) => Number(parts(value).hour),
    weekday: (value, style = 'long') => {
      const { year, month, day } = parts(value);
      // Noon UTC of the user's calendar day names the same weekday everywhere.
      const noon = new Date(Date.UTC(Number(year), Number(month) - 1, Number(day), 12));
      return safe(
        () => new Intl.DateTimeFormat(p.language, { weekday: style, timeZone: 'UTC' }).format(noon),
        () => new Intl.DateTimeFormat('en', { weekday: style, timeZone: 'UTC' }).format(noon),
      );
    },
  };
}

function safe<T>(make: () => T, fallback: () => T): T {
  try {
    return make();
  } catch {
    return fallback();
  }
}

function stripEmpty<T extends object>(value: T): Partial<T> {
  return Object.fromEntries(
    Object.entries(value).filter(([, v]) => v !== undefined && v !== null && v !== ''),
  ) as Partial<T>;
}
