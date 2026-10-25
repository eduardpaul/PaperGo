/// <reference path="./globals.d.ts" />

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
  type RequestMetadata,
  InMemoryBackingStoreFactory,
} from "@microsoft/kiota-abstractions";
import { host, type HostResponse } from "./host.js";
import { HostParseNode } from "./parsenode.js";
import { expand } from "./uritemplate.js";

/** The origin request builders put in front of every path; the engine strips it. */
export const baseUrl = "http://papergo.workflow";

const textType = /^(application\/([\w.+-]*\+)?json|text\/[\w.+-]+|application\/x-www-form-urlencoded)$/;
const jsonType = /^application\/([\w.+-]*\+)?json$/;

function mediaType(contentType: string | undefined): string {
  return (contentType ?? "").split(";")[0].trim().toLowerCase();
}

/** Reads JSON text the engine returns, such as a wait's operations, as models. */
export function parseCollection<T extends Parsable>(text: string, factory: ParsableFactory<T>): T[] {
  return new HostParseNode(JSON.parse(text), undefined).getCollectionOfObjectValues(factory) ?? [];
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
    return this.content(this.exchange(info), errors)?.getObjectValue(factory);
  }
  async sendCollection<T extends Parsable>(info: RequestInformation, factory: ParsableFactory<T>, errors: ErrorMappings | undefined): Promise<T[] | undefined> {
    return this.content(this.exchange(info), errors)?.getCollectionOfObjectValues(factory);
  }
  async sendCollectionOfPrimitive<T>(info: RequestInformation, type: PrimitiveTypesForDeserializationForCollection, errors: ErrorMappings | undefined): Promise<T[] | undefined> {
    return this.content(this.exchange(info), errors)?.getCollectionOfPrimitiveValues<T>(type);
  }
  async sendPrimitive<T>(info: RequestInformation, type: PrimitiveTypesForDeserialization, errors: ErrorMappings | undefined): Promise<T | undefined> {
    return this.primitive(this.exchange(info), type, errors) as T | undefined;
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

  /**
   * Sends a request the SDK's builders made (src/builders.ts) and reads its
   * response as metadata says, as Kiota's send methods do. headers have
   * lower-case names.
   */
  async call(metadata: RequestMetadata, method: string, url: string, headers: Record<string, string>, content: ArrayBuffer | undefined): Promise<unknown> {
    const response = this.request(method, url, headers, content);
    const errors = metadata.errorMappings;
    const factory = metadata.responseBodyFactory as ParsableFactory<Parsable>;
    switch (metadata.adapterMethodName) {
      case "send":
        return this.content(response, errors)?.getObjectValue(factory);
      case "sendCollection":
        return this.content(response, errors)?.getCollectionOfObjectValues(factory);
      case "sendCollectionOfPrimitive":
        return this.content(response, errors)?.getCollectionOfPrimitiveValues(metadata.responseBodyFactory as unknown as PrimitiveTypesForDeserializationForCollection);
      case "sendPrimitive":
        return this.primitive(response, metadata.responseBodyFactory as unknown as PrimitiveTypesForDeserialization, errors);
      case "sendNoResponseContent":
        this.check(response, errors);
        return undefined;
      case "sendEnum":
        return this.content(response, errors)?.getEnumValue(metadata.enumObject);
      case "sendCollectionOfEnum":
        return this.content(response, errors)?.getCollectionOfEnumValues(metadata.enumObject);
      default:
        throw new Error("couldn't find adapter method");
    }
  }

  private primitive(response: HostResponse, type: PrimitiveTypesForDeserialization, errors: ErrorMappings | undefined): unknown {
    if (type === "ArrayBuffer") {
      this.check(response, errors);
      return isEmpty(response) ? undefined : bytes(response);
    }
    const node = this.content(response, errors);
    if (!node) return undefined;
    switch (type) {
      case "string":
        return node.getStringValue();
      case "number":
        return node.getNumberValue();
      case "boolean":
        return node.getBooleanValue();
      case "Date":
        return node.getDateValue();
      case "DateOnly":
        return node.getDateOnlyValue();
      case "TimeOnly":
        return node.getTimeOnlyValue();
      case "Duration":
        return node.getDurationValue();
      default:
        throw new Error(`unsupported primitive response type ${String(type)}`);
    }
  }

  private exchange(info: RequestInformation): HostResponse {
    info.pathParameters["baseurl"] = this.baseUrl;
    const headers: Record<string, string> = {};
    // Kiota's Headers keeps its values outside the Map it extends; only its
    // own forEach sees them.
    info.headers.forEach((values, name) => {
      headers[name.toLowerCase()] = Array.from(values).join(", ");
    });
    // RequestInformation.URL expands with std-uritemplate; see uritemplate.ts.
    const raw = info.pathParameters["request-raw-url"];
    const url = typeof raw === "string" ? raw : expand(info.urlTemplate ?? "", { ...info.queryParameters, ...info.pathParameters });
    return this.request(info.httpMethod ?? "GET", url, headers, info.content);
  }

  private request(method: string, url: string, headers: Record<string, string>, content: ArrayBuffer | undefined): HostResponse {
    let body: string | ArrayBuffer | undefined = content;
    if (body !== undefined && textType.test(mediaType(headers["content-type"]))) body = new TextDecoder().decode(body);
    return host.request(method, url, headers, body);
  }

  private content(response: HostResponse, errors: ErrorMappings | undefined): ParseNode | undefined {
    this.check(response, errors);
    if (isEmpty(response)) return undefined;
    return this.parse(response);
  }

  // JSON text is parsed once and read by HostParseNode, which gives what
  // Kiota's JSON parse node gives with far fewer allocations; encoding it
  // to bytes for the factory and decoding it again costs more than both.
  private parse(response: HostResponse): ParseNode {
    const type = mediaType(response.headers["content-type"]);
    if (!response.bytes && jsonType.test(type)) return new HostParseNode(JSON.parse(response.body), this.store);
    return this.parseNodes.getRootParseNode(type, bytes(response));
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
      const node = this.parse(response);
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
  return response.status === 204 || response.status === 304 || (response.body === "" && !response.bytes?.byteLength);
}

function bytes(response: HostResponse): ArrayBuffer {
  if (response.bytes) return response.bytes;
  const view = new TextEncoder().encode(response.body);
  return view.buffer.slice(view.byteOffset, view.byteOffset + view.byteLength) as ArrayBuffer;
}
