-- Workspaces keep their installed solutions as a setting: 'solutions'
-- joins the keys a setting may have. SQLite changes a CHECK by rebuilding
-- the table.
CREATE TABLE settings_new (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resource_id TEXT REFERENCES resources(id) ON DELETE CASCADE,
    "key" TEXT NOT NULL CHECK ("key" IN ('comments', 'trusted_hosts', 'solutions')),
    "value" TEXT NOT NULL CHECK (json_valid("value") AND json_type("value") = 'object'),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_by TEXT NOT NULL DEFAULT ''
);
INSERT INTO settings_new (id, created_at, updated_at, resource_id, "key", "value", version, updated_by)
SELECT id, created_at, updated_at, resource_id, "key", "value", version, updated_by FROM settings;
DROP TABLE settings;
ALTER TABLE settings_new RENAME TO settings;
CREATE UNIQUE INDEX setting_resource_key ON settings(resource_id, "key") WHERE resource_id IS NOT NULL;
CREATE UNIQUE INDEX setting_organization_key ON settings("key") WHERE resource_id IS NULL;
