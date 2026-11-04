// Runs the SDK's tests: each test/**/*.test.ts is bundled for Node, with the
// aliases the SDK build uses, and run. test/app holds the app runtime's.
import { readdir, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const sdk = join(dirname(fileURLToPath(import.meta.url)), "..");
const out = await mkdtemp(join(tmpdir(), "papergo-sdk-test-"));
try {
  const tests = (await readdir(join(sdk, "test"), { recursive: true })).filter((f) => f.endsWith(".test.ts"));
  for (const name of tests) {
    const file = join(out, name.replace(/[\\/]/g, "-").replace(/\.ts$/, ".mjs"));
    await build({
      entryPoints: [join(sdk, "test", name)],
      outfile: file,
      bundle: true,
      format: "esm",
      platform: "node",
      alias: {
        "@opentelemetry/api": join(sdk, "src", "otel.ts"),
        "@std-uritemplate/std-uritemplate": join(sdk, "src", "stduritemplate.ts"),
      },
      logLevel: "warning",
    });
    execFileSync(process.execPath, [file], { stdio: "inherit" });
  }
} finally {
  await rm(out, { recursive: true, force: true });
}
