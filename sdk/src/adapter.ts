// HostRequestAdapter sends Kiota requests to the workflow engine instead of the
// network. The engine runs each request in-process as the workflow version's
// author and records it as one exactly-once step, so a replay returns the
// recorded response without calling the API again.

import {
  DefaultApiError,
  ParseNodeFactoryRegistry,
  SerializationWriterFactoryRegistry,
  type BackingStoreFactory,
  type ErrorMappings,
  type Parsable,
  type ParsableFactory,
  type ParseNode,
  type PrimitiveTypesForDeserialization,
  type PrimitiveTypesForDeserializationForCollection,
  type RequestAdapter,
  type RequestInformation,
  InMemoryBackingStoreFactory,
} from "@microsoft/kiota-abstractions";
import { host, type HostResponse } from "./host.js";

/** The origin request builders put in front of every path; the engine strips it. */
export const baseUrl = "http://papergo.workflow";

const textType = /^(application\/([\w.+-]*\+)?json|text\/[\w.+-]+|application\/x-www-form-urlencoded)$/;

function mediaType(contentType: string | undefined): string {
  return (contentType ?? "").split(";")[0].trim().toLowerCase();
}

export class HostRequestAdapter implements RequestAdapter {
  baseUrl = baseUrl;
  private readonly parseNodes = new ParseNodeFactoryRegistry();
  private readonly writers = new SerializationWriterFactoryRegistry();
  private readonly store = new InMemoryBackingStoreFactory();

  getSerializationWriterFactory(): SerializationWriterFactoryRegistry {
    return this.writers;
  }
  getParseNodeFactory(): ParseNodeFactoryRegistry {
    return this.parseNodes;
  }
  getBackingStoreFactory(): BackingStoreFactory {
    return this.store;
  }
  enableBackingStore(): void {
    throw new Error("workflow clients do not use a backing store");
  }
  convertToNativeRequest<T>(): Promise<T> {
    return Promise.reject(new Error("workflow requests have no native form"));
  }

  async send<T extends Parsable>(info: RequestInformation, factory: ParsableFactory<T>, errors: ErrorMappings | undefined): Promise<T | undefined> {
    const node = this.content(this.exchange(info), errors);
    return node?.getObjectValue(factory);
  }
  async sendCollection<T extends Parsable>(info: RequestInformation, factory: ParsableFactory<T>, errors: ErrorMappings | undefined): Promise<T[] | undefined> {
    const node = this.content(this.exchange(info), errors);
    return node?.getCollectionOfObjectValues(factory);
  }
  async sendCollectionOfPrimitive<T>(info: RequestInformation, type: PrimitiveTypesForDeserializationForCollection, errors: ErrorMappings | undefined): Promise<T[] | undefined> {
    const node = this.content(this.exchange(info), errors);
    return node?.getCollectionOfPrimitiveValues<T>(type);
  }
  async sendPrimitive<T>(info: RequestInformation, type: PrimitiveTypesForDeserialization, errors: ErrorMappings | undefined): Promise<T | undefined> {
    const response = this.exchange(info);
    if (type === "ArrayBuffer") {
      this.check(response, errors);
      return (isEmpty(response) ? undefined : bytes(response)) as T | undefined;
    }
    const node = this.content(response, errors);
    if (!node) return undefined;
    switch (type) {
      case "string":
        return node.getStringValue() as T;
      case "number":
        return node.getNumberValue() as T;
      case "boolean":
        return node.getBooleanValue() as T;
      case "Date":
        return node.getDateValue() as T;
      case "DateOnly":
        return node.getDateOnlyValue() as T;
      case "TimeOnly":
        return node.getTimeOnlyValue() as T;
      case "Duration":
        return node.getDurationValue() as T;
      default:
        throw new Error(`unsupported primitive response type ${String(type)}`);
    }
  }
  async sendNoResponseContent(info: RequestInformation, errors: ErrorMappings | undefined): Promise<void> {
    this.check(this.exchange(info), errors);
  }
  async sendEnum<E extends Record<string, unknown>>(info: RequestInformation, type: E, errors: ErrorMappings | undefined): Promise<E[keyof E] | undefined> {
    return this.content(this.exchange(info), errors)?.getEnumValue(type);
  }
  async sendCollectionOfEnum<E extends Record<string, unknown>>(info: RequestInformation, type: E, errors: ErrorMappings | undefined): Promise<E[keyof E][] | undefined> {
    return this.content(this.exchange(info), errors)?.getCollectionOfEnumValues(type);
  }

  private exchange(info: RequestInformation): HostResponse {
    info.pathParameters["baseurl"] = this.baseUrl;
    const headers: Record<string, string> = {};
    // Kiota's Headers keeps its values outside the Map it extends; only its
    // own forEach sees them.
    info.headers.forEach((values, name) => {
      headers[name.toLowerCase()] = Array.from(values).join(", ");
    });
    let body: string | undefined;
    let encoded = false;
    if (info.content !== undefined) {
      if (textType.test(mediaType(headers["content-type"]))) {
        body = new TextDecoder().decode(info.content);
      } else {
        body = Buffer.from(info.content).toString("base64");
        encoded = true;
      }
    }
    return host.request(info.httpMethod ?? "GET", info.URL, headers, body, encoded);
  }

  private content(response: HostResponse, errors: ErrorMappings | undefined): ParseNode | undefined {
    this.check(response, errors);
    if (isEmpty(response)) return undefined;
    return this.parseNodes.getRootParseNode(mediaType(response.headers["content-type"]), bytes(response));
  }

  private check(response: HostResponse, errors: ErrorMappings | undefined): void {
    if (response.status < 400) return;
    const headers: Record<string, string[]> = {};
    for (const name of Object.keys(response.headers)) headers[name] = response.headers[name].split(",");
    const factory =
      errors?.[response.status] ??
      (response.status < 500 ? errors?._4XX : errors?._5XX) ??
      errors?.XXX;
    let error: Error & { responseStatusCode?: number; responseHeaders?: Record<string, string[]> };
    if (factory && !isEmpty(response)) {
      const node = this.parseNodes.getRootParseNode(mediaType(response.headers["content-type"]), bytes(response));
      const problem = node.getObjectValue(factory) as unknown as Error & { code?: string; detail?: string };
      error = problem instanceof Error ? problem : Object.assign(new DefaultApiError(), problem);
      // A problem's detail says what was wrong; Kiota leaves the message empty.
      error.message = [`request failed with status ${response.status}`, problem.code, problem.detail].filter(Boolean).join(": ");
    } else {
      error = new DefaultApiError(`request failed with status ${response.status}`);
    }
    error.responseStatusCode = response.status;
    error.responseHeaders = headers;
    throw error;
  }
}

function isEmpty(response: HostResponse): boolean {
  return response.status === 204 || response.status === 304 || response.body === "";
}

function bytes(response: HostResponse): ArrayBuffer {
  const view = response.base64 ? new Uint8Array(Buffer.from(response.body, "base64")) : new TextEncoder().encode(response.body);
  return view.buffer.slice(view.byteOffset, view.byteOffset + view.byteLength) as ArrayBuffer;
}
