# Apps

An **app** is a React screen that belongs to a workspace, as a [workflow](workflows.md) does. Its people open it from the workspace. It reads and changes the workspace's data through the REST API, as the person viewing it, and never creates lists: it points at lists that exist through its parameters.

| | Workflow | App |
| --- | --- | --- |
| Source | one TypeScript file, compiled by the server | one TSX file, compiled by the server |
| Declares | triggers, forms, concurrency | its parameters form |
| Versions | every save is an immutable version | every new source is an immutable version |
| Acts as | the author of its version | the person viewing it |

## Writing an app

The source's default export is `app()` from `@papergo/app`, with a `render` component and an optional `parameters` [form](workflows.md#forms):

```tsx
import { app } from "@papergo/app";

export default app({
  parameters: {
    type: "object",
    required: ["list"],
    properties: {
      list: { type: "string", title: "Diesel list", "x-papergo": { kind: "collection" } },
    },
  },
  render(ctx) {
    return <p>Readings are kept in {ctx.params.list}.</p>;
  },
});
```

**Compiling.** Saving compiles the source with the server's esbuild:
- TSX with automatic JSX.
- One file of up to 1 MiB.
- It imports only `react`, `react/jsx-runtime` and `@papergo/app`, which the app's runtime supplies; any other import is refused, naming it.

The server then loads the code with stand-ins for those modules to read the parameters form. Loading must finish within two seconds and must not throw. A source that does not compile, exports no `app({ render })` or declares an invalid form is refused with 422 and the reason.

**Parameters** are the values of that form, which the app reads as `ctx.params`. Pickers let the people who set up the app choose the workspace's lists, content types or items instead of typing IDs. Saving checks the values as the person who saves:
- their types and picks always, so a draft can be saved before its lists are chosen;
- the form's required fields once the app is published.

## Who may do what

| Action | Needs |
| --- | --- |
| See and open published apps | read access to the workspace |
| Create, change, publish and delete apps; see drafts, sources and versions | manage access to the workspace and the `app.manage` scope |

An app acts as everyone who opens it, so `app.manage` is not granted to the Member role; administrators have it (see [identity](identity.md)). Limited access tokens cannot hold it.

Creating, changing and deleting an app are audited as `app.create`, `app.update` and `app.delete` in the workspace's audit trail, with the app's ID, key and version.

## API

| Method | Route | Purpose |
| --- | --- | --- |
| GET / POST | `/v1/workspaces/{id}/apps` | List the apps the caller may see, by name, without their source; or create one: `{"name", "description", "source", "parameters", "state", "key"}` |
| GET / PUT / DELETE | `/v1/apps/{id}` | Read; replace name, description, state, parameters and source (`If-Match`); delete with its versions (`If-Match`) |
| GET | `/v1/apps/{id}/versions` | Versions with their sources and forms, newest first |
| GET | `/v1/apps/{id}/bundle` | The current version's compiled code, for the app page; its `ETag` is the code's hash and `PaperGo-App-Version` the version |

- **Keys.** An app's `key` comes from its name unless creation gives one, and never changes. Keys and names are unique in a workspace.
- **States.** A new app is a `draft` unless it is created `published`.
- **Versions.** A replacement with a new source becomes the next version; one with the same source keeps the current version.
- **Not for scripts.** Workflow scripts cannot call these routes.

The complete contract is in [OpenAPI](../api/openapi.json).
