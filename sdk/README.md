# @papergo/workflow

The API PaperGo workflow scripts are written against. The engine is described in [the script engine plan](../docs/plans/script-engine.md).

```sh
npm ci --prefix sdk
npm --prefix sdk run generate   # regenerate src/client after api/openapi.json changes
npm --prefix sdk run typecheck
npm --prefix sdk run build      # rebuild dist/sdk.js, which Go embeds, and dist/workflow.d.ts
```

`src/client/`, `dist/sdk.js` and `dist/workflow.d.ts` are generated and committed. `dist/workflow.d.ts` holds the SDK's declarations, Kiota's and the runtime's globals (`src/globals.d.ts`) in one file; the web app's script editor checks scripts against it. `scope.json` lists the API paths scripts cannot call; the generator and the engine both read it.
