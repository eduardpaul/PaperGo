// The functions the workflow engine installs as globalThis.__papergo. Every
// call is a recorded step: on replay the engine returns the recorded result.

export interface HostResponse {
  status: number;
  headers: Record<string, string>;
  /** UTF-8 text, or base64 when base64 is true. */
  body: string;
  base64: boolean;
}

export type LogLevel = "info" | "warn" | "error";

interface Host {
  /** Runs an API request in-process as the workflow version's author. */
  request(method: string, url: string, headers: Record<string, string>, body: string | undefined, base64: boolean): HostResponse;
  /** Runs a platform action and returns its JSON output; it may unload the run. */
  action(name: string, input: string): string;
  uuid(): string;
  log(level: LogLevel, message: string): void;
}

declare const __papergo: Host;

export const host: Host = {
  request: (method, url, headers, body, base64) => __papergo.request(method, url, headers, body, base64),
  action: (name, input) => __papergo.action(name, input),
  uuid: () => __papergo.uuid(),
  log: (level, message) => __papergo.log(level, message),
};
