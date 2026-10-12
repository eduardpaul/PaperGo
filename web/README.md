# PaperGo web

React UI for the PaperGo `/v1` API. Sign in with a bearer token. Production assets are embedded in the Go executable and served on the API’s port. No Node.js runtime or separate asset directory is needed for deployment.

The dev server proxies `/v1` and `/health` to `http://127.0.0.1:8080` (`PAPERGO_API` overrides the target).

```text
npm ci
npm run dev
```

`npm run typecheck`, `npm test` and `npm run build` check the app. Gaps between this UI and the API are tracked in [`docs/plans/web-frontend-backlog.md`](../docs/plans/web-frontend-backlog.md).

Run `bash scripts/build.sh` (or `./scripts/build.ps1` on Windows) from the repository root to build the frontend and the deployable executable. `web/dist` is generated and ignored by Git; it must exist before building the server or running Go tests and analysis across `./...`. The Docker build and CI build it automatically.
