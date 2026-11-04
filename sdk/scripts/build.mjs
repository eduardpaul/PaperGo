// Bundles the SDK into dist/sdk.js, the script the engine runs before a
// workflow. It defines the global __papergoSdk; workflows reach it through
// require("@papergo/workflow"). It also writes dist/workflow.d.ts, the
// SDK's declarations with Kiota's in one file, which the web app's script
// editor checks scripts against.
//
// The app runtime goes to dist/app-runtime.js, which the web app puts into
// an app's sandboxed frame before the app's code, and its declarations to
// dist/app.d.ts, which the app editor checks apps against with React's own.
//
// dist/THIRD-PARTY-NOTICES holds the notices of the npm packages the
// bundles embed, which release archives ship.
import { readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { generateDtsBundle } from "dts-bundle-generator";
import { build } from "esbuild";
import { notices } from "../../scripts/npm-notices.mjs";

const sdk = join(dirname(fileURLToPath(import.meta.url)), "..");
await build({
  entryPoints: [join(sdk, "src", "index.ts")],
  outfile: join(sdk, "dist", "sdk.js"),
  bundle: true,
  format: "iife",
  globalName: "__papergoSdk",
  platform: "neutral",
  mainFields: ["module", "main"],
  // Kiota's tracing has no exporter in the engine (src/otel.ts), and its
  // templates need only a small expander (src/uritemplate.ts).
  alias: {
    "@opentelemetry/api": join(sdk, "src", "otel.ts"),
    "@std-uritemplate/std-uritemplate": join(sdk, "src", "stduritemplate.ts"),
  },
  target: "es2020",
  minify: true,
  legalComments: "eof",
  logLevel: "warning",
});

await build({
  entryPoints: [join(sdk, "src", "app", "runtime.tsx")],
  outfile: join(sdk, "dist", "app-runtime.js"),
  bundle: true,
  format: "iife",
  platform: "browser",
  tsconfig: join(sdk, "tsconfig.app.json"),
  jsx: "automatic",
  define: { "process.env.NODE_ENV": '"production"' },
  alias: {
    "@opentelemetry/api": join(sdk, "src", "otel.ts"),
    "@std-uritemplate/std-uritemplate": join(sdk, "src", "stduritemplate.ts"),
  },
  // The page inlines the runtime in the frame's document.
  supported: { "inline-script": true },
  target: "es2020",
  minify: true,
  legalComments: "eof",
  logLevel: "warning",
});

const [declarations] = generateDtsBundle(
  [{ filePath: join(sdk, "src", "index.ts"), libraries: { inlinedLibraries: ["@microsoft/kiota-abstractions", "@std-uritemplate/std-uritemplate", "tinyduration", "guid-typescript", "@opentelemetry/api"] }, output: { noBanner: true } }],
  { preferredConfigPath: join(sdk, "tsconfig.json") },
);
// The runtime's globals follow the module's declarations.
const globals = (await readFile(join(sdk, "src", "globals.d.ts"), "utf8")).replace(/^declare /gm, "").replace(/^(?=.)/gm, "  ");
await writeFile(join(sdk, "dist", "workflow.d.ts"), `${declarations}\ndeclare global {\n${globals}}\n`);

const [appDeclarations] = generateDtsBundle(
  [{ filePath: join(sdk, "src", "app", "index.ts"), libraries: { inlinedLibraries: ["@microsoft/kiota-abstractions", "@std-uritemplate/std-uritemplate", "tinyduration", "guid-typescript", "@opentelemetry/api"] }, output: { noBanner: true } }],
  { preferredConfigPath: join(sdk, "tsconfig.app.json") },
);
await writeFile(join(sdk, "dist", "app.d.ts"), appDeclarations);
await writeFile(join(sdk, "dist", "THIRD-PARTY-NOTICES"), notices(sdk).text);
