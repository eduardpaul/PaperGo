// Numbers and dates in the viewer's preferences, as PaperGo shows them.

/** The viewer's display preferences (docs/preferences.md). */
export interface Preferences {
  language: string;
  time_zone: string;
  /** A pattern of d, M and y, such as yyyy-MM-dd. */
  date_format: string;
  time_format: "12h" | "24h";
  /** The locale numbers are written in, such as en or de. */
  number_format: string;
}

export interface Format {
  /** A number with at most digits fraction digits (default 2), in the viewer's number format. */
  number(value: number | string | null | undefined, digits?: number): string;
  /** A decimal with exactly scale fraction digits. */
  decimal(value: number | string | null | undefined, scale: number): string;
  /** An amount of a currency, such as EUR. */
  currency(value: number | string | null | undefined, code: string): string;
  /** A date in the viewer's date format and time zone. */
  date(value: Date | string | number | null | undefined): string;
  /** A date and time in the viewer's formats and time zone. */
  dateTime(value: Date | string | number | null | undefined): string;
  /** How long ago or from now, such as "3 days ago", in the viewer's language. */
  relative(value: Date | string | number | null | undefined, now?: Date): string;
  /**
   * Reads a decimal typed in the viewer's number format: 41,5 is 41.5 where
   * the separator is a comma, and a dot is always a decimal point. Returns
   * the canonical text, such as "41.5", or undefined for anything else,
   * such as text with both separators.
   */
  parseDecimal(text: string): string | undefined;
}

function toNumber(value: number | string | null | undefined): number | undefined {
  if (value === null || value === undefined || value === "") return undefined;
  const n = typeof value === "number" ? value : Number(value);
  return Number.isFinite(n) ? n : undefined;
}

function toDate(value: Date | string | number | null | undefined): Date | undefined {
  if (value === null || value === undefined || value === "") return undefined;
  const d = value instanceof Date ? value : new Date(value);
  return Number.isNaN(d.getTime()) ? undefined : d;
}

/** The decimal separator of a number format. */
export function decimalSeparator(locale: string): string {
  try {
    return new Intl.NumberFormat(locale).formatToParts(1.5).find((p) => p.type === "decimal")?.value ?? ".";
  } catch {
    return ".";
  }
}

export function formatter(p: Preferences): Format {
  const locale = p.number_format || "en";
  const zone = p.time_zone || "UTC";
  const separator = decimalSeparator(locale);
  // Intl formatters are costly to make and charts format many values: each
  // is made once.
  const numbers = new Map<string, Intl.NumberFormat>();
  const numberFormat = (key: string, options: () => Intl.NumberFormatOptions) => {
    let format = numbers.get(key);
    if (!format) numbers.set(key, (format = new Intl.NumberFormat(locale, options())));
    return format;
  };
  let calendar: Intl.DateTimeFormat | undefined;
  const parts = (d: Date) => {
    const out: Record<string, string> = {};
    calendar ??= new Intl.DateTimeFormat("en-US", { timeZone: zone, year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
    for (const part of calendar.formatToParts(d)) {
      out[part.type] = part.value;
    }
    return out;
  };
  const date = (d: Date) => {
    const x = parts(d);
    return (p.date_format || "yyyy-MM-dd").replace(/y+|M+|d+/g, (token) => {
      switch (token[0]) {
        case "y":
          return token.length === 2 ? x.year.slice(-2) : x.year;
        case "M":
          return token.length === 1 ? String(Number(x.month)) : x.month;
        default:
          return token.length === 1 ? String(Number(x.day)) : x.day;
      }
    });
  };
  const time = (d: Date) => {
    const x = parts(d);
    if (p.time_format !== "12h") return `${x.hour}:${x.minute}`;
    const h = Number(x.hour);
    return `${h % 12 || 12}:${x.minute} ${h < 12 ? "AM" : "PM"}`;
  };
  return {
    number(value, digits = 2) {
      const n = toNumber(value);
      return n === undefined ? "" : numberFormat(`n${digits}`, () => ({ maximumFractionDigits: digits })).format(n);
    },
    decimal(value, scale) {
      const n = toNumber(value);
      return n === undefined ? "" : numberFormat(`d${scale}`, () => ({ minimumFractionDigits: scale, maximumFractionDigits: scale })).format(n);
    },
    currency(value, code) {
      const n = toNumber(value);
      if (n === undefined) return "";
      try {
        return numberFormat(`c${code}`, () => ({ style: "currency", currency: code })).format(n);
      } catch {
        return `${numberFormat("d2", () => ({ minimumFractionDigits: 2, maximumFractionDigits: 2 })).format(n)} ${code}`;
      }
    },
    date(value) {
      const d = toDate(value);
      return d ? date(d) : "";
    },
    dateTime(value) {
      const d = toDate(value);
      return d ? `${date(d)} ${time(d)}` : "";
    },
    relative(value, now = new Date()) {
      const d = toDate(value);
      if (!d) return "";
      const seconds = (d.getTime() - now.getTime()) / 1000;
      const units: [Intl.RelativeTimeFormatUnit, number][] = [["year", 31536000], ["month", 2592000], ["day", 86400], ["hour", 3600], ["minute", 60]];
      const rtf = new Intl.RelativeTimeFormat(p.language || "en", { numeric: "auto" });
      for (const [unit, size] of units) {
        if (Math.abs(seconds) >= size) return rtf.format(Math.round(seconds / size), unit);
      }
      return rtf.format(0, "minute");
    },
    parseDecimal(text) {
      let t = text.trim().replace(/\s/g, "");
      if (separator === ",") {
        if (t.includes(",") && t.includes(".")) return undefined;
        t = t.replace(",", ".");
      }
      if (!/^[-+]?(\d+(\.\d*)?|\.\d+)$/.test(t)) return undefined;
      const n = Number(t);
      if (!Number.isFinite(n)) return undefined;
      const [whole, fraction = ""] = t.replace(/^\+/, "").split(".");
      const canonical = (whole.replace(/^(-?)0+(?=\d)/, "$1") || "0") + (fraction.replace(/0+$/, "") ? "." + fraction.replace(/0+$/, "") : "");
      return canonical.startsWith(".") ? "0" + canonical : canonical.replace(/^-\./, "-0.");
    },
  };
}
