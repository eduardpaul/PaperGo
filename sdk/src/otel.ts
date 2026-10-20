// The bundle replaces @opentelemetry/api with this no-op tracer. Kiota
// traces every request, but a workflow run has no exporter, and the real
// API costs 73 KB of bundle and runtime set-up in every slice.

interface Span {
  setAttribute(): Span;
  setAttributes(): Span;
  setStatus(): Span;
  addEvent(): Span;
  updateName(): Span;
  recordException(): void;
  end(): void;
  isRecording(): boolean;
}

const span: Span = {
  setAttribute: () => span,
  setAttributes: () => span,
  setStatus: () => span,
  addEvent: () => span,
  updateName: () => span,
  recordException: () => undefined,
  end: () => undefined,
  isRecording: () => false,
};

const tracer = {
  startSpan: () => span,
  startActiveSpan(...args: unknown[]): unknown {
    const fn = args[args.length - 1] as (span: Span) => unknown;
    return fn(span);
  },
};

export const trace = { getTracer: () => tracer };
export const SpanStatusCode = { UNSET: 0, OK: 1, ERROR: 2 } as const;
