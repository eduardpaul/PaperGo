// A test fails when it logs a console warning or error: React, jsdom and the
// libraries warn about a missing provider, an update outside act or an API
// jsdom lacks. A test that expects one spies on the console method itself.
import { afterEach } from 'vitest';

const logged: string[] = [];
for (const level of ['warn', 'error'] as const) {
  const original = console[level].bind(console);
  console[level] = (...args: unknown[]) => {
    logged.push(`console.${level}: ${args.map(String).join(' ')}`);
    original(...args);
  };
}
// jsdom reports what it does not implement on its own console.
const { jsdom } = globalThis as { jsdom?: { virtualConsole: { on(event: 'jsdomError', listener: (error: Error) => void): void } } };
jsdom?.virtualConsole.on('jsdomError', (error) => logged.push(`jsdom: ${error.message}`));

afterEach(() => {
  if (logged.length) throw new Error(`the test logged:\n${logged.splice(0).join('\n')}`);
});
