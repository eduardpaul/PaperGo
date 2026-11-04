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
  /** The page's live stream reconnected and may have missed changes: read everything again. */
  resync?: boolean;
}

// Messages the app sends.
export type AppMessage =
  | { type: "papergo:request"; id: number; method: string; url: string; headers: Record<string, string>; body?: string | ArrayBuffer }
  | { type: "papergo:open"; target: OpenTarget };

// Messages the page sends.
export type PageMessage = { type: "papergo:response"; id: number; response: HostResponse } | { type: "papergo:change"; changes: Change[] };

export interface Port {
  post(message: AppMessage): void;
  listen(handler: (message: PageMessage) => void): void;
}

/**
 * The port to the PaperGo page. The runtime asks the page for a
 * MessageChannel port as soon as it loads, before the app's own code runs,
 * and the page answers only that first request: everything then travels
 * over the port, which no other document can reach and which ends with this
 * one. Messages sent before the port arrives wait for it.
 */
export function parentPort(): Port {
  let port: MessagePort | undefined;
  let handler: ((message: PageMessage) => void) | undefined;
  const waiting: AppMessage[] = [];
  const send = (p: MessagePort, message: AppMessage) => p.postMessage(message, message.type === "papergo:request" && message.body instanceof ArrayBuffer ? [message.body] : []);
  const receive = (event: MessageEvent) => {
    if (port || event.source !== window.parent || (event.data as { type?: string } | undefined)?.type !== "papergo:port" || !event.ports[0]) return;
    port = event.ports[0];
    window.removeEventListener("message", receive);
    port.onmessage = (e: MessageEvent) => {
      const data = e.data as PageMessage | undefined;
      if (data && typeof data === "object" && typeof data.type === "string") handler?.(data);
    };
    for (const message of waiting.splice(0)) send(port, message);
  };
  window.addEventListener("message", receive);
  window.parent.postMessage({ type: "papergo:hello" }, "*");
  return {
    post: (message) => (port ? send(port, message) : void waiting.push(message)),
    listen: (h) => {
      handler = h;
    },
  };
}

export class Bridge {
  private next = 1;
  private readonly pending = new Map<number, (response: HostResponse) => void>();
  private readonly watchers = new Set<(changes: Change[]) => void>();

  constructor(private readonly port: Port) {
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
