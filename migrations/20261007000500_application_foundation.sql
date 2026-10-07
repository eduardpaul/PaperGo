-- Add the reusable application foundation without changing retained content.
-- Convert legacy augmented inheritance into explicit scopes with the same
-- effective grant set, so this migration neither broadens nor removes access.
CREATE TEMP TABLE augmented_scopes AS SELECT id FROM resources r WHERE inherit_permissions AND EXISTS(SELECT 1 FROM grants g WHERE g.resource_id=r.id);
CREATE TEMP TABLE copied_scope_grants AS
 WITH RECURSIVE a(scope,id,parent_id,inherit_permissions,depth) AS (
  SELECT r.id,r.id,r.parent_id,r.inherit_permissions,0 FROM resources r JOIN augmented_scopes x ON x.id=r.id
  UNION ALL SELECT a.scope,p.id,p.parent_id,p.inherit_permissions,a.depth+1 FROM resources p JOIN a ON p.id=a.parent_id WHERE a.inherit_permissions AND a.depth<32
 ) SELECT DISTINCT a.scope,g.subject,g.action FROM a JOIN grants g ON g.resource_id=a.id;
INSERT INTO grants(id,created_at,subject,action,effect,resource_id)
 SELECT lower(hex(randomblob(16))),CURRENT_TIMESTAMP,c.subject,c.action,'allow',c.scope FROM copied_scope_grants c
 WHERE NOT EXISTS(SELECT 1 FROM grants g WHERE g.resource_id=c.scope AND g.subject=c.subject AND g.action=c.action);
UPDATE resources SET inherit_permissions=0,version=version+1 WHERE id IN (SELECT id FROM augmented_scopes);
INSERT INTO audit_events(id,created_at,workspace_id,resource_id,subject,action,details)
 SELECT lower(hex(randomblob(16))),CURRENT_TIMESTAMP,r.workspace_id,r.id,'migration','permissions.exclusive_scope_migration','{}'
 FROM resources r JOIN augmented_scopes x ON x.id=r.id;
DROP TABLE copied_scope_grants;
DROP TABLE augmented_scopes;
CREATE TRIGGER resource_permission_scope_validate BEFORE UPDATE OF inherit_permissions ON resources BEGIN
 SELECT CASE WHEN new.inherit_permissions AND (new.kind='workspace' OR EXISTS(SELECT 1 FROM grants WHERE resource_id=new.id)) THEN RAISE(ABORT,'inherited resources cannot contain grants') END;
END;
CREATE TRIGGER grant_exclusive_scope_insert BEFORE INSERT ON grants BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM resources WHERE id=new.resource_id AND inherit_permissions) THEN RAISE(ABORT,'grants require an exclusive permission scope') END;
END;
CREATE TRIGGER grant_exclusive_scope_update BEFORE UPDATE OF resource_id ON grants BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM resources WHERE id=new.resource_id AND inherit_permissions) THEN RAISE(ABORT,'grants require an exclusive permission scope') END;
END;
ALTER TABLE field_definitions ADD COLUMN options JSON NOT NULL DEFAULT '{}' CHECK(json_valid(options) AND json_type(options)='object');
DROP TRIGGER field_type_validate;
CREATE TRIGGER field_type_validate BEFORE INSERT ON field_definitions BEGIN
 SELECT CASE WHEN new.type NOT IN ('text','note','email','url','date','lookup','term','choice','datetime','integer','decimal','number','boolean') OR new.scale<0 OR new.scale>9 OR (new.type<>'decimal' AND new.scale<>0)
 THEN RAISE(ABORT,'invalid field type or scale') END;
END;
CREATE TRIGGER field_options_identity BEFORE UPDATE OF options ON field_definitions BEGIN
 SELECT CASE WHEN coalesce(json_extract(new.options,'$.multiple'),0)<>coalesce(json_extract(old.options,'$.multiple'),0)
 OR coalesce(json_extract(new.options,'$.lookup_container_id'),'')<>coalesce(json_extract(old.options,'$.lookup_container_id'),'')
 OR coalesce(json_extract(new.options,'$.term_set_id'),'')<>coalesce(json_extract(old.options,'$.term_set_id'),'')
 THEN RAISE(ABORT,'field cardinality and reference scope are immutable') END;
END;

-- This is a derived table. Copy scalar indexes and add ordered multi-values.
CREATE TABLE expanded_field_values (
 id TEXT PRIMARY KEY NOT NULL, created_at DATETIME NOT NULL, surface_id TEXT NOT NULL REFERENCES item_surfaces(id),
 container_id TEXT NOT NULL, item_id TEXT NOT NULL, surface TEXT NOT NULL, field_key TEXT NOT NULL, field_type TEXT NOT NULL,
 scale INTEGER NOT NULL DEFAULT 0, ordinal INTEGER NOT NULL DEFAULT 0 CHECK(ordinal>=0 AND ordinal<100),
 value_text TEXT, value_integer INTEGER, value_number REAL, value_boolean BOOLEAN,
 CHECK((value_text IS NOT NULL)+(value_integer IS NOT NULL)+(value_number IS NOT NULL)+(value_boolean IS NOT NULL)=1),
 CHECK((field_type IN ('text','note','email','url','date','lookup','term','choice','datetime') AND value_text IS NOT NULL)
 OR (field_type IN ('integer','decimal') AND value_integer IS NOT NULL)
 OR (field_type='number' AND value_number IS NOT NULL)
 OR (field_type='boolean' AND value_boolean IS NOT NULL AND value_boolean IN (0,1))),
 CHECK(scale BETWEEN 0 AND 9 AND (field_type='decimal' OR scale=0)),
 CHECK(value_integer IS NULL OR typeof(value_integer)='integer'),
 CHECK(value_number IS NULL OR typeof(value_number) IN ('real','integer'))
);
INSERT INTO expanded_field_values(id,created_at,surface_id,container_id,item_id,surface,field_key,field_type,scale,value_text,value_integer,value_number,value_boolean)
 SELECT id,created_at,surface_id,container_id,item_id,surface,field_key,field_type,scale,value_text,value_integer,value_number,value_boolean FROM field_values;
DROP TABLE field_values;
ALTER TABLE expanded_field_values RENAME TO field_values;
CREATE UNIQUE INDEX fieldvalue_surface_id_field_key_ordinal ON field_values(surface_id,field_key,ordinal);
CREATE INDEX fieldvalue_container_id_surface_field_key_value_integer_item_id ON field_values(container_id,surface,field_key,value_integer,item_id);
CREATE INDEX fieldvalue_container_id_surface_field_key_value_text_item_id ON field_values(container_id,surface,field_key,value_text,item_id);
CREATE INDEX fieldvalue_container_id_surface_field_key_value_number_item_id ON field_values(container_id,surface,field_key,value_number,item_id);
CREATE INDEX fieldvalue_container_id_surface_field_key_value_boolean_item_id ON field_values(container_id,surface,field_key,value_boolean,item_id);
CREATE TRIGGER field_value_validate BEFORE INSERT ON field_values BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM item_surfaces WHERE id=new.surface_id AND item_id=new.item_id AND container_id=new.container_id AND surface=new.surface)
 THEN RAISE(ABORT,'invalid field index ownership') END;
END;
CREATE TRIGGER field_value_no_update BEFORE UPDATE ON field_values BEGIN SELECT RAISE(ABORT,'replace derived field index rows'); END;

CREATE TABLE schema_templates (
 id TEXT PRIMARY KEY NOT NULL,created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL,workspace_id TEXT NOT NULL REFERENCES resources(id),
 key TEXT NOT NULL,name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',definition JSON NOT NULL CHECK(json_valid(definition) AND json_type(definition)='object'),
 version INTEGER NOT NULL DEFAULT 1 CHECK(version>0)
);
CREATE UNIQUE INDEX schematemplate_workspace_id_key ON schema_templates(workspace_id,key);
CREATE TABLE term_sets (
 id TEXT PRIMARY KEY NOT NULL,created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL,workspace_id TEXT NOT NULL REFERENCES resources(id),
 key TEXT NOT NULL,name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 1 CHECK(version>0)
);
CREATE UNIQUE INDEX termset_workspace_id_key ON term_sets(workspace_id,key);
CREATE TABLE terms (
 id TEXT PRIMARY KEY NOT NULL,created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL,term_set_id TEXT NOT NULL REFERENCES term_sets(id),
 parent_id TEXT REFERENCES terms(id),name TEXT NOT NULL,normalized_name TEXT NOT NULL,
 labels JSON NOT NULL CHECK(json_valid(labels) AND json_type(labels)='object'),synonyms JSON NOT NULL CHECK(json_valid(synonyms) AND json_type(synonyms)='array'),
 deprecated BOOLEAN NOT NULL DEFAULT 0,version INTEGER NOT NULL DEFAULT 1 CHECK(version>0)
);
CREATE UNIQUE INDEX term_term_set_id_normalized_name ON terms(term_set_id,normalized_name);
CREATE INDEX term_term_set_id_id ON terms(term_set_id,id);
CREATE TRIGGER term_parent_validate BEFORE INSERT ON terms WHEN new.parent_id IS NOT NULL BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM terms WHERE id=new.parent_id AND term_set_id=new.term_set_id) THEN RAISE(ABORT,'term parent must belong to the same term set') END;
END;
CREATE TRIGGER term_identity_immutable BEFORE UPDATE OF term_set_id,parent_id ON terms BEGIN
 SELECT CASE WHEN new.term_set_id IS NOT old.term_set_id OR new.parent_id IS NOT old.parent_id THEN RAISE(ABORT,'term ownership and parent are immutable') END;
END;
CREATE TABLE list_views (
 id TEXT PRIMARY KEY NOT NULL,created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL,container_id TEXT NOT NULL REFERENCES resources(id),
 name TEXT NOT NULL,columns JSON NOT NULL CHECK(json_valid(columns) AND json_type(columns)='array'),
 query JSON NOT NULL CHECK(json_valid(query) AND json_type(query)='object'),layout TEXT NOT NULL DEFAULT 'table' CHECK(layout IN ('table','board','calendar','gallery')),
 is_default BOOLEAN NOT NULL DEFAULT 0,version INTEGER NOT NULL DEFAULT 1 CHECK(version>0)
);
CREATE UNIQUE INDEX listview_container_id_name ON list_views(container_id,name);
CREATE INDEX listview_container_id_id ON list_views(container_id,id);
CREATE UNIQUE INDEX listview_one_default ON list_views(container_id) WHERE is_default=1;
CREATE TRIGGER listview_container_validate BEFORE INSERT ON list_views BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM resources WHERE id=new.container_id AND kind IN ('list','library')) THEN RAISE(ABORT,'view requires a collection') END;
END;
CREATE TRIGGER listview_container_immutable BEFORE UPDATE OF container_id ON list_views BEGIN
 SELECT CASE WHEN new.container_id IS NOT old.container_id THEN RAISE(ABORT,'view ownership is immutable') END;
END;
CREATE TABLE relationship_types (
 id TEXT PRIMARY KEY NOT NULL,created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL,workspace_id TEXT NOT NULL REFERENCES resources(id),
 key TEXT NOT NULL,label TEXT NOT NULL,inverse_label TEXT NOT NULL DEFAULT '',directed BOOLEAN NOT NULL DEFAULT 1,
 max_incoming INTEGER CHECK(max_incoming BETWEEN 1 AND 1000),max_outgoing INTEGER CHECK(max_outgoing BETWEEN 1 AND 1000),
 attributes JSON NOT NULL CHECK(json_valid(attributes) AND json_type(attributes)='object'),version INTEGER NOT NULL DEFAULT 1 CHECK(version>0),
 CHECK(directed OR (inverse_label='' AND max_incoming IS NULL AND max_outgoing IS NULL))
);
CREATE UNIQUE INDEX relationshiptype_workspace_id_key ON relationship_types(workspace_id,key);
ALTER TABLE relationships ADD COLUMN type_id TEXT REFERENCES relationship_types(id);
ALTER TABLE relationships ADD COLUMN directed BOOLEAN NOT NULL DEFAULT 1;
ALTER TABLE relationships ADD COLUMN version INTEGER NOT NULL DEFAULT 1 CHECK(version>0);
CREATE UNIQUE INDEX relationship_type_id_source_id_target_id ON relationships(type_id,source_id,target_id);
CREATE TRIGGER schema_templates_workspace_validate BEFORE INSERT ON schema_templates BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM resources WHERE id=new.workspace_id AND kind='workspace') THEN RAISE(ABORT,'configuration requires a workspace') END;
END;
CREATE TRIGGER schema_templates_identity_immutable BEFORE UPDATE OF workspace_id,key ON schema_templates BEGIN
 SELECT CASE WHEN new.workspace_id IS NOT old.workspace_id OR new.key IS NOT old.key THEN RAISE(ABORT,'configuration identity is immutable') END;
END;
CREATE TRIGGER term_sets_workspace_validate BEFORE INSERT ON term_sets BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM resources WHERE id=new.workspace_id AND kind='workspace') THEN RAISE(ABORT,'configuration requires a workspace') END;
END;
CREATE TRIGGER term_sets_identity_immutable BEFORE UPDATE OF workspace_id,key ON term_sets BEGIN
 SELECT CASE WHEN new.workspace_id IS NOT old.workspace_id OR new.key IS NOT old.key THEN RAISE(ABORT,'configuration identity is immutable') END;
END;
CREATE TRIGGER relationship_types_workspace_validate BEFORE INSERT ON relationship_types BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM resources WHERE id=new.workspace_id AND kind='workspace') THEN RAISE(ABORT,'configuration requires a workspace') END;
END;
CREATE INDEX relationship_type_id_target_id ON relationships(type_id,target_id);
CREATE TRIGGER relationship_type_validate BEFORE INSERT ON relationships WHEN new.type_id IS NOT NULL BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM relationship_types WHERE id=new.type_id AND workspace_id=new.workspace_id AND key=new.name AND directed=new.directed)
 OR (new.directed=0 AND new.source_id>=new.target_id) THEN RAISE(ABORT,'invalid typed relationship') END;
 SELECT CASE WHEN EXISTS(SELECT 1 FROM relationship_types t WHERE t.id=new.type_id AND
 ((t.max_outgoing IS NOT NULL AND (SELECT count(*) FROM relationships WHERE type_id=t.id AND source_id=new.source_id)>=t.max_outgoing)
 OR (t.max_incoming IS NOT NULL AND (SELECT count(*) FROM relationships WHERE type_id=t.id AND target_id=new.target_id)>=t.max_incoming)))
 THEN RAISE(ABORT,'relationship cardinality exceeded') END;
END;
CREATE TRIGGER relationship_type_identity BEFORE UPDATE OF type_id,directed ON relationships BEGIN
 SELECT CASE WHEN new.type_id IS NOT old.type_id OR new.directed IS NOT old.directed THEN RAISE(ABORT,'relationship type and direction are immutable') END;
END;
CREATE TRIGGER relationship_policy_identity BEFORE UPDATE OF workspace_id,key,directed ON relationship_types BEGIN
 SELECT CASE WHEN new.workspace_id IS NOT old.workspace_id OR new.key IS NOT old.key OR new.directed IS NOT old.directed THEN RAISE(ABORT,'relationship policy identity is immutable') END;
END;
