// How PaperGo reads typed numbers and writes dates. The web app and the app
// runtime both use these, so a value reads and shows the same in a PaperGo
// field and in an app.

/** The decimal separator of a number format: "." for en, "," for es or de. */
export function decimalSeparator(locale: string): string {
  try {
    return new Intl.NumberFormat(locale).formatToParts(1.5).find((part) => part.type === "decimal")?.value ?? ".";
  } catch {
    return ".";
  }
}

/**
 * Typed text with its decimal separator read: where the separator is a comma,
 * "41,5" is "41.5". A dot stays a decimal point, and anything with both, or
 * several commas, is left as typed so that it is rejected rather than guessed.
 */
export function withDecimalPoint(text: string, separator: string): string {
  const input = text.trim();
  return separator === "," && !input.includes(".") && input.split(",").length === 2 ? input.replace(",", ".") : input;
}

/**
 * A typed number as the text of a JSON number, keeping every digit it was
 * typed with, or undefined when it is not a number.
 */
export function typedNumber(text: string, separator: string, integer = false): string | undefined {
  const input = integer ? text.trim() : withDecimalPoint(text, separator);
  const valid = integer ? /^[+-]?\d+$/.test(input) : /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(input);
  if (!valid) return undefined;
  const [mantissa = "", exponent] = input.replace(/^[+-]/, "").split(/[eE]/);
  const [whole = "", fraction] = mantissa.split(".");
  const digits = (whole || "0").replace(/^0+(?=\d)/, "") + (fraction ? `.${fraction}` : "");
  return `${input.startsWith("-") ? "-" : ""}${digits}${exponent === undefined ? "" : `e${exponent}`}`;
}

// A decimal field holds far fewer digits than this; it stops an exponent
// writing out an absurd number.
const maxExponent = 1000;

/**
 * The text of a JSON number as a decimal field saves it, such as "1500" for
 * 1.5e3 and "41.5" for 41.50, or undefined when its exponent is out of reach.
 */
export function canonicalDecimal(number: string): string | undefined {
  const match = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(number);
  if (!match) return undefined;
  const exponent = Number(match[4] ?? 0);
  if (Math.abs(exponent) > maxExponent) return undefined;
  const whole = match[2] ?? "";
  const digits = whole + (match[3] ?? "");
  const point = whole.length + exponent;
  const before = point <= 0 ? "" : digits.slice(0, point).padEnd(point, "0");
  const after = point <= 0 ? "0".repeat(-point) + digits : digits.slice(point);
  const integer = before.replace(/^0+(?=\d)/, "") || "0";
  const fraction = after.replace(/0+$/, "");
  const zero = /^0+$/.test(integer) && fraction === "";
  return `${match[1] && !zero ? "-" : ""}${integer}${fraction ? `.${fraction}` : ""}`;
}

/** A calendar day: month 1 to 12. */
export interface CalendarDay {
  year: number;
  month: number;
  day: number;
}

/**
 * The day a date without a time names, such as a due date: a calendar day and
 * not a moment, so no time zone moves it. Undefined for anything else.
 */
export function calendarDay(value: unknown): CalendarDay | undefined {
  if (typeof value !== "string") return undefined;
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  return match ? { year: Number(match[1]), month: Number(match[2]), day: Number(match[3]) } : undefined;
}

const pad = (n: number) => String(n).padStart(2, "0");

/** A day in a pattern of d, M and y, such as yyyy-MM-dd. */
export function formatDay(pattern: string, day: CalendarDay): string {
  return pattern.replace(/y+|M+|d+/g, (token) => {
    switch (token[0]) {
      case "y":
        return token.length <= 2 ? String(day.year).slice(-2) : String(day.year);
      case "M":
        return token.length === 1 ? String(day.month) : pad(day.month);
      default:
        return token.length === 1 ? String(day.day) : pad(day.day);
    }
  });
}

/** A time of day on a 24-hour or 12-hour clock; hour is 0 to 23. */
export function formatClock(hour: number, minute: number, clock: string): string {
  if (clock !== "12h") return `${pad(hour)}:${pad(minute)}`;
  return `${hour % 12 || 12}:${pad(minute)} ${hour < 12 ? "AM" : "PM"}`;
}

const relativeUnits: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 31536000],
  ["month", 2592000],
  ["week", 604800],
  ["day", 86400],
  ["hour", 3600],
  ["minute", 60],
];

/** How far off a number of seconds is, such as "in 3 hours" or "yesterday". */
export function formatRelative(seconds: number, format: Intl.RelativeTimeFormat): string {
  for (const [unit, size] of relativeUnits) {
    if (Math.abs(seconds) >= size) return format.format(Math.round(seconds / size), unit);
  }
  return format.format(0, "minute");
}
