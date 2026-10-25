// withPages gives the request builders of paged lists their all(), which
// scripts/generate.mjs declares on them. A list request is paged when its
// query takes $skiptoken; where both its GET and POST are, all() posts.

import type { NavigationMetadata, RequestMetadata, RequestsMetadata } from "@microsoft/kiota-abstractions";
import { baseUrl } from "./adapter.js";

interface Page {
  value?: unknown[] | null;
  odataNextLink?: string | null;
}

type Send = (...args: unknown[]) => Promise<Page | undefined>;

interface Builder {
  withUrl(url: string): Record<string, Send>;
}

const paged = (metadata: RequestMetadata | undefined) => metadata?.queryParametersMapper?.skiptoken !== undefined;

/** Wraps a Kiota request builder, and those it navigates to, with all() where its list is paged. */
export function withPages<T extends object>(builder: T, navigation?: Record<string, NavigationMetadata>, requests?: RequestsMetadata): T {
  const method = paged(requests?.post) ? "post" : paged(requests?.get) ? "get" : undefined;
  return new Proxy(builder, {
    get(target, property) {
      const name = String(property);
      if (name === "all" && method) return (...args: unknown[]) => all(target as unknown as Builder, method, args);
      if (name === "withUrl") return (url: string) => withPages((target as unknown as Builder).withUrl(url), navigation, requests);
      const value: unknown = Reflect.get(target, property);
      const next = navigation?.[name];
      if (!next) return value;
      if (!next.pathParametersMappings?.length) return withPages(value as object, next.navigationMetadata, next.requestsMetadata);
      return (...args: unknown[]) => withPages((value as (...args: unknown[]) => object)(...args), next.navigationMetadata, next.requestsMetadata);
    },
  });
}

async function all(builder: Builder, method: "get" | "post", args: unknown[]): Promise<unknown[]> {
  const values: unknown[] = [];
  let page = await (builder as unknown as Record<string, Send>)[method](...args);
  for (;;) {
    if (page?.value) values.push(...page.value);
    const link = page?.odataNextLink;
    if (!link) return values;
    // The link carries the API's origin as the server saw it; the path
    // and query are the next page.
    page = await builder.withUrl(baseUrl + link.replace(/^[a-z][a-z0-9+.-]*:\/\/[^/]*/i, ""))[method](...args);
  }
}
