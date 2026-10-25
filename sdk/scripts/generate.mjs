// Regenerates src/client from api/openapi.json with the pinned Kiota
// generator, leaving out the paths in scope.json.
//
// The @microsoft/kiota package downloads the generator for its version and
// verifies its checksum. Its JSON-RPC Generate call drops the serializer
// registrations, so this script runs the downloaded command-line generator.
import { spawnSync } from "node:child_process";
import { readdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const sdk = join(dirname(fileURLToPath(import.meta.url)), "..");
const install = await import(pathToFileURL(join(sdk, "node_modules", "@microsoft", "kiota", "dist", "install.js")).href);
await install.ensureKiotaIsPresent();

const scope = JSON.parse(await readFile(join(sdk, "scope.json"), "utf8"));
const args = [
  "generate",
  "--language", "typescript",
  "--openapi", join(sdk, "..", "api", "openapi.json"),
  "--class-name", "PaperGoClient",
  "--namespace-name", "PaperGo",
  "--output", join(sdk, "src", "client"),
  "--clean-output",
  "--clear-cache",
  "--exclude-backward-compatible",
  "--log-level", "Warning",
  ...scope.exclude.flatMap((path) => ["--exclude-path", path]),
];
const run = spawnSync(install.getKiotaPath(), args, {
  cwd: sdk,
  encoding: "utf8",
  env: { ...process.env, KIOTA_TUTORIAL_ENABLED: "false", KIOTA_OFFLINE_ENABLED: "true" },
});
// The generator logs a failed update check and unsupported string formats
// as warnings; neither affects the client.
const ignored = /Unable to check for updates|format uri is not supported|^\s+at |ApiException|^warn: /;
for (const line of `${run.stdout}${run.stderr}`.split("\n")) {
  if (line.trim() && !ignored.test(line)) console.error(line);
}
if (run.status !== 0) process.exit(run.status ?? 1);

// Kiota serializers write a property's schema default when a model leaves it
// unset. The server applies its own defaults, and presence matters: an
// update must not send the defaults of fields the caller did not change.
const defaulted = /(writer\.write\w+(?:<[^>]*>)?\("[^"]+", [A-Za-z_$][\w$]*\.[A-Za-z_$][\w$]*) \?\? [^;]+\);/g;
let stripped = 0;
for (const entry of await readdir(join(sdk, "src", "client"), { recursive: true, withFileTypes: true })) {
  if (!entry.isFile() || !entry.name.endsWith(".ts")) continue;
  const file = join(entry.parentPath, entry.name);
  const code = await readFile(file, "utf8");
  const out = code.replace(defaulted, (_, call) => {
    stripped++;
    return `${call});`;
  });
  if (out !== code) await writeFile(file, out);
}
if (stripped === 0) {
  console.error("found no serialized defaults to strip; check the Kiota output");
  process.exit(1);
}

// A GET or POST that takes $skiptoken returns a page of a list linked to the
// next by @odata.nextLink. Its builder gets all(), which src/builders.ts
// implements: it reads every page and returns their values. Where both page
// (/v1/search), all() posts, since the body carries the GET parameters too.
const paged = /^     (post|get)\(((?:body: \w+, )?requestConfiguration\?: RequestConfiguration<(\w+QueryParameters)> \| undefined)\) : Promise<(\w+) \| undefined>;$/gm;
let lists = 0;
for (const entry of await readdir(join(sdk, "src", "client"), { recursive: true, withFileTypes: true })) {
  if (!entry.isFile() || entry.name !== "index.ts") continue;
  const file = join(entry.parentPath, entry.name);
  const code = await readFile(file, "utf8");
  const pages = [...code.matchAll(paged)].filter(([, , , parameters]) =>
    /\n    skiptoken\?: string;\n/.test(code.match(new RegExp(`\\nexport interface ${parameters} \\{[\\s\\S]*?\\n\\}`))?.[0] ?? ""),
  );
  const page = pages.find(([, method]) => method === "post") ?? pages[0];
  if (!page) continue;
  const [, method, signature, , type] = page;
  const body = method === "post" ? "\n     * @param body The request body, sent again for every page" : "";
  const all = `    /**
     * Reads every page of the ${method.toUpperCase()} request, following @odata.nextLink, and returns their values in order. Each page is one request.${body}
     * @param requestConfiguration Configuration for the request; later pages take their query from @odata.nextLink.
     * @returns {Promise<NonNullable<${type}["value"]>>}
     */
     all(${signature}) : Promise<NonNullable<${type}["value"]>>;
`;
  const builder = code.match(/^export interface (\w+RequestBuilder) extends BaseRequestBuilder<\1> \{\n/m);
  if (!builder) {
    console.error(`${file}: found no request builder to add all() to`);
    process.exit(1);
  }
  const out = code
    .replace(builder[0], builder[0] + all)
    // all() is a method, not a navigation property.
    .replace(`Exclude<keyof ${builder[1]}, KeysToExcludeForNavigationMetadata>`, `Exclude<keyof ${builder[1]}, KeysToExcludeForNavigationMetadata | "all">`);
  await writeFile(file, out);
  lists++;
}
if (lists === 0) {
  console.error("found no paged requests to add all() to; check the Kiota output");
  process.exit(1);
}
