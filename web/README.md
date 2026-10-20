# PaperGo web

React UI for the PaperGo `/v1` API. OIDC deployments that set `OIDC_CLIENT_ID` sign in at the identity provider (authorization code with PKCE, `/login/callback`); other bearer deployments sign in with a pasted bearer token, and personal access tokens work in both. Proxy deployments use the browser’s HTTP credentials and automatically load the verified profile. Production assets are embedded in the Go executable and served on the API’s port. No Node.js runtime or separate asset directory is needed for deployment.

The dev server proxies `/v1`, `/auth` and `/health` to `http://127.0.0.1:8080` (`PAPERGO_API` overrides the target).

```text
npm ci
npm run dev
```

`npm run typecheck`, `npm test` and `npm run build` check the app. Gaps between this UI and the API are tracked in [`docs/plans/web-frontend-backlog.md`](../docs/plans/web-frontend-backlog.md); usability principles and open work for workspaces, lists and libraries are in [`docs/plans/web-usability-review.md`](../docs/plans/web-usability-review.md).

Run `bash scripts/build.sh` (or `./scripts/build.ps1` on Windows) from the repository root to build the frontend and the deployable executable. `web/dist` is generated and ignored by Git; it must exist before building the server or running Go tests and analysis across `./...`. The Docker build and CI build it automatically.
