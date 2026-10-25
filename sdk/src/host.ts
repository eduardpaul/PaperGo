// The functions the workflow engine installs as globalThis.__papergo. Every
// call is a recorded step: on replay the engine returns the recorded result.

export interface HostResponse {
  status: number;
  headers: Record<string, string>;
  /** The body as UTF-8 text; empty when it is binary. */
  body: string;
  /** A binary body. */
  bytes?: ArrayBuffer;
}

export type LogLevel = "info" | "warn" | "error";

interface Host {
  /** Runs an API request in-process as the workflow version's author. */
  request(method: string, url: string, headers: Record<string, string>, body: string | ArrayBuffer | undefined): HostResponse;
  /**
   * Returns once none of the operations is pending, with them as they
   * ended: a JSON array of operations. The run is unloaded while it waits
   * and replayed when the last one finishes.
   */
  wait(operationIds: string[]): string;
  /**
   * Returns at or after a time, given in milliseconds since the epoch. The
   * run is unloaded while it sleeps.
   */
  sleep(until: number): void;
  uuid(): string;
  log(level: LogLevel, message: string): void;
}

declare const __papergo: Host;

export const host: Host = {
  request: (method, url, headers, body) => __papergo.request(method, url, headers, body),
  wait: (operationIds) => __papergo.wait(operationIds),
  sleep: (until) => __papergo.sleep(until),
  uuid: () => __papergo.uuid(),
  log: (level, message) => __papergo.log(level, message),
};
