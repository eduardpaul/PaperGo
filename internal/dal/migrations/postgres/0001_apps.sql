-- The apps of workspaces and their versions.
CREATE TABLE apps (
    id TEXT PRIMARY KEY NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workspace_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE DEFERRABLE,
    "key" TEXT NOT NULL CHECK (length("key") BETWEEN 1 AND 100),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
    state TEXT NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'published')),
    parameters JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(parameters) = 'object'),
    current_version BIGINT NOT NULL CHECK (current_version > 0),
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0)
);
CREATE UNIQUE INDEX app_workspace_id_key ON apps(workspace_id, "key");
CREATE UNIQUE INDEX app_workspace_id_name ON apps(workspace_id, name);

CREATE TABLE app_versions (
    id TEXT PRIMARY KEY NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE DEFERRABLE,
    number BIGINT NOT NULL CHECK (number > 0),
    source TEXT NOT NULL CHECK (length(source) BETWEEN 1 AND 1048576),
    bundle TEXT NOT NULL CHECK (length(bundle) >= 1),
    bundle_hash TEXT NOT NULL CHECK (length(bundle_hash) = 64),
    parameters_schema JSONB NOT NULL CHECK (jsonb_typeof(parameters_schema) = 'object'),
    created_by TEXT NOT NULL
);
CREATE UNIQUE INDEX appversion_app_id_number ON app_versions(app_id, number);
