import { AllowedHostsValidator, BaseBearerTokenAuthenticationProvider, type RequestConfiguration } from '@microsoft/kiota-abstractions';
import { DefaultRequestAdapter } from '@microsoft/kiota-bundle';
import type { Problem } from '../generated/models/index.js';
import { createPaperGoClient, type PaperGoClient } from '../generated/paperGoClient.js';

export interface ClientOptions {
  /** API base URL, e.g. https://dms.example.com. */
  baseUrl: string;
  /** Bearer token (an OIDC access token, or the development token), or a function that returns a current one. */
  token: string | (() => string | Promise<string>);
}

/** Creates an API client. The token is sent only to the host of baseUrl. */
export function createClient({ baseUrl, token }: ClientOptions): PaperGoClient {
  const url = new URL(baseUrl);
  const hosts = new AllowedHostsValidator(new Set([url.hostname]));
  const auth = new BaseBearerTokenAuthenticationProvider({
    getAuthorizationToken: async (requestUrl) =>
      hosts.isUrlHostValid(requestUrl ?? '') ? (typeof token === 'string' ? token : await token()) : '',
    getAllowedHostsValidator: () => hosts,
  });
  const adapter = new DefaultRequestAdapter(auth);
  adapter.baseUrl = url.href.replace(/\/+$/, '');
  return createPaperGoClient(adapter);
}

/** Request configuration with the If-Match precondition of a versioned write: pass the entity's current version. */
export function ifMatch(version: number | null | undefined): RequestConfiguration<object> {
  if (!Number.isInteger(version) || version! < 1) {
    throw new TypeError('ifMatch needs the positive integer version of the entity being changed');
  }
  return { headers: { 'If-Match': `"${version}"` } };
}

/** Whether a call failed with an API problem (RFC 9457), optionally with the given stable code. */
export function isProblem(error: unknown, code?: string): error is Problem {
  const problem = error as Problem | undefined;
  return typeof problem?.code === 'string' && typeof problem.responseStatusCode === 'number' && (code === undefined || problem.code === code);
}
