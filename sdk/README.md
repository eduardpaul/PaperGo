# @papergo/workflow

The API PaperGo workflow scripts are written against. The engine is described in [the script engine plan](../docs/plans/script-engine.md).

```sh
npm ci --prefix sdk
npm --prefix sdk run generate   # regenerate src/client after api/openapi.json changes
npm --prefix sdk run typecheck
npm --prefix sdk test           # HostParseNode reads every model as Kiota's JSON parse node does
npm --prefix sdk run build      # rebuild dist/sdk.js, which Go embeds, and dist/workflow.d.ts
```

`src/client/`, `dist/sdk.js` and `dist/workflow.d.ts` are generated and committed. `dist/workflow.d.ts` holds the SDK's declarations, Kiota's and the runtime's globals (`src/globals.d.ts`) in one file; the web app's script editor checks scripts against it. `scope.json` lists the API paths scripts cannot call; the generator and the engine both read it.

Scripts use the SDK's own request builders (`src/builders.ts`), not Kiota's: a replay makes every recorded request again, and Kiota's Proxy-based builders and `RequestInformation` cost most of a request's time. Each kind of builder is a plain prototype made once from Kiota's generated metadata, and requests go to the adapter as method, URL, headers and body. `test/builders.test.ts` makes every request of the client through both and requires the same requests and results. Kiota's client still registers the serializers.

The generator gives the builder of every paged list (a GET or POST that takes `$skiptoken`) an `all()` declaration; `src/builders.ts` implements it by following `@odata.nextLink`, asking the SDK's origin with the link's path and query. `test/pages.test.ts` checks that every paged list a script can call has it.

The request adapter (`src/adapter.ts`) reads JSON responses with `HostParseNode` (`src/parsenode.ts`), which follows the rules of Kiota's `JsonParseNode` with a fraction of its allocations, since a replay reads every recorded response again. `test/parsenode.test.ts` feeds every model's fields values of every JSON type through both and requires equal results.
