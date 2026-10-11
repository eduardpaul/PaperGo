# @papergo/client

Typed JavaScript/TypeScript client for the PaperGo REST API. `src/generated` is generated with
[Kiota](https://learn.microsoft.com/openapi/kiota/) (MIT) from [`api/openapi.json`](../../api/openapi.json) and is
never edited by hand. `src/runtime` adds what the generator cannot express. The package is ES modules for Node 20+
and browsers, with type declarations.

```bash
npm install && npm run build    # in sdk/typescript
```

## Usage

```js
import { createClient, ifMatch, isProblem } from '@papergo/client';

const api = createClient({ baseUrl: 'https://dms.example.com', token: () => getAccessToken() });

const workspace = await api.v1.workspaces.post({ name: 'Projects' });
const list = await api.v1.resources.byId(workspace.id).children.post({ kind: 'list', name: 'Invoices' });
const item = await api.v1.resources.byId(list.id).children.post({
  kind: 'item',
  name: 'INV-1',
  values: { additionalData: { status: 'open', amount: '12.50' } },   // custom field values by key
});

try {
  await api.v1.resources.byId(item.id).patch({ name: 'INV-001' }, ifMatch(item.version));
} catch (error) {
  if (isProblem(error, 'conflict')) { /* changed by someone else: reload */ }
  if (isProblem(error, 'validation_failed')) console.log(error.detail, error.errors);
}

const page = await api.v1.resources.byId(list.id).children.get({ queryParameters: { limit: 100 } });
// page.data, and page.nextCursor for queryParameters.after
```

- **Tokens:** `token` is a string or a function that returns the current OIDC access token. It is sent only to the
  host of `baseUrl`.
- **ETags:** every versioned entity has `version`; pass it to `ifMatch(version)` for writes that require `If-Match`.
- **Errors:** failed calls reject with the generated `Problem` (RFC 9457): `responseStatusCode`, a stable `code`,
  `detail`, `requestId`, schema `errors` and, for bulk requests, `operationIndex`.
- **Content:** `api.v1.items.byId(id).content.put(bytes, mediaType, { headers: { ...ifMatch(version).headers,
  'X-Filename': name } })` uploads; `content.get()` returns an `ArrayBuffer`.

## Regenerating

`go generate ./internal/httpapi` (at the repository root) writes the OpenAPI document and then this client with the
Kiota version pinned in `.config/dotnet-tools.json`, so it needs the .NET SDK. CI fails when either is stale.

`npm test` builds the package and runs `test/*.test.mjs` against a real API: `test/server` starts it on a free port
with a temporary, migrated SQLite database and development authentication (needs Go).

The Kiota runtime packages are pinned to the version that `kiota info -l TypeScript` names for the pinned generator.
Update them together with the tool. Runtimes from 1.0.0-preview.108 add the HTTP QUERY method, which breaks the
generated `query` navigation (`/query` endpoints) of Kiota 1.35.
