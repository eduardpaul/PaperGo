// Writes the third-party notices of an npm project's production
// dependencies: every package-lock.json entry that is not "dev", read from
// the installed node_modules, with its name, version, licence field and the
// text of each licence, copying and notice file at its root. Optional
// packages npm did not install for this platform cannot be in a bundle and
// are left out.
//
// It fails on a licence outside those docs/dependencies.md allows, unless
// REVIEWED records the package.
//
//   node scripts/npm-notices.mjs <project directory> <output file>
//
// The web build writes web/dist/THIRD-PARTY-NOTICES with it, and the SDK
// build sdk/dist/THIRD-PARTY-NOTICES.
import { readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// The SPDX identifiers of the licences docs/dependencies.md allows.
export const ALLOWED = new Set([
  "MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "0BSD", "ISC", "Zlib", "BSL-1.0", "PostgreSQL",
]);
// Packages whose licence is outside ALLOWED, reviewed by hand: name ->
// the exact licence field reviewed. A different licence needs a new review.
export const REVIEWED = {
  // A dependency of @tanstack/react-router that detects crawlers during
  // server rendering. The Unlicense dedicates it to the public domain.
  isbot: "Unlicense",
};
const LICENSE_FILE = /^((un)?licen[cs]e|copying|notice)([-_.][\w.-]*)?$/i;
const SEPARATOR = `\n${"=".repeat(78)}\n\n`;

// Whether an SPDX expression grants at least one allowed licence choice:
// OR needs either side, AND both, and an exception (WITH) keeps its licence.
export function allowed(expression) {
  const tokens = String(expression).match(/\(|\)|[^\s()]+/g) ?? [];
  let position = 0;
  const next = () => tokens[position++];
  const peek = () => tokens[position];
  function factor() {
    const token = next();
    if (token === "(") {
      const value = or();
      if (next() !== ")") throw new Error(`unbalanced SPDX expression: ${expression}`);
      return value;
    }
    if (token === undefined || token === ")" || /^(AND|OR|WITH)$/i.test(token))
      throw new Error(`invalid SPDX expression: ${expression}`);
    if (/^WITH$/i.test(peek() ?? "")) {
      next();
      next();
    }
    return ALLOWED.has(token.replace(/\+$/, ""));
  }
  function and() {
    let value = factor();
    while (/^AND$/i.test(peek() ?? "")) {
      next();
      const right = factor();
      value = value && right;
    }
    return value;
  }
  function or() {
    let value = and();
    while (/^OR$/i.test(peek() ?? "")) {
      next();
      const right = and();
      value = value || right;
    }
    return value;
  }
  const value = or();
  if (position !== tokens.length) throw new Error(`invalid SPDX expression: ${expression}`);
  return value;
}

// The licence a package.json declares: the SPDX "license" field, or the
// legacy "license" object and "licenses" array.
export function declared(manifest) {
  const field = manifest.license ?? manifest.licenses;
  if (typeof field === "string") return field;
  if (Array.isArray(field)) return field.map((entry) => entry.type ?? entry).join(" OR ");
  if (field && typeof field === "object") return field.type;
  return undefined;
}

function check(name, licence) {
  if (REVIEWED[name] !== undefined) {
    if (REVIEWED[name] !== licence)
      throw new Error(`${name} is now licensed ${licence}, not the reviewed ${REVIEWED[name]}; review it and update REVIEWED in scripts/npm-notices.mjs`);
    return;
  }
  if (!licence) throw new Error(`${name} declares no licence; review it and record it in REVIEWED in scripts/npm-notices.mjs`);
  let ok;
  try {
    ok = allowed(licence);
  } catch (error) {
    throw new Error(`${name}: ${error.message}`);
  }
  if (!ok)
    throw new Error(`${name} is licensed ${licence}, which docs/dependencies.md does not allow; review it and record it in REVIEWED in scripts/npm-notices.mjs if it is acceptable`);
}

export function notices(project) {
  const root = resolve(project);
  const lock = JSON.parse(readFileSync(join(root, "package-lock.json"), "utf8"));
  const manifest = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
  const packages = new Map();
  for (const [path, entry] of Object.entries(lock.packages)) {
    if (!path || entry.dev || entry.link) continue;
    const directory = join(root, path);
    let installed;
    try {
      installed = JSON.parse(readFileSync(join(directory, "package.json"), "utf8"));
    } catch {
      if (entry.optional || entry.devOptional) continue;
      throw new Error(`${path} is not installed; run npm ci --prefix ${project}`);
    }
    const key = `${installed.name}@${installed.version}`;
    if (packages.has(key)) continue;
    const licence = declared(installed);
    check(installed.name, licence);
    const files = readdirSync(directory)
      .filter((name) => LICENSE_FILE.test(name) && statSync(join(directory, name)).isFile())
      .sort()
      .map((name) => [name, readFileSync(join(directory, name), "utf8")]);
    packages.set(key, { name: installed.name, version: installed.version, licence, files });
  }
  const entries = [...packages.values()].sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0) || (a.version < b.version ? -1 : 1));
  const header =
    `Third-party notices for the npm packages bundled into ${manifest.name}\n\n` +
    "Generated by scripts/npm-notices.mjs from package-lock.json: every production\n" +
    `dependency, ${entries.length} packages, whether or not the bundle keeps its code.\n\n` +
    entries.map((entry) => `  ${entry.name} ${entry.version} (${entry.licence})\n`).join("");
  const sections = entries.map((entry) => {
    const body = entry.files.length
      ? entry.files.map(([name, text]) => `--- ${name} ---\n\n${text.replace(/\r\n/g, "\n").trimEnd()}\n`).join("\n")
      : `The package ships no licence file; its package.json declares ${entry.licence}.\n`;
    return `${entry.name} ${entry.version}\nLicence: ${entry.licence}\n\n${body}`;
  });
  return { text: header + SEPARATOR + sections.join(SEPARATOR), count: entries.length };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [project, output] = process.argv.slice(2);
  if (!project || !output) {
    console.error("usage: node scripts/npm-notices.mjs <project directory> <output file>");
    process.exit(2);
  }
  try {
    const { text, count } = notices(project);
    writeFileSync(output, text);
    console.log(`npm notices for ${count} packages: ${output}`);
  } catch (error) {
    console.error(`npm notices: ${error.message}`);
    process.exit(1);
  }
}
