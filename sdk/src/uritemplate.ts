// expand fills the URI templates Kiota generates for the PaperGo API, which
// use three RFC 6570 forms: {+name} (reserved), {name} (simple) and
// {?a*,b*} (exploded query). std-uritemplate implements the whole RFC and
// costs more than the rest of a request in the workflow runtime.

type Value = string | number | boolean | Date | { toString(): string } | Value[];

const parsed = new Map<string, Array<string | { op: string; names: string[] }>>();

function parts(template: string): Array<string | { op: string; names: string[] }> {
  let out = parsed.get(template);
  if (out) return out;
  out = [];
  const pattern = /\{([+?]?)([^}]+)\}/g;
  let at = 0;
  for (let match = pattern.exec(template); match; match = pattern.exec(template)) {
    if (match.index > at) out.push(template.slice(at, match.index));
    out.push({ op: match[1], names: match[2].split(",") });
    at = pattern.lastIndex;
  }
  if (at < template.length) out.push(template.slice(at));
  for (const part of out) {
    if (typeof part !== "string" && (part.op !== "?" ? part.names.length !== 1 || part.names[0].endsWith("*") : part.names.some((name) => !name.endsWith("*")))) {
      throw new Error(`unsupported URI template expression in ${template}`);
    }
  }
  parsed.set(template, out);
  return out;
}

function text(value: Value): string {
  if (value instanceof Date) return value.toISOString();
  return String(value);
}

// Unreserved characters stay; everything else is percent-encoded.
function unreserved(value: string): string {
  return encodeURIComponent(value).replace(/[!'()*]/g, (c) => `%${c.charCodeAt(0).toString(16).toUpperCase()}`);
}

// Reserved characters and existing percent-encodings stay too.
function reserved(value: string): string {
  return encodeURI(value).replace(/%25([0-9A-Fa-f]{2})/g, "%$1");
}

export function expand(template: string, data: Record<string, unknown>): string {
  let out = "";
  for (const part of parts(template)) {
    if (typeof part === "string") {
      out += part;
      continue;
    }
    if (part.op !== "?") {
      const value = data[part.names[0]] as Value | undefined | null;
      if (value === undefined || value === null) continue;
      const values = Array.isArray(value) ? value.map(text) : [text(value)];
      out += values.map(part.op === "+" ? reserved : unreserved).join(",");
      continue;
    }
    const pairs: string[] = [];
    for (const name of part.names) {
      const key = name.slice(0, -1);
      const value = data[key] as Value | undefined | null;
      if (value === undefined || value === null) continue;
      for (const item of Array.isArray(value) ? value : [value]) {
        if (item !== undefined && item !== null) pairs.push(`${key}=${unreserved(text(item))}`);
      }
    }
    if (pairs.length) out += `?${pairs.join("&")}`;
  }
  return out;
}
