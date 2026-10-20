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
  target: "es2020",
  minify: true,
  legalComments: "eof",
  logLevel: "warning",
});
