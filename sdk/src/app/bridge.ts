// The app's side of the bridge to the PaperGo page. The app runs in a
// sandboxed frame with no origin, network or login of its own: every API
// request goes to the page by postMessage, and the page sends it as the
// viewer and answers with the response. The page alone talks to the API.

import type { HostResponse } from "../host.js";

/** Where an app asks PaperGo to go; the app cannot navigate itself. */
export type OpenTarget = { itemId: string } | { taskId: string } | { listId: string };

/** A live change of an item the viewer may see. */
export interface Change {
  id: string;
  /** The item's list or library, when the page knows it. */
  collectionId?: string;
  /** Whether the item was removed or is no longer visible. */
  removed?: boolean;
}

// Messages the app sends.
export type AppMessage =
  | { type: "papergo:request"; id: number; method: string; url: string; headers: Record<string, string>; body?: string | ArrayBuffer }
  | { type: "papergo:open"; target: OpenTarget }
  | { type: "papergo:ready" };

// Messages the page sends.
export type PageMessage = { type: "papergo:response"; id: number; response: HostResponse } | { type: "papergo:change"; changes: Change[] };

interface Port {
  post(message: AppMessage): void;
  listen(handler: (message: PageMessage) => void): void;
}

// The frame's parent, which is the PaperGo page. Messages from anywhere else
// are ignored.
const parentPort: Port = {
  post: (message) => window.parent.postMessage(message, "*"),
  listen: (handler) =>
    window.addEventListener("message", (event) => {
      if (event.source !== window.parent) return;
      const data = event.data as PageMessage | undefined;
      if (data && typeof data === "object" && typeof data.type === "string" && data.type.startsWith("papergo:")) handler(data);
    }),
};

export class Bridge {
  private next = 1;
  private readonly pending = new Map<number, (response: HostResponse) => void>();
  private readonly watchers = new Set<(changes: Change[]) => void>();

  constructor(private readonly port: Port = parentPort) {
    port.listen((message) => {
      if (message.type === "papergo:response") {
        const resolve = this.pending.get(message.id);
        this.pending.delete(message.id);
        resolve?.(message.response);
      } else if (message.type === "papergo:change") {
        for (const watcher of this.watchers) watcher(message.changes);
      }
    });
  }

  /** Sends a request to the API through the page. url is a path with its query. */
  request(method: string, url: string, headers: Record<string, string>, body: string | ArrayBuffer | undefined): Promise<HostResponse> {
    const id = this.next++;
    return new Promise((resolve) => {
      this.pending.set(id, resolve);
      this.port.post({ type: "papergo:request", id, method, url: path(url), headers, body });
    });
  }

  open(target: OpenTarget): void {
    this.port.post({ type: "papergo:open", target });
  }

  ready(): void {
    this.port.post({ type: "papergo:ready" });
  }

  /** Calls watcher with the live changes of items; returns what stops it. */
  watch(watcher: (changes: Change[]) => void): () => void {
    this.watchers.add(watcher);
    return () => this.watchers.delete(watcher);
  }
}

/** path drops the origin the generated client and next links put first. */
export function path(url: string): string {
  return url.replace(/^[a-z][a-z0-9+.-]*:\/\/[^/]*/i, "");
}
