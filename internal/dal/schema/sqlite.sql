-- JSON values use TEXT affinity so SQLite cannot coerce scalar numbers to floating point.
-- PaperGo SQLite Schema (Clean DDL: PKs, FKs, Indexes, and CHECK constraints).
-- Cross-table validations, projections (resource_keywords, item_surface_keywords,
-- group_counts), hierarchy cycle detection, and permission scope cascades (scope_id)
-- are enforced in the Go DAL layer within atomic transactions.

PRAGMA foreign_keys = ON;

-- =============================================================================
-- 1. Identity & Access Directory
-- =============================================================================

CREATE TABLE identity_organizations (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    singleton BOOLEAN NOT NULL DEFAULT TRUE CHECK (singleton IN (0, 1) AND singleton = 1),
    identifier TEXT NOT NULL CHECK (length(identifier) BETWEEN 1 AND 128),
    display_name TEXT NOT NULL CHECK (length(display_name) BETWEEN 1 AND 200),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0)
);
CREATE UNIQUE INDEX identityorganization_singleton ON identity_organizations(singleton);

CREATE TABLE identity_users (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(id) BETWEEN 1 AND 255 AND id = trim(id)),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    active BOOLEAN NOT NULL DEFAULT TRUE CHECK (active IN (0, 1)),
    service BOOLEAN NOT NULL DEFAULT FALSE CHECK (service IN (0, 1)),
    display_name TEXT NOT NULL DEFAULT '' CHECK (length(display_name) <= 200),
    email TEXT NOT NULL DEFAULT '' CHECK (length(email) <= 256),
    sync_hash TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '' CHECK (length(password_hash) <= 200),
    deleted_at DATETIME,
    CHECK (deleted_at IS NULL OR (active = 0 AND display_name = 'Deleted user' AND email = '' AND password_hash = ''))
);

CREATE TABLE identity_sessions (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(id) BETWEEN 1 AND 64),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    subject TEXT NOT NULL REFERENCES identity_users(id) ON DELETE CASCADE,
    secret_hash TEXT NOT NULL CHECK (length(secret_hash) = 64),
    expires_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX identitysession_secret_hash ON identity_sessions(secret_hash);
CREATE INDEX identity_sessions_subject ON identity_sessions(subject);

CREATE TABLE access_tokens (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    subject TEXT NOT NULL REFERENCES identity_users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    prefix TEXT NOT NULL,
    secret_hash TEXT NOT NULL CHECK (length(secret_hash) = 64),
    scopes TEXT NOT NULL CHECK (json_valid(scopes) AND json_type(scopes) = 'array' AND json_array_length(scopes) > 0),
    -- Workspaces, lists and libraries the token is limited to; empty is unlimited.
    resources TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(resources) AND json_type(resources) = 'array'),
    expires_at DATETIME,
    last_used_at DATETIME,
    revoked_at DATETIME,
    integration BOOLEAN NOT NULL DEFAULT FALSE CHECK (integration IN (0, 1)),
    created_by TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX accesstoken_secret_hash ON access_tokens(secret_hash);
CREATE INDEX accesstoken_subject_id ON access_tokens(subject, id);

CREATE TABLE identity_groups (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'local' CHECK (source IN ('local', 'oidc')),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0)
);
CREATE UNIQUE INDEX identitygroup_name ON identity_groups(name);

CREATE TABLE identity_roles (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description TEXT NOT NULL DEFAULT '',
    scopes TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(scopes) AND json_type(scopes) = 'array'),
    builtin BOOLEAN NOT NULL DEFAULT FALSE CHECK (builtin IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0)
);
CREATE UNIQUE INDEX identityrole_name ON identity_roles(name);

CREATE TABLE identity_memberships (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    group_id TEXT NOT NULL REFERENCES identity_groups(id) ON DELETE CASCADE,
    principal_type TEXT NOT NULL CHECK (principal_type IN ('user', 'group')),
    principal_id TEXT NOT NULL CHECK (length(principal_id) > 0),
    CHECK (NOT (principal_type = 'group' AND principal_id = group_id))
);
CREATE UNIQUE INDEX identitymembership_group_id_principal_type_principal_id ON identity_memberships(group_id, principal_type, principal_id);
CREATE INDEX identitymembership_principal_type_principal_id_group_id ON identity_memberships(principal_type, principal_id, group_id);

CREATE TABLE identity_assignments (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    role_id TEXT NOT NULL REFERENCES identity_roles(id) ON DELETE CASCADE,
    principal_type TEXT NOT NULL CHECK (principal_type IN ('user', 'group')),
    principal_id TEXT NOT NULL CHECK (length(principal_id) > 0)
);
CREATE UNIQUE INDEX identityassignment_role_id_principal_type_principal_id ON identity_assignments(role_id, principal_type, principal_id);
CREATE INDEX identityassignment_principal_type_principal_id_role_id ON identity_assignments(principal_type, principal_id, role_id);

CREATE TABLE identity_preferences (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    owner_type TEXT NOT NULL CHECK (owner_type IN ('user', 'organization')),
    owner_id TEXT NOT NULL CHECK (length(owner_id) <= 255 AND ((owner_type = 'user' AND owner_id <> '') OR (owner_type = 'organization' AND owner_id = ''))),
    "values" TEXT NOT NULL DEFAULT '{}' CHECK (json_valid("values") AND json_type("values") = 'object'),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0)
);
CREATE UNIQUE INDEX identitypreference_owner_type_owner_id ON identity_preferences(owner_type, owner_id);

-- =============================================================================
-- 2. Taxonomy & Relationship Types
-- =============================================================================

CREATE TABLE term_groups (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_system BOOLEAN NOT NULL DEFAULT FALSE CHECK (is_system IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0)
);
CREATE UNIQUE INDEX termgroup_name ON term_groups(name);
CREATE UNIQUE INDEX termgroup_system ON term_groups(is_system) WHERE is_system = 1;

CREATE TABLE term_sets (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    group_id TEXT NOT NULL REFERENCES term_groups(id) ON DELETE CASCADE,
    "key" TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_open BOOLEAN NOT NULL DEFAULT FALSE CHECK (is_open IN (0, 1)),
    is_keywords BOOLEAN NOT NULL DEFAULT FALSE CHECK (is_keywords IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    CHECK (NOT is_keywords OR is_open)
);
CREATE UNIQUE INDEX termset_key ON term_sets("key");
CREATE UNIQUE INDEX termset_group_id_name ON term_sets(group_id, name);
CREATE UNIQUE INDEX termset_keywords ON term_sets(is_keywords) WHERE is_keywords = 1;

CREATE TABLE terms (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    term_set_id TEXT NOT NULL REFERENCES term_sets(id) ON DELETE CASCADE,
    parent_id TEXT REFERENCES terms(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    labels TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(labels) AND json_type(labels) = 'object'),
    synonyms TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(synonyms) AND json_type(synonyms) = 'array'),
    description TEXT NOT NULL DEFAULT '',
    color TEXT CHECK (color IS NULL OR (length(color) = 7 AND glob('#[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]', color))),
    sort_order INTEGER NOT NULL DEFAULT 0,
    path TEXT NOT NULL CHECK (path LIKE '/%/'),
    merged_into_id TEXT REFERENCES terms(id) ON DELETE SET NULL,
    available_as_keyword BOOLEAN NOT NULL DEFAULT FALSE CHECK (available_as_keyword IN (0, 1)),
    deprecated BOOLEAN NOT NULL DEFAULT FALSE CHECK (deprecated IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    CHECK (parent_id IS NULL OR parent_id <> id),
    CHECK (merged_into_id IS NULL OR deprecated = 1),
    CHECK (merged_into_id IS NULL OR merged_into_id <> id)
);
CREATE UNIQUE INDEX term_active_sibling_name ON terms(term_set_id, coalesce(parent_id, ''), normalized_name) WHERE merged_into_id IS NULL;
CREATE INDEX term_term_set_id_parent_id_sort ON terms(term_set_id, parent_id, sort_order, normalized_name, id);
CREATE INDEX term_term_set_id_id ON terms(term_set_id, id);
CREATE INDEX term_parent_id ON terms(parent_id);
CREATE INDEX term_path ON terms(path);
CREATE INDEX term_merged_into_id ON terms(merged_into_id) WHERE merged_into_id IS NOT NULL;
CREATE INDEX term_available_as_keyword ON terms(term_set_id) WHERE available_as_keyword = 1;

CREATE TABLE relationship_types (
    id TEXT PRIMARY KEY NOT NULL REFERENCES terms(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    inverse_label TEXT NOT NULL DEFAULT '',
    directed BOOLEAN NOT NULL DEFAULT FALSE CHECK (directed IN (0, 1)),
    max_incoming INTEGER CHECK (max_incoming BETWEEN 1 AND 1000),
    max_outgoing INTEGER CHECK (max_outgoing BETWEEN 1 AND 1000),
    attributes TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(attributes) AND json_type(attributes) = 'object'),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    CHECK (directed = 1 OR (inverse_label = '' AND max_incoming IS NULL AND max_outgoing IS NULL))
);

-- =============================================================================
-- 3. Schema Definitions & Content Types
-- =============================================================================

CREATE TABLE field_definitions (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "key" TEXT NOT NULL,
    label TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('text','note','email','url','date','lookup','person','term','keywords','choice','datetime','integer','decimal','number','currency','boolean')),
    required BOOLEAN NOT NULL DEFAULT FALSE CHECK (required IN (0, 1)),
    choices TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(choices) AND json_type(choices) = 'array'),
    indexed BOOLEAN NOT NULL DEFAULT FALSE CHECK (indexed IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    index_status TEXT NOT NULL DEFAULT 'ready' CHECK (index_status IN ('ready', 'building', 'failed')),
    scale INTEGER NOT NULL DEFAULT 0 CHECK (scale BETWEEN 0 AND 9 AND (type = 'decimal' OR scale = 0)),
    options TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(options) AND json_type(options) = 'object')
);
CREATE INDEX fielddefinition_key ON field_definitions("key");

CREATE TABLE content_types (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "key" TEXT NOT NULL,
    name TEXT NOT NULL,
    template_key TEXT,
    field_ids TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(field_ids) AND json_type(field_ids) = 'array'),
    rules TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(rules) AND json_type(rules) = 'array'),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0)
);
CREATE INDEX contenttype_key ON content_types("key");
CREATE UNIQUE INDEX contenttype_template_key ON content_types(template_key);

-- =============================================================================
-- 4. Resources, Blobs, Revisions & Surfaces
-- =============================================================================

CREATE TABLE resources (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workspace_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    personal_owner_id TEXT REFERENCES identity_users(id) ON DELETE SET NULL CHECK (personal_owner_id IS NULL OR kind = 'workspace'),
    system_key TEXT CHECK (system_key IS NULL OR (kind = 'library' AND system_key IN ('home.documents', 'home.inbox'))),
    kind TEXT NOT NULL CHECK (kind IN ('workspace', 'list', 'library', 'folder', 'item')),
    name TEXT NOT NULL,
    default_currency TEXT NOT NULL DEFAULT '' CHECK (default_currency = '' OR (kind = 'workspace' AND length(default_currency) = 3 AND glob('*[^A-Z]*', default_currency) = 0)),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 2000 AND (kind IN ('workspace', 'list', 'library') OR description = '')),
    allow_folders BOOLEAN NOT NULL DEFAULT TRUE CHECK (allow_folders IN (0, 1) AND (kind IN ('list', 'library') OR allow_folders = 1)),
    keywords TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(keywords) AND json_type(keywords) = 'array'),
    "values" TEXT NOT NULL DEFAULT '{}' CHECK (json_valid("values") AND json_type("values") = 'object'),
    inherit_permissions BOOLEAN NOT NULL DEFAULT TRUE CHECK (inherit_permissions IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    scope_id TEXT REFERENCES resources(id) ON DELETE SET NULL,
    parent_id TEXT REFERENCES resources(id) ON DELETE SET NULL,
    container_id TEXT REFERENCES resources(id) ON DELETE SET NULL,
    head_revision_id TEXT REFERENCES item_revisions(id) ON DELETE SET NULL,
    published_revision_id TEXT REFERENCES item_revisions(id) ON DELETE SET NULL,
    schema_head_id TEXT REFERENCES schema_revisions(id) ON DELETE SET NULL,
    next_revision_number INTEGER NOT NULL DEFAULT 1 CHECK (next_revision_number >= 1),
    publishing_enabled BOOLEAN NOT NULL DEFAULT FALSE CHECK (publishing_enabled IN (0, 1) AND (publishing_enabled = 0 OR kind IN ('list', 'library'))),
    versioning TEXT NOT NULL DEFAULT 'off' CHECK (versioning IN ('off', 'major') AND (kind IN ('list', 'library') OR versioning = 'off')),
    max_versions INTEGER NOT NULL DEFAULT 50 CHECK (max_versions BETWEEN 1 AND 500 AND (kind IN ('list', 'library') OR max_versions = 50)),
    webdav_enabled BOOLEAN NOT NULL DEFAULT FALSE CHECK (webdav_enabled IN (0, 1) AND (webdav_enabled = 0 OR kind = 'library')),
    duplicate_policy TEXT NOT NULL DEFAULT 'warn' CHECK (duplicate_policy IN ('allow', 'warn', 'block') AND (kind = 'library' OR duplicate_policy = 'warn')),
    name_key TEXT,
    deleted_at DATETIME,
    deleted_by TEXT,
    archived_at DATETIME,
    archived_by TEXT,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    content_type_id TEXT REFERENCES content_types(id) ON DELETE RESTRICT,
    template_keys TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(template_keys) AND json_type(template_keys) = 'array' AND json_array_length(template_keys) <= 100 AND (kind IN ('list', 'library') OR json_array_length(template_keys) = 0)),
    CHECK ((archived_at IS NULL) = (archived_by IS NULL)),
    CHECK (archived_at IS NULL OR kind = 'item'),
    CHECK (deleted_at IS NULL OR name_key IS NULL),
    CHECK ((kind IN ('item', 'folder')) = (content_type_id IS NOT NULL)),
    CHECK (kind <> 'workspace' OR (workspace_id = id AND parent_id IS NULL AND container_id IS NULL AND inherit_permissions = 0))
);
CREATE INDEX resource_archived_at_id ON resources(archived_at DESC, id DESC) WHERE archived_at IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX resource_container_id_archived_at_id ON resources(container_id, archived_at, id);
CREATE INDEX resource_workspace_id_kind_id ON resources(workspace_id, kind, id);
CREATE INDEX resource_workspace_id_id_scope_id ON resources(workspace_id, id, scope_id);
CREATE INDEX resource_kind_id_workspace_id_scope_id ON resources(kind, id, workspace_id, scope_id);
CREATE INDEX resource_scope_id_id ON resources(scope_id, id);
CREATE INDEX resource_parent_id_id_scope_id ON resources(parent_id, id, scope_id);
CREATE INDEX resource_parent_id_kind_id_scope_id ON resources(parent_id, kind, id, scope_id);
CREATE INDEX resource_container_id_id ON resources(container_id, id);
CREATE INDEX resource_container_id_content_type_id_id ON resources(container_id, content_type_id, id);
CREATE INDEX resource_container_id_scope_id ON resources(container_id, scope_id);
CREATE UNIQUE INDEX resource_parent_id_name_key ON resources(parent_id, name_key) WHERE name_key IS NOT NULL;
CREATE INDEX resource_head_revision_id ON resources(head_revision_id) WHERE head_revision_id IS NOT NULL;
CREATE INDEX resource_published_revision_id ON resources(published_revision_id) WHERE published_revision_id IS NOT NULL;
CREATE INDEX resource_schema_head_id ON resources(schema_head_id) WHERE schema_head_id IS NOT NULL;
CREATE INDEX resource_content_type_id ON resources(content_type_id) WHERE content_type_id IS NOT NULL;
CREATE INDEX resource_container_id_deleted_at_id ON resources(container_id, deleted_at, id) WHERE deleted_at IS NOT NULL;
CREATE UNIQUE INDEX resource_personal_owner_id ON resources(personal_owner_id) WHERE personal_owner_id IS NOT NULL;
CREATE UNIQUE INDEX resource_workspace_id_system_key ON resources(workspace_id, system_key) WHERE system_key IS NOT NULL;

CREATE TABLE resource_keywords (
    resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    keyword TEXT NOT NULL REFERENCES terms(id) ON DELETE CASCADE,
    PRIMARY KEY (keyword, resource_id)
) WITHOUT ROWID;
CREATE INDEX resource_keywords_by_resource ON resource_keywords(resource_id, keyword);

CREATE TABLE collection_types (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    container_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    content_type_id TEXT NOT NULL REFERENCES content_types(id) ON DELETE RESTRICT,
    is_default BOOLEAN NOT NULL DEFAULT FALSE CHECK (is_default IN (0, 1))
);
CREATE UNIQUE INDEX collectiontype_container_id_content_type_id ON collection_types(container_id, content_type_id);
CREATE INDEX collectiontype_content_type_id_container_id ON collection_types(content_type_id, container_id);
CREATE UNIQUE INDEX collectiontype_container_id ON collection_types(container_id) WHERE is_default = 1;

CREATE TABLE schema_revisions (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    container_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    revision_number INTEGER NOT NULL CHECK (revision_number > 0),
    definition TEXT NOT NULL CHECK (json_valid(definition) AND json_type(definition) = 'object'),
    created_by TEXT NOT NULL
);
CREATE UNIQUE INDEX schemarevision_container_id_revision_number ON schema_revisions(container_id, revision_number);
CREATE INDEX schemarevision_container_id_id ON schema_revisions(container_id, id);

CREATE TABLE blobs (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    version INTEGER NOT NULL CHECK (version > 0),
    object_key TEXT NOT NULL,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL CHECK (length(content_type) BETWEEN 3 AND 127 AND content_type LIKE '%/%'),
    languages TEXT NOT NULL DEFAULT '' CHECK (length(languages) <= 191),
    size INTEGER NOT NULL CHECK (size >= 0),
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
    item_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    availability TEXT NOT NULL DEFAULT 'available' CHECK (availability IN ('available', 'pending', 'reclaimed')),
    reclaim_after DATETIME
);
CREATE INDEX blob_sha256_item_id ON blobs(sha256, item_id);
CREATE INDEX blob_object_key ON blobs(object_key);
CREATE INDEX blob_reclaim_after ON blobs(reclaim_after) WHERE availability = 'pending';
CREATE UNIQUE INDEX blob_item_id_version ON blobs(item_id, version);

CREATE TABLE blob_deletions (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    object_key TEXT NOT NULL UNIQUE
);

CREATE TABLE blob_previews (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
    status TEXT NOT NULL CHECK (status IN ('ready', 'failed')),
    object_key TEXT NOT NULL DEFAULT '',
    object_sha256 TEXT NOT NULL DEFAULT '',
    object_size INTEGER NOT NULL DEFAULT 0,
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    CHECK (
        (status = 'ready' AND length(object_key) > 0 AND length(object_sha256) = 64 AND object_size > 0 AND object_size < 100000 AND width BETWEEN 1 AND 800 AND height BETWEEN 1 AND 800)
        OR (status = 'failed' AND object_key = '' AND object_sha256 = '' AND object_size = 0 AND width = 0 AND height = 0)
    )
);
CREATE UNIQUE INDEX blobpreview_sha256 ON blob_previews(sha256);
CREATE INDEX blobpreview_object_key ON blob_previews(object_key);

CREATE TABLE item_revisions (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    item_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    container_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    schema_revision_id TEXT NOT NULL REFERENCES schema_revisions(id) ON DELETE RESTRICT,
    content_type_id TEXT NOT NULL REFERENCES content_types(id) ON DELETE RESTRICT,
    revision_number INTEGER NOT NULL CHECK (revision_number > 0),
    name TEXT NOT NULL,
    keywords TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(keywords) AND json_type(keywords) = 'array'),
    payload TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload) AND json_type(payload) = 'object'),
    created_by TEXT NOT NULL,
    blob_id TEXT REFERENCES blobs(id) ON DELETE SET NULL,
    filename TEXT NOT NULL DEFAULT '' CHECK ((blob_id IS NULL) = (filename = ''))
);
CREATE UNIQUE INDEX itemrevision_item_id_revision_number ON item_revisions(item_id, revision_number);
CREATE INDEX itemrevision_item_id_id ON item_revisions(item_id, id);
CREATE INDEX itemrevision_container_id ON item_revisions(container_id);
CREATE INDEX itemrevision_schema_revision_id ON item_revisions(schema_revision_id);
CREATE INDEX itemrevision_content_type_id ON item_revisions(content_type_id);
CREATE INDEX itemrevision_blob_id ON item_revisions(blob_id) WHERE blob_id IS NOT NULL;

CREATE TABLE item_histories (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revision_id TEXT NOT NULL REFERENCES item_revisions(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    number INTEGER NOT NULL CHECK (number > 0),
    changed_fields TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(changed_fields) AND json_type(changed_fields) = 'array')
);
CREATE UNIQUE INDEX itemhistory_revision_id ON item_histories(revision_id);
CREATE UNIQUE INDEX itemhistory_item_id_number ON item_histories(item_id, number);

CREATE TABLE publications (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    version INTEGER NOT NULL CHECK (version > 0),
    published_by TEXT NOT NULL,
    snapshot TEXT NOT NULL CHECK (json_valid(snapshot) AND json_type(snapshot) = 'object'),
    item_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    action TEXT NOT NULL DEFAULT 'publish' CHECK (action IN ('publish', 'unpublish')),
    revision_id TEXT REFERENCES item_revisions(id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX publication_item_id_version_action ON publications(item_id, version, action);
CREATE INDEX publication_revision_id ON publications(revision_id) WHERE revision_id IS NOT NULL;

CREATE TABLE item_surfaces (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    item_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    container_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    surface TEXT NOT NULL CHECK (surface IN ('head', 'published')),
    revision_id TEXT NOT NULL REFERENCES item_revisions(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    folded_name TEXT NOT NULL,
    keywords TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(keywords) AND json_type(keywords) = 'array'),
    payload TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload) AND json_type(payload) = 'object'),
    item_created_at DATETIME NOT NULL,
    item_created_by TEXT NOT NULL,
    modified_at DATETIME NOT NULL,
    modified_by TEXT NOT NULL
);
CREATE UNIQUE INDEX itemsurface_item_id_surface ON item_surfaces(item_id, surface);
CREATE INDEX itemsurface_workspace_id_surface_item_id ON item_surfaces(workspace_id, surface, item_id);
CREATE INDEX itemsurface_container_id_surface_item_id ON item_surfaces(container_id, surface, item_id);
CREATE INDEX itemsurface_folded_name_item_id_surface ON item_surfaces(folded_name, item_id, surface);
CREATE INDEX itemsurface_revision_id ON item_surfaces(revision_id);
CREATE INDEX itemsurface_container_id_surface_modified_at_item_id ON item_surfaces(container_id, surface, modified_at, item_id);
CREATE INDEX itemsurface_container_id_surface_modified_by_item_id ON item_surfaces(container_id, surface, modified_by, item_id);
CREATE INDEX itemsurface_container_id_surface_item_created_at_item_id ON item_surfaces(container_id, surface, item_created_at, item_id);
CREATE INDEX itemsurface_container_id_surface_item_created_by_item_id ON item_surfaces(container_id, surface, item_created_by, item_id);
CREATE INDEX itemsurface_container_id_surface_name_item_id ON item_surfaces(container_id, surface, name, item_id);

CREATE TABLE item_surface_keywords (
    surface_id TEXT NOT NULL REFERENCES item_surfaces(id) ON DELETE CASCADE,
    keyword TEXT NOT NULL REFERENCES terms(id) ON DELETE CASCADE,
    PRIMARY KEY (keyword, surface_id)
) WITHOUT ROWID;
CREATE INDEX item_surface_keywords_by_surface ON item_surface_keywords(surface_id, keyword);

CREATE TABLE field_values (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    surface_id TEXT NOT NULL REFERENCES item_surfaces(id) ON DELETE CASCADE,
    container_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    surface TEXT NOT NULL CHECK (surface IN ('head', 'published')),
    field_key TEXT NOT NULL,
    field_type TEXT NOT NULL CHECK (field_type IN ('text','note','email','url','date','lookup','person','term','keywords','choice','datetime','integer','decimal','number','currency','boolean')),
    scale INTEGER NOT NULL DEFAULT 0 CHECK (scale BETWEEN 0 AND 9 AND (field_type = 'decimal' OR scale = 0)),
    ordinal INTEGER NOT NULL DEFAULT 0 CHECK (ordinal >= 0),
    value_text TEXT,
    value_integer INTEGER,
    value_numeric NUMERIC,
    value_boolean BOOLEAN,
    -- applies_parents copies the immutable apply_parents option of a term
    -- or keywords field: its value also applies the term's ancestors.
    applies_parents BOOLEAN NOT NULL DEFAULT FALSE CHECK (applies_parents IN (0, 1)),
    CHECK (field_type IN ('term', 'keywords') OR NOT applies_parents),
    CHECK (
        (CASE WHEN value_text IS NOT NULL THEN 1 ELSE 0 END) +
        (CASE WHEN value_integer IS NOT NULL THEN 1 ELSE 0 END) +
        (CASE WHEN value_numeric IS NOT NULL THEN 1 ELSE 0 END) +
        (CASE WHEN value_boolean IS NOT NULL THEN 1 ELSE 0 END) = 1
    ),
    CHECK (
        (field_type IN ('text','note','email','url','date','lookup','person','term','keywords','choice','datetime') AND value_text IS NOT NULL)
        OR (field_type IN ('integer','decimal') AND value_integer IS NOT NULL AND typeof(value_integer) = 'integer')
        OR (field_type IN ('number','currency') AND (
            value_numeric IS NOT NULL
            OR (value_text IS NOT NULL AND length(value_text) = 58 AND substr(value_text, 1, 1) IN ('0', '1') AND glob('*[^0-9]*', value_text) = 0)
        ))
        OR (field_type = 'boolean' AND value_boolean IS NOT NULL AND value_boolean IN (0, 1))
    )
);
CREATE INDEX fieldvalue_field_type_value_text_item_id ON field_values(field_type, value_text, item_id);
CREATE UNIQUE INDEX fieldvalue_surface_id_field_key_ordinal ON field_values(surface_id, field_key, ordinal);
CREATE INDEX fieldvalue_container_id_surface_field_key_value_integer_item_id ON field_values(container_id, surface, field_key, value_integer, item_id);
CREATE INDEX fieldvalue_container_id_surface_field_key_value_numeric_item_id ON field_values(container_id, surface, field_key, value_numeric, item_id);
CREATE INDEX fieldvalue_container_id_surface_field_key_value_text_item_id ON field_values(container_id, surface, field_key, value_text, item_id);
CREATE INDEX fieldvalue_container_id_surface_field_key_value_boolean_item_id ON field_values(container_id, surface, field_key, value_boolean, item_id);

CREATE TABLE group_counts (
    container_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    surface TEXT NOT NULL CHECK (surface IN ('head', 'published')),
    field_key TEXT NOT NULL,
    "value" TEXT NOT NULL,
    count INTEGER NOT NULL CHECK (count >= 0),
    PRIMARY KEY (container_id, surface, field_key, "value")
) WITHOUT ROWID;

CREATE TABLE business_keys (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    container_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    field_key TEXT NOT NULL,
    "value" TEXT NOT NULL
);
CREATE UNIQUE INDEX businesskey_container_id_field_key_value ON business_keys(container_id, field_key, "value");
CREATE INDEX businesskey_item_id ON business_keys(item_id);

-- =============================================================================
-- 5. Permissions, Relationships, Views & Configurations
-- =============================================================================

CREATE TABLE grants (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    principal_type TEXT NOT NULL CHECK (principal_type IN ('user', 'group', 'workspace_visitor', 'workspace_member', 'workspace_owner')),
    principal_id TEXT NOT NULL CHECK (length(trim(principal_id)) > 0),
    action TEXT NOT NULL CHECK (action IN ('read', 'read_draft', 'write', 'publish', 'manage')),
    effect TEXT NOT NULL DEFAULT 'allow' CHECK (effect = 'allow'),
    resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    CHECK (principal_type <> 'workspace_owner' OR action = 'manage')
);
CREATE UNIQUE INDEX grant_resource_id_principal_type_principal_id_action ON grants(resource_id, principal_type, principal_id, action);
CREATE INDEX grant_principal_type_principal_id_resource_id ON grants(principal_type, principal_id, resource_id);

CREATE TABLE relationships (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    metadata TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata) AND json_type(metadata) = 'object'),
    source_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    type_id TEXT REFERENCES relationship_types(id) ON DELETE RESTRICT,
    directed BOOLEAN NOT NULL DEFAULT FALSE CHECK (directed IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    CHECK (source_id <> target_id),
    CHECK (directed = 1 OR source_id < target_id)
);
CREATE INDEX relationship_source_id_id ON relationships(source_id, id);
CREATE INDEX relationship_source_id_type_id_id ON relationships(source_id, type_id, id);
CREATE INDEX relationship_target_id_id ON relationships(target_id, id);
CREATE INDEX relationship_type_id_id ON relationships(type_id, id);
CREATE INDEX relationship_target_id_type_id_id ON relationships(target_id, type_id, id);
CREATE UNIQUE INDEX relationship_type_id_source_id_target_id_directed ON relationships(type_id, source_id, target_id, directed);
CREATE UNIQUE INDEX relationship_source_id_target_id_directed ON relationships(source_id, target_id, directed) WHERE type_id IS NULL;
CREATE INDEX relationship_type_id_target_id ON relationships(type_id, target_id);

CREATE TABLE schema_templates (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workspace_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    "key" TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    definition TEXT NOT NULL CHECK (json_valid(definition) AND json_type(definition) = 'object'),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0)
);
CREATE UNIQUE INDEX schematemplate_workspace_id_key ON schema_templates(workspace_id, "key");

CREATE TABLE list_views (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    container_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    system_key TEXT NOT NULL DEFAULT '' CHECK (system_key IN ('', 'archived')),
    columns TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(columns) AND json_type(columns) = 'array'),
    "query" TEXT NOT NULL DEFAULT '{}' CHECK (json_valid("query") AND json_type("query") = 'object'),
    layout TEXT NOT NULL DEFAULT 'table' CHECK (layout IN ('table', 'board', 'calendar', 'gallery', 'navigation')),
    -- settings configures the layout, such as the field a navigation view browses.
    settings TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(settings) AND json_type(settings) = 'object'),
    is_default BOOLEAN NOT NULL DEFAULT FALSE CHECK (is_default IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0)
);
CREATE UNIQUE INDEX listview_container_id_system_key ON list_views(container_id, system_key) WHERE system_key <> '';
CREATE UNIQUE INDEX listview_container_id_name ON list_views(container_id, name);
CREATE INDEX listview_container_id_id ON list_views(container_id, id);
CREATE UNIQUE INDEX listview_one_default ON list_views(container_id) WHERE is_default = 1;

CREATE TABLE smart_folders (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workspace_id TEXT REFERENCES resources(id) ON DELETE CASCADE,
    owner_id TEXT REFERENCES identity_users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    definition TEXT NOT NULL CHECK (json_valid(definition) AND json_type(definition) = 'object' AND coalesce(json_extract(definition, '$.mode'), '') IN ('query', 'classification')),
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    CHECK (workspace_id IS NOT NULL OR owner_id IS NOT NULL),
    CHECK (owner_id IS NULL OR length(owner_id) > 0)
);
CREATE INDEX smartfolder_owner_id_id ON smart_folders(owner_id, id);
CREATE INDEX smartfolder_workspace_id_id ON smart_folders(workspace_id, id);
CREATE UNIQUE INDEX smartfolder_workspace_id_name ON smart_folders(workspace_id, name) WHERE owner_id IS NULL;
CREATE UNIQUE INDEX smartfolder_owner_id_workspace_id_name ON smart_folders(owner_id, workspace_id, name);
CREATE UNIQUE INDEX smartfolder_owner_id_name ON smart_folders(owner_id, name) WHERE workspace_id IS NULL;

CREATE TABLE group_inboxes (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    group_id TEXT NOT NULL REFERENCES identity_groups(id) ON DELETE CASCADE,
    library_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX groupinbox_group_id ON group_inboxes(group_id);
CREATE INDEX groupinbox_library_id ON group_inboxes(library_id);

-- =============================================================================
-- 6. Integrations, Sync, Operations, Workflows & Tasks
-- =============================================================================

CREATE TABLE list_integrations (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    container_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (length(kind) BETWEEN 1 AND 64),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    paused BOOLEAN NOT NULL DEFAULT FALSE CHECK (paused IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    generation TEXT NOT NULL,
    config_ciphertext BLOB NOT NULL,
    config_hash TEXT NOT NULL,
    configured_by TEXT NOT NULL,
    credential_id TEXT NOT NULL DEFAULT '',
    credential_scopes TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(credential_scopes) AND json_type(credential_scopes) = 'array'),
    state TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(state) AND json_type(state) = 'object'),
    last_attempt_at DATETIME,
    last_success_at DATETIME,
    error TEXT NOT NULL DEFAULT '',
    item_count INTEGER NOT NULL DEFAULT 0 CHECK (item_count >= 0),
    lease_token TEXT NOT NULL DEFAULT '',
    lease_until DATETIME,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX listintegration_container_id_kind_config_hash ON list_integrations(container_id, kind, config_hash);

CREATE TABLE integration_items (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    item_id TEXT NOT NULL UNIQUE REFERENCES resources(id) ON DELETE CASCADE,
    integration_id TEXT REFERENCES list_integrations(id) ON DELETE SET NULL,
    external_id TEXT NOT NULL CHECK (length(external_id) BETWEEN 1 AND 4096),
    provider_kind TEXT NOT NULL CHECK (length(provider_kind) BETWEEN 1 AND 64),
    managed_fields TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(managed_fields) AND json_type(managed_fields) = 'array')
);
CREATE UNIQUE INDEX integrationitem_integration_id_external_id ON integration_items(integration_id, external_id);

CREATE TABLE integration_revision_data (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revision_id TEXT NOT NULL UNIQUE REFERENCES item_revisions(id) ON DELETE CASCADE,
    provider_kind TEXT NOT NULL CHECK (length(provider_kind) BETWEEN 1 AND 64),
    data TEXT NOT NULL CHECK (json_valid(data) AND json_type(data) = 'object')
);

CREATE TABLE operations (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    container_id TEXT REFERENCES resources(id) ON DELETE SET NULL,
    field_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL CHECK (length(kind) BETWEEN 1 AND 64),
    trace_id TEXT NOT NULL,
    credential_id TEXT,
    credential_scopes TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(credential_scopes) AND json_type(credential_scopes) = 'array'),
    result TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(result)),
    started_at DATETIME,
    completed_at DATETIME,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed', 'superseded', 'cancelled')),
    surface TEXT NOT NULL DEFAULT 'head' CHECK (surface IN ('head', 'published')),
    after_item_id TEXT NOT NULL DEFAULT '',
    processed INTEGER NOT NULL DEFAULT 0 CHECK (processed >= 0),
    error TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL,
    payload TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload) AND json_type(payload) = 'object'),
    prepared BOOLEAN NOT NULL DEFAULT FALSE CHECK (prepared IN (0, 1)),
    matched INTEGER NOT NULL DEFAULT 0 CHECK (matched >= 0),
    updated INTEGER NOT NULL DEFAULT 0 CHECK (updated >= 0),
    failed INTEGER NOT NULL DEFAULT 0 CHECK (failed >= 0),
    progress INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    failures TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(failures) AND json_type(failures) = 'array'),
    workspace_id TEXT REFERENCES resources(id) ON DELETE CASCADE,
    attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    claim_holder TEXT,
    claim_token TEXT,
    claim_until DATETIME,
    parent_id TEXT REFERENCES operations(id) ON DELETE SET NULL,
    execution_id TEXT NOT NULL DEFAULT '',
    group_key TEXT NOT NULL DEFAULT '' CHECK (length(group_key) <= 128),
    input_key TEXT NOT NULL DEFAULT '' CHECK (length(input_key) <= 128),
    deadline DATETIME,
    idempotency_key TEXT NOT NULL DEFAULT '' CHECK (length(idempotency_key) <= 128),
    payload_digest TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '' CHECK (error_code IN ('', 'deadline_exceeded', 'retries_exhausted', 'forbidden')),
    CHECK ((claim_token IS NULL) = (claim_until IS NULL) AND (claim_token IS NULL) = (claim_holder IS NULL))
);
CREATE INDEX operation_status_created_at ON operations(status, created_at);
CREATE INDEX operation_status_updated_at ON operations(status, updated_at);
CREATE INDEX operation_container_id_id ON operations(container_id, id);
CREATE INDEX operation_parent_id ON operations(parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX operation_ready ON operations(kind, workspace_id, attempt_at) WHERE status = 'pending';
CREATE INDEX operation_group ON operations(kind, group_key, created_at) WHERE status = 'pending';
CREATE INDEX operation_pending_workspace ON operations(workspace_id) WHERE status = 'pending';
CREATE INDEX operation_pending_creator ON operations(created_by) WHERE status = 'pending';
CREATE INDEX operation_execution_id ON operations(execution_id) WHERE execution_id <> '';
CREATE UNIQUE INDEX operation_idempotency ON operations(created_by, kind, COALESCE(container_id, ''), idempotency_key) WHERE idempotency_key <> '';

CREATE TABLE operation_items (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    operation_id TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX operationitem_operation_id_item_id ON operation_items(operation_id, item_id);

CREATE TABLE workflows (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workspace_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    "key" TEXT NOT NULL CHECK (length("key") BETWEEN 1 AND 100),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    automatic_enabled BOOLEAN NOT NULL DEFAULT FALSE CHECK (automatic_enabled IN (0, 1)),
    manual_enabled BOOLEAN NOT NULL DEFAULT TRUE CHECK (manual_enabled IN (0, 1)),
    automatic_provider BOOLEAN NOT NULL DEFAULT FALSE CHECK (automatic_provider IN (0, 1)),
    state TEXT NOT NULL DEFAULT 'published' CHECK (state IN ('draft', 'published', 'archived')),
    builtin_key TEXT,
    collection_id TEXT REFERENCES resources(id) ON DELETE SET NULL,
    parameters TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(parameters) AND json_type(parameters) = 'object'),
    current_version INTEGER NOT NULL CHECK (current_version > 0),
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    deleted_at DATETIME,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    role TEXT CHECK (role IS NULL OR length(role) BETWEEN 1 AND 100),
    CHECK (collection_id IS NULL OR builtin_key IS NOT NULL OR role IS NOT NULL)
);
CREATE UNIQUE INDEX workflow_workspace_id_key ON workflows(workspace_id, "key") WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX workflow_workspace_id_name ON workflows(workspace_id, name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX workflow_workspace_id_builtin_key_collection_id ON workflows(workspace_id, builtin_key, coalesce(collection_id, '')) WHERE builtin_key IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX workflow_workspace_id_id ON workflows(workspace_id, id);
CREATE INDEX workflow_workspace_id_role_collection_id ON workflows(workspace_id, role, collection_id) WHERE role IS NOT NULL AND deleted_at IS NULL;

CREATE TABLE workflow_versions (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    number INTEGER NOT NULL CHECK (number > 0),
    definition TEXT NOT NULL CHECK (json_valid(definition) AND json_type(definition) = 'object'),
    source TEXT NOT NULL CHECK (length(source) BETWEEN 1 AND 1048576),
    bundle TEXT NOT NULL CHECK (length(bundle) >= 1),
    bundle_hash TEXT NOT NULL CHECK (length(bundle_hash) = 64),
    created_by TEXT NOT NULL
);
CREATE UNIQUE INDEX workflowversion_workflow_id_number ON workflow_versions(workflow_id, number);

CREATE TABLE workflow_triggers (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    collection_id TEXT REFERENCES resources(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    next_at DATETIME,
    CHECK ((type = 'schedule') = (next_at IS NOT NULL))
);
CREATE INDEX workflowtrigger_type_workspace_id_collection_id ON workflow_triggers(type, workspace_id, collection_id);
CREATE INDEX workflowtrigger_workflow_id ON workflow_triggers(workflow_id);
CREATE INDEX workflowtrigger_next_at ON workflow_triggers(next_at) WHERE next_at IS NOT NULL;

-- A run is the provenance of a script.run operation, which holds its
-- execution state. Run item and retry identifiers are provenance; current
-- references are validated by DAL.
CREATE TABLE workflow_runs (
    id TEXT PRIMARY KEY NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    workflow_version INTEGER NOT NULL CHECK (workflow_version > 0),
    workspace_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    item_id TEXT,
    event_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    depth INTEGER NOT NULL CHECK (depth >= 0),
    actor TEXT NOT NULL,
    retry_of TEXT,
    logs TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(logs) AND json_type(logs) = 'array')
);
CREATE UNIQUE INDEX workflowrun_workflow_id_event_id ON workflow_runs(workflow_id, event_id);
CREATE INDEX workflowrun_workspace_id_created_at_id ON workflow_runs(workspace_id, created_at, id);
CREATE INDEX workflowrun_workflow_id_created_at_id ON workflow_runs(workflow_id, created_at, id);
CREATE INDEX workflowrun_item_id_created_at_id ON workflow_runs(item_id, created_at, id);
CREATE INDEX workflowrun_created_at ON workflow_runs(created_at);

CREATE TABLE workflow_run_items (
    id TEXT PRIMARY KEY NOT NULL,
    run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0)
);
CREATE UNIQUE INDEX workflowrunitem_run_id_position ON workflow_run_items(run_id, position);
CREATE UNIQUE INDEX workflowrunitem_run_id_item_id ON workflow_run_items(run_id, item_id);
CREATE INDEX workflowrunitem_item_id ON workflow_run_items(item_id);

-- Event origins are immutable identifiers, retained after content and run cleanup.
CREATE TABLE domain_events (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    type TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    collection_id TEXT,
    resource_id TEXT,
    actor TEXT NOT NULL,
    data TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(data) AND json_type(data) = 'object'),
    depth INTEGER NOT NULL CHECK (depth >= 0),
    cause_run_id TEXT,
    dispatched_at DATETIME,
    fanout_cursor TEXT
);
CREATE INDEX domainevent_created_at_id ON domain_events(created_at, id) WHERE dispatched_at IS NULL;
CREATE INDEX domainevent_dispatched_at ON domain_events(dispatched_at) WHERE dispatched_at IS NOT NULL;

CREATE TABLE artifacts (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workspace_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    operation_id TEXT REFERENCES operations(id) ON DELETE SET NULL,
    input_key TEXT NOT NULL CHECK (length(input_key) = 64),
    sources TEXT NOT NULL CHECK (json_valid(sources) AND json_type(sources) = 'array'),
    object_key TEXT NOT NULL CHECK (object_key <> ''),
    filename TEXT NOT NULL CHECK (length(filename) BETWEEN 1 AND 255),
    content_type TEXT NOT NULL CHECK (length(content_type) BETWEEN 1 AND 255),
    size INTEGER NOT NULL CHECK (size >= 0),
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
    metrics TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metrics)),
    expires_at DATETIME NOT NULL,
    created_by TEXT NOT NULL
);
CREATE INDEX artifact_item_id_input_key ON artifacts(item_id, input_key);
CREATE INDEX artifact_expires_at ON artifacts(expires_at);
CREATE INDEX artifact_object_key ON artifacts(object_key);

CREATE TABLE staged_files (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workspace_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    artifact_id TEXT NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    run_id TEXT REFERENCES workflow_runs(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'promoted', 'discarded')),
    created_by TEXT NOT NULL
);
CREATE INDEX stagedfile_run_id ON staged_files(run_id);
CREATE INDEX stagedfile_item_id ON staged_files(item_id);
CREATE INDEX stagedfile_artifact_id_state ON staged_files(artifact_id, state);

CREATE TABLE tasks (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    workspace_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    item_id TEXT REFERENCES resources(id) ON DELETE SET NULL,
    kind TEXT NOT NULL DEFAULT 'task' CHECK (kind IN ('task', 'approval')),
    "key" TEXT NOT NULL DEFAULT '' CHECK (length("key") <= 64),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 500),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 4000),
    assignees TEXT NOT NULL CHECK (json_valid(assignees) AND json_type(assignees) = 'array'),
    completion TEXT NOT NULL DEFAULT 'any' CHECK (completion IN ('any', 'all')),
    outcomes TEXT NOT NULL CHECK (json_valid(outcomes) AND json_type(outcomes) = 'array' AND json_array_length(outcomes) > 0),
    input_schema TEXT CHECK (input_schema IS NULL OR json_valid(input_schema)),
    "values" TEXT CHECK ("values" IS NULL OR json_valid("values")),
    review TEXT CHECK (review IS NULL OR json_valid(review)),
    priority TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high')),
    due_at DATETIME,
    source TEXT NOT NULL CHECK (source IN ('workflow', 'user', 'system')),
    workflow_id TEXT REFERENCES workflows(id) ON DELETE SET NULL,
    run_id TEXT REFERENCES workflow_runs(id) ON DELETE SET NULL,
    operation_id TEXT REFERENCES operations(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'completed', 'cancelled', 'expired')),
    outcome TEXT,
    completed_by TEXT,
    completed_at DATETIME,
    "comment" TEXT NOT NULL DEFAULT '' CHECK (length("comment") <= 4000),
    "input" TEXT CHECK ("input" IS NULL OR json_valid("input")),
    created_by TEXT NOT NULL,
    CHECK (source = 'workflow' OR (workflow_id IS NULL AND run_id IS NULL AND operation_id IS NULL)),
    CHECK ((status = 'open') = (completed_at IS NULL))
);
CREATE INDEX task_run_id ON tasks(run_id);
CREATE INDEX task_operation_id ON tasks(operation_id);
CREATE INDEX task_workspace_id_status ON tasks(workspace_id, status);
CREATE INDEX task_item_id ON tasks(item_id);

CREATE TABLE task_responses (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    subject TEXT NOT NULL,
    assignee TEXT NOT NULL,
    outcome TEXT NOT NULL,
    "comment" TEXT NOT NULL DEFAULT '' CHECK (length("comment") <= 4000),
    "input" TEXT CHECK ("input" IS NULL OR json_valid("input"))
);
CREATE UNIQUE INDEX taskresponse_task_id_assignee ON task_responses(task_id, assignee);

-- A setting is a feature's value for the organization (no resource) or for
-- one resource; lower levels inherit what they leave unset.
CREATE TABLE settings (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resource_id TEXT REFERENCES resources(id) ON DELETE CASCADE,
    "key" TEXT NOT NULL CHECK ("key" IN ('comments')),
    "value" TEXT NOT NULL CHECK (json_valid("value") AND json_type("value") = 'object'),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_by TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX setting_resource_key ON settings(resource_id, "key") WHERE resource_id IS NOT NULL;
CREATE UNIQUE INDEX setting_organization_key ON settings("key") WHERE resource_id IS NULL;

-- A comment discusses an item. A thread is a root comment (thread_id = id)
-- and its replies, one level deep.
CREATE TABLE comments (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    item_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    thread_id TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    author TEXT NOT NULL CHECK (length(author) BETWEEN 1 AND 255),
    source TEXT NOT NULL CHECK (source IN ('user', 'workflow')),
    workflow_id TEXT REFERENCES workflows(id) ON DELETE SET NULL,
    body TEXT NOT NULL CHECK (length(body) <= 10000),
    edited_at DATETIME,
    deleted_at DATETIME,
    deleted_by TEXT,
    resolved_at DATETIME,
    resolved_by TEXT,
    CHECK ((deleted_at IS NULL) = (deleted_by IS NULL)),
    CHECK ((resolved_at IS NULL) = (resolved_by IS NULL)),
    CHECK (resolved_at IS NULL OR thread_id = id),
    CHECK (deleted_at IS NOT NULL OR length(body) >= 1)
);
CREATE INDEX comment_item_created ON comments(item_id, created_at, id);
CREATE INDEX comment_thread_created ON comments(thread_id, created_at, id);

-- The people a comment mentions, for their notifications and for telling
-- an edit's new mentions from those already told.
CREATE TABLE comment_mentions (
    comment_id TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    subject TEXT NOT NULL REFERENCES identity_users(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (comment_id, subject)
);
CREATE INDEX comment_mention_subject ON comment_mentions(subject);

-- A notification is one message to one person: their inbox. It never starts
-- a workflow and raises no domain event, so notifications cannot loop.
CREATE TABLE notifications (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    recipient TEXT NOT NULL REFERENCES identity_users(id) ON DELETE CASCADE,
    workspace_id TEXT REFERENCES resources(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('message', 'task.assigned', 'task.completed', 'workflow.failed', 'comment.mentioned', 'comment.replied')),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 300),
    body TEXT NOT NULL DEFAULT '' CHECK (length(body) <= 4000),
    -- The link opens one item, task or run; a purged item or task takes its
    -- notifications with it, a run past retention leaves them unlinked.
    item_id TEXT REFERENCES resources(id) ON DELETE CASCADE,
    task_id TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    run_id TEXT REFERENCES workflow_runs(id) ON DELETE SET NULL,
    -- A comment's notification also opens its thread on its item.
    comment_id TEXT REFERENCES comments(id) ON DELETE CASCADE,
    source TEXT NOT NULL CHECK (source IN ('workflow', 'user', 'system')),
    sender TEXT NOT NULL DEFAULT '',
    workflow_id TEXT REFERENCES workflows(id) ON DELETE SET NULL,
    dedup_key TEXT CHECK (dedup_key IS NULL OR length(dedup_key) BETWEEN 1 AND 200),
    -- An unread notification with a coalesce key takes the next one with
    -- that key instead of a new row, counting how many it stands for.
    coalesce_key TEXT CHECK (coalesce_key IS NULL OR length(coalesce_key) BETWEEN 1 AND 200),
    "count" INTEGER NOT NULL DEFAULT 1 CHECK ("count" >= 1),
    read_at DATETIME,
    CHECK ((item_id IS NULL OR task_id IS NULL) AND (run_id IS NULL OR (item_id IS NULL AND task_id IS NULL))),
    CHECK (comment_id IS NULL OR item_id IS NOT NULL)
);
CREATE INDEX notification_recipient_created ON notifications(recipient, created_at, id);
CREATE INDEX notification_recipient_unread ON notifications(recipient) WHERE read_at IS NULL;
CREATE UNIQUE INDEX notification_recipient_dedup ON notifications(recipient, dedup_key) WHERE dedup_key IS NOT NULL;
CREATE INDEX notification_item_id ON notifications(item_id);
CREATE INDEX notification_task_id ON notifications(task_id);
CREATE INDEX notification_run_id ON notifications(run_id);
CREATE INDEX notification_comment_id ON notifications(comment_id);
CREATE UNIQUE INDEX notification_recipient_coalesce ON notifications(recipient, coalesce_key) WHERE coalesce_key IS NOT NULL AND read_at IS NULL;

-- The comments a notification tells of: the one it names, or every reply
-- folded into it, so deleting a reply takes it out of each notification
-- that counts it.
CREATE TABLE notification_comments (
    notification_id TEXT NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    comment_id TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    PRIMARY KEY (notification_id, comment_id)
);
CREATE INDEX notification_comment_comment ON notification_comments(comment_id);

CREATE TABLE workflow_steps (
    run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL CHECK (seq >= 0),
    kind TEXT NOT NULL CHECK (kind IN ('request', 'wait', 'value')),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 300),
    input_hash TEXT NOT NULL CHECK (length(input_hash) = 64),
    status TEXT NOT NULL CHECK (status IN ('pending', 'completed', 'failed')),
    "input" TEXT CHECK ("input" IS NULL OR json_valid("input")),
    "output" BLOB,
    error TEXT,
    started_at DATETIME NOT NULL,
    completed_at DATETIME,
    PRIMARY KEY (run_id, seq),
    CHECK (status = 'pending' OR completed_at IS NOT NULL),
    CHECK (status <> 'pending' OR kind = 'wait')
);
CREATE INDEX workflow_steps_pending ON workflow_steps(status, started_at) WHERE status = 'pending';

CREATE TABLE workflow_step_operations (
    run_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    operation_id TEXT NOT NULL,
    PRIMARY KEY (run_id, seq, operation_id),
    FOREIGN KEY (run_id, seq) REFERENCES workflow_steps(run_id, seq) ON DELETE CASCADE
);
CREATE INDEX workflow_step_operations_operation ON workflow_step_operations(operation_id);

-- =============================================================================
-- 7. Durable Activity, Caching, Sync, Audit & Live Notifications
-- =============================================================================

CREATE TABLE cached_results (
    id TEXT PRIMARY KEY NOT NULL,
    namespace TEXT NOT NULL CHECK (length(namespace) BETWEEN 1 AND 64),
    scope TEXT NOT NULL CHECK (length(scope) BETWEEN 1 AND 255),
    "key" TEXT NOT NULL CHECK (length("key") BETWEEN 1 AND 4096),
    payload TEXT NOT NULL CHECK (json_valid(payload) AND octet_length(payload) <= 1048576),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    used_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL CHECK (expires_at > used_at)
);
CREATE UNIQUE INDEX cachedresult_namespace_scope_key ON cached_results(namespace, scope, "key");
CREATE INDEX cachedresult_expires_at ON cached_results(expires_at);

CREATE TABLE activity_records (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    namespace TEXT NOT NULL,
    scope TEXT NOT NULL,
    "key" TEXT NOT NULL,
    owner_id TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL CHECK (json_valid(payload) AND length(payload) <= 1048576),
    state TEXT NOT NULL DEFAULT 'pending',
    expires_at DATETIME,
    lease_owner TEXT,
    lease_token TEXT,
    lease_until DATETIME
);
CREATE UNIQUE INDEX activityrecord_namespace_scope_key ON activity_records(namespace, scope, "key");
CREATE INDEX activityrecord_namespace_scope_state_key ON activity_records(namespace, scope, state, "key");
CREATE INDEX activityrecord_expires_at ON activity_records(expires_at);

CREATE TABLE activity_references (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    owner_kind TEXT NOT NULL CHECK (owner_kind IN ('record', 'operation')),
    owner_id TEXT NOT NULL,
    record_id TEXT NOT NULL REFERENCES activity_records(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX activityreference_owner_record ON activity_references(owner_kind, owner_id, record_id);
CREATE INDEX activityreference_record_id ON activity_references(record_id);

CREATE TABLE blob_leases (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    operation_id TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
    blob_id TEXT NOT NULL REFERENCES blobs(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX bloblease_operation_id_blob_id ON blob_leases(operation_id, blob_id);
CREATE INDEX bloblease_blob_id ON blob_leases(blob_id);

CREATE TABLE sync_views (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    subject TEXT NOT NULL REFERENCES identity_users(id) ON DELETE CASCADE,
    container_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    "sequence" INTEGER NOT NULL DEFAULT 0 CHECK ("sequence" >= 0)
);
CREATE UNIQUE INDEX syncview_subject_container_id ON sync_views(subject, container_id);

CREATE TABLE sync_states (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    view_id TEXT NOT NULL REFERENCES sync_views(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    from_sequence INTEGER NOT NULL CHECK (from_sequence > 0),
    to_sequence INTEGER CHECK (to_sequence > from_sequence),
    fingerprint TEXT NOT NULL CHECK (length(fingerprint) = 64)
);
CREATE UNIQUE INDEX syncstate_view_id_item_id_from_sequence ON sync_states(view_id, item_id, from_sequence);
CREATE UNIQUE INDEX syncstate_view_id_item_id ON sync_states(view_id, item_id) WHERE to_sequence IS NULL;
CREATE INDEX syncstate_view_id_from_sequence_to_sequence ON sync_states(view_id, from_sequence, to_sequence);

CREATE TABLE sync_tokens (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    view_id TEXT NOT NULL REFERENCES sync_views(id) ON DELETE CASCADE,
    "sequence" INTEGER NOT NULL CHECK ("sequence" > 0),
    changes TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(changes) AND json_type(changes) = 'array'),
    page_expires_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL
);
CREATE INDEX synctoken_view_id_sequence ON sync_tokens(view_id, "sequence");
CREATE INDEX synctoken_expires_at ON sync_tokens(expires_at);
CREATE INDEX synctoken_page_expires_at ON sync_tokens(page_expires_at);

-- Audit scope and target identifiers survive the records whose deletion they describe.
CREATE TABLE audit_events (
    id TEXT PRIMARY KEY NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    workspace_id TEXT,
    resource_id TEXT,
    subject TEXT NOT NULL,
    action TEXT NOT NULL,
    entity_type TEXT NOT NULL CHECK (length(entity_type) > 0),
    entity_id TEXT,
    properties TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(properties) AND json_type(properties) = 'array'),
    trace_id TEXT NOT NULL CHECK (length(trace_id) > 0),
    details TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(details) AND json_type(details) = 'object'),
    -- The access token the action was taken with, such as an AI assistant's.
    credential_id TEXT
);
CREATE INDEX auditevent_workspace_id_created_at ON audit_events(workspace_id, created_at);
CREATE INDEX auditevent_resource_id_created_at ON audit_events(resource_id, created_at);
CREATE INDEX auditevent_created_at_id ON audit_events(created_at, id);
CREATE INDEX auditevent_subject_created_at ON audit_events(subject, created_at);
CREATE INDEX auditevent_entity_type_entity_id_created_at ON audit_events(entity_type, entity_id, created_at);

-- A transactional counter orders notifications by commit and survives retention.
CREATE TABLE live_change_position (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    position INTEGER NOT NULL CHECK (position >= 0)
);
INSERT INTO live_change_position (id, position) VALUES (1, 0);

CREATE TABLE live_changes (
    id INTEGER PRIMARY KEY NOT NULL CHECK (id > 0),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    kind TEXT NOT NULL CHECK (kind IN ('item', 'operation', 'run', 'access', 'notification', 'comment')),
    entity_id TEXT NOT NULL CHECK (length(entity_id) BETWEEN 1 AND 200),
    action TEXT NOT NULL
);
CREATE INDEX livechange_created_at ON live_changes(created_at);

-- =============================================================================
-- 8. Seed Data: Organization System Term Store
-- =============================================================================

INSERT INTO term_groups (id, created_at, updated_at, name, description, is_system, version)
VALUES ('00000000-0000-4000-8000-000000000001', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'System', 'Term sets managed by PaperGo.', 1, 1);

INSERT INTO term_sets (id, created_at, updated_at, group_id, "key", name, description, is_open, is_keywords, version)
VALUES ('00000000-0000-4000-8000-000000000002', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '00000000-0000-4000-8000-000000000001', 'keywords', 'Keywords', 'Free keywords that people add while tagging items.', 1, 1, 1);

-- The schema version: the number of the last migration in
-- internal/dal/migrations the database includes. The DAL writes it when it
-- installs this schema or applies a migration.
CREATE TABLE schema_metadata (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL CHECK (version >= 0)
);
