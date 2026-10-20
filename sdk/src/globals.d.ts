// The globals the workflow runtime provides besides ECMAScript itself.
// dist/workflow.d.ts carries them for the web app's script editor.

declare class TextEncoder {
  encode(input?: string): Uint8Array;
}

declare class TextDecoder {
  decode(input?: ArrayBuffer | ArrayBufferView): string;
}

// console writes to the run's log, like ctx.log.
declare var console: {
  log(...values: unknown[]): void;
  info(...values: unknown[]): void;
  debug(...values: unknown[]): void;
  warn(...values: unknown[]): void;
  error(...values: unknown[]): void;
};

declare var crypto: {
  randomUUID(): string;
};
