# @papergo/workflow

The API PaperGo workflow scripts are written against. The engine is described in [the script engine plan](../docs/plans/script-engine.md).

```sh
npm ci --prefix sdk
npm --prefix sdk run generate   # regenerate src/client after api/openapi.json changes
npm --prefix sdk run typecheck
npm --prefix sdk run build      # rebuild dist/sdk.js, which Go embeds
```

`src/client/` and `dist/sdk.js` are generated and committed. `scope.json` lists the API paths scripts cannot call; the generator and the engine both read it.
