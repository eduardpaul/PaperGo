// Bundles the SDK into dist/sdk.js, the script the engine runs before a
// workflow. It defines the global __papergoSdk; workflows reach it through
// require("@papergo/workflow").
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

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
