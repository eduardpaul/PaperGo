-- Upgrade additively: preserve the resource table and its FTS/containment triggers.
-- Existing explicit denies require an administrator to review their replacement;
-- silently removing them could broaden access.
CREATE TEMP TABLE additive_acl_upgrade_guard (safe INTEGER NOT NULL CHECK(safe=1));
INSERT INTO additive_acl_upgrade_guard SELECT CASE WHEN EXISTS(SELECT 1 FROM grants WHERE effect<>'allow') THEN 0 ELSE 1 END;
DROP TABLE additive_acl_upgrade_guard;

CREATE TABLE schema_revisions (
 id TEXT PRIMARY KEY NOT NULL, created_at DATETIME NOT NULL, container_id TEXT NOT NULL REFERENCES resources(id),
 revision_number INTEGER NOT NULL CHECK(revision_number>0), definition JSON NOT NULL CHECK(json_valid(definition) AND json_type(definition)='object'), created_by TEXT NOT NULL
);
CREATE UNIQUE INDEX schemarevision_container_id_revision_number ON schema_revisions(container_id,revision_number);
CREATE UNIQUE INDEX schemarevision_container_id_id ON schema_revisions(container_id,id);
CREATE TABLE item_revisions (
 id TEXT PRIMARY KEY NOT NULL, created_at DATETIME NOT NULL,
 item_id TEXT NOT NULL REFERENCES resources(id), container_id TEXT NOT NULL REFERENCES resources(id),
 schema_revision_id TEXT NOT NULL, revision_number INTEGER NOT NULL CHECK(revision_number>0),
 name TEXT NOT NULL, tags JSON NOT NULL CHECK(json_valid(tags) AND json_type(tags)='array'), payload JSON NOT NULL CHECK(json_valid(payload) AND json_type(payload)='object'),
 created_by TEXT NOT NULL, blob_id TEXT REFERENCES blobs(id),
 FOREIGN KEY(container_id,schema_revision_id) REFERENCES schema_revisions(container_id,id)
);
CREATE UNIQUE INDEX itemrevision_item_id_revision_number ON item_revisions(item_id,revision_number);
CREATE UNIQUE INDEX itemrevision_item_id_id ON item_revisions(item_id,id);
CREATE TABLE item_surfaces (
 id TEXT PRIMARY KEY NOT NULL, created_at DATETIME NOT NULL, item_id TEXT NOT NULL REFERENCES resources(id),
 container_id TEXT NOT NULL REFERENCES resources(id), workspace_id TEXT NOT NULL,
 surface TEXT NOT NULL CHECK(surface IN ('head','published')), revision_id TEXT NOT NULL,
 name TEXT NOT NULL, tags JSON NOT NULL CHECK(json_valid(tags) AND json_type(tags)='array'), payload JSON NOT NULL CHECK(json_valid(payload) AND json_type(payload)='object'),
 FOREIGN KEY(item_id,revision_id) REFERENCES item_revisions(item_id,id)
);
CREATE UNIQUE INDEX itemsurface_item_id_surface ON item_surfaces(item_id,surface);
CREATE INDEX itemsurface_workspace_id_surface_item_id ON item_surfaces(workspace_id,surface,item_id);
CREATE INDEX itemsurface_container_id_surface_item_id ON item_surfaces(container_id,surface,item_id);
CREATE TABLE field_values (
 id TEXT PRIMARY KEY NOT NULL, created_at DATETIME NOT NULL, surface_id TEXT NOT NULL REFERENCES item_surfaces(id),
 container_id TEXT NOT NULL, item_id TEXT NOT NULL, surface TEXT NOT NULL,
 field_key TEXT NOT NULL, field_type TEXT NOT NULL, scale INTEGER NOT NULL DEFAULT 0,
 value_text TEXT, value_integer INTEGER, value_number REAL, value_boolean BOOLEAN,
 CHECK((value_text IS NOT NULL)+(value_integer IS NOT NULL)+(value_number IS NOT NULL)+(value_boolean IS NOT NULL)=1),
 CHECK((field_type IN ('text','choice','datetime') AND value_text IS NOT NULL)
 OR (field_type IN ('integer','decimal') AND value_integer IS NOT NULL)
 OR (field_type='number' AND value_number IS NOT NULL)
 OR (field_type='boolean' AND value_boolean IS NOT NULL AND value_boolean IN (0,1))),
 CHECK(scale BETWEEN 0 AND 9 AND (field_type='decimal' OR scale=0)),
 CHECK(value_integer IS NULL OR typeof(value_integer)='integer'),
 CHECK(value_number IS NULL OR typeof(value_number) IN ('real','integer'))
);
CREATE UNIQUE INDEX fieldvalue_surface_id_field_key ON field_values(surface_id,field_key);
CREATE INDEX fieldvalue_container_id_surface_field_key_value_integer_item_id ON field_values(container_id,surface,field_key,value_integer,item_id);
CREATE INDEX fieldvalue_container_id_surface_field_key_value_text_item_id ON field_values(container_id,surface,field_key,value_text,item_id);
CREATE INDEX fieldvalue_container_id_surface_field_key_value_number_item_id ON field_values(container_id,surface,field_key,value_number,item_id);
CREATE INDEX fieldvalue_container_id_surface_field_key_value_boolean_item_id ON field_values(container_id,surface,field_key,value_boolean,item_id);

ALTER TABLE resources ADD COLUMN head_revision_id TEXT REFERENCES item_revisions(id);
ALTER TABLE resources ADD COLUMN published_revision_id TEXT REFERENCES item_revisions(id);
ALTER TABLE resources ADD COLUMN schema_head_id TEXT REFERENCES schema_revisions(id);
ALTER TABLE resources ADD COLUMN next_revision_number INTEGER NOT NULL DEFAULT 1;
ALTER TABLE resources ADD COLUMN publishing_enabled BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE field_definitions ADD COLUMN indexed BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE field_definitions ADD COLUMN scale INTEGER NOT NULL DEFAULT 0;
ALTER TABLE publications ADD COLUMN action TEXT NOT NULL DEFAULT 'publish';
ALTER TABLE publications ADD COLUMN revision_id TEXT REFERENCES item_revisions(id);
DROP INDEX publication_item_id_version;
CREATE UNIQUE INDEX publication_item_id_version_action ON publications(item_id,version,action);

-- Freeze the legacy field schema, retain each saved publication as a revision,
-- then append the current draft. Earlier overwritten drafts cannot be recovered.
INSERT INTO schema_revisions(id,created_at,container_id,revision_number,definition,created_by)
 SELECT c.id,c.created_at,c.id,1,json_object('fields',json(COALESCE((
 SELECT json_group_array(json_object('id',f.key,'label',f.label,'type',f.type,
 'required',json(CASE WHEN f.required THEN 'true' ELSE 'false' END),
 'choices',json(f.choices),'indexed',json('false'),'scale',0))
 FROM field_definitions f WHERE f.container_id=c.id),'[]'))),'migration'
 FROM resources c WHERE c.kind IN ('list','library');
INSERT INTO item_revisions(id,created_at,item_id,container_id,schema_revision_id,revision_number,name,tags,payload,created_by,blob_id)
 SELECT p.id,p.created_at,p.item_id,r.container_id,r.container_id,
 row_number() OVER(PARTITION BY p.item_id ORDER BY p.version,p.id),
 COALESCE(json_extract(p.snapshot,'$.name'),r.name),COALESCE(json_extract(p.snapshot,'$.tags'),'[]'),
 COALESCE(json_extract(p.snapshot,'$.values'),'{}'),p.published_by,json_extract(p.snapshot,'$.blob_id')
 FROM publications p JOIN resources r ON r.id=p.item_id;
INSERT INTO item_revisions(id,created_at,item_id,container_id,schema_revision_id,revision_number,name,tags,payload,created_by,blob_id)
 SELECT r.id,r.updated_at,r.id,r.container_id,r.container_id,
 (SELECT count(*)+1 FROM publications p WHERE p.item_id=r.id),r.name,r.tags,r."values",'migration',
 (SELECT b.id FROM blobs b WHERE b.item_id=r.id ORDER BY b.version DESC LIMIT 1)
 FROM resources r WHERE r.kind='item';
UPDATE resources SET schema_head_id=id,publishing_enabled=1 WHERE kind IN ('list','library');
UPDATE resources SET head_revision_id=id,
 next_revision_number=(SELECT count(*)+2 FROM publications p WHERE p.item_id=resources.id),
 published_revision_id=(SELECT p.id FROM publications p WHERE p.item_id=resources.id ORDER BY p.version DESC LIMIT 1)
 WHERE kind='item';
DROP TRIGGER publication_immutable;
UPDATE publications SET revision_id=id;
CREATE TRIGGER publication_immutable BEFORE UPDATE ON publications BEGIN SELECT RAISE(ABORT,'publication snapshots are immutable'); END;
INSERT INTO item_surfaces(id,created_at,item_id,container_id,workspace_id,surface,revision_id,name,tags,payload)
 SELECT r.id,r.created_at,r.id,r.container_id,r.workspace_id,'head',v.id,v.name,v.tags,v.payload
 FROM resources r JOIN item_revisions v ON v.id=r.head_revision_id;
INSERT INTO item_surfaces(id,created_at,item_id,container_id,workspace_id,surface,revision_id,name,tags,payload)
 SELECT v.id,v.created_at,r.id,r.container_id,r.workspace_id,'published',v.id,v.name,v.tags,v.payload
 FROM resources r JOIN item_revisions v ON v.id=r.published_revision_id;

CREATE VIRTUAL TABLE item_surface_search USING fts5(id UNINDEXED,name,tags,payload,content='item_surfaces',content_rowid='rowid',tokenize='unicode61');
CREATE TRIGGER item_surface_search_insert AFTER INSERT ON item_surfaces BEGIN
 INSERT INTO item_surface_search(rowid,id,name,tags,payload) VALUES(new.rowid,new.id,new.name,new.tags,new.payload);
END;
CREATE TRIGGER item_surface_search_delete AFTER DELETE ON item_surfaces BEGIN
 INSERT INTO item_surface_search(item_surface_search,rowid,id,name,tags,payload) VALUES('delete',old.rowid,old.id,old.name,old.tags,old.payload);
END;
CREATE TRIGGER item_surface_search_update AFTER UPDATE ON item_surfaces BEGIN
 INSERT INTO item_surface_search(item_surface_search,rowid,id,name,tags,payload) VALUES('delete',old.rowid,old.id,old.name,old.tags,old.payload);
 INSERT INTO item_surface_search(rowid,id,name,tags,payload) VALUES(new.rowid,new.id,new.name,new.tags,new.payload);
END;
INSERT INTO item_surface_search(item_surface_search) VALUES('rebuild');
CREATE TABLE item_surface_tags(surface_id TEXT NOT NULL REFERENCES item_surfaces(id) ON DELETE CASCADE,tag TEXT NOT NULL,PRIMARY KEY(tag,surface_id)) WITHOUT ROWID;
CREATE INDEX item_surface_tags_by_surface ON item_surface_tags(surface_id,tag);
INSERT INTO item_surface_tags SELECT p.id,j.value FROM item_surfaces p,json_each(p.tags) j;
CREATE TRIGGER item_surface_tags_insert AFTER INSERT ON item_surfaces BEGIN INSERT INTO item_surface_tags SELECT new.id,value FROM json_each(new.tags); END;
CREATE TRIGGER item_surface_tags_update AFTER UPDATE OF tags ON item_surfaces BEGIN
 DELETE FROM item_surface_tags WHERE surface_id=old.id;
 INSERT INTO item_surface_tags SELECT new.id,value FROM json_each(new.tags);
END;

CREATE TRIGGER schema_revision_validate BEFORE INSERT ON schema_revisions BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM resources WHERE id=new.container_id AND kind IN ('list','library')) THEN RAISE(ABORT,'invalid schema container') END;
END;
CREATE TRIGGER schema_revision_immutable BEFORE UPDATE ON schema_revisions BEGIN SELECT RAISE(ABORT,'schema revision is immutable'); END;
CREATE TRIGGER schema_revision_retained BEFORE DELETE ON schema_revisions BEGIN SELECT RAISE(ABORT,'schema revisions are retained'); END;
CREATE TRIGGER item_revision_validate BEFORE INSERT ON item_revisions BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM resources WHERE id=new.item_id AND kind='item' AND container_id=new.container_id)
 OR (new.blob_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM blobs WHERE id=new.blob_id AND item_id=new.item_id))
 THEN RAISE(ABORT,'invalid revision ownership') END;
END;
CREATE TRIGGER item_revision_immutable BEFORE UPDATE ON item_revisions BEGIN SELECT RAISE(ABORT,'item revision is immutable'); END;
CREATE TRIGGER item_revision_retained BEFORE DELETE ON item_revisions BEGIN SELECT RAISE(ABORT,'item revisions are retained'); END;
CREATE TRIGGER resource_revision_pointers BEFORE UPDATE OF head_revision_id,published_revision_id,schema_head_id,publishing_enabled ON resources BEGIN
 SELECT CASE WHEN (new.head_revision_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM item_revisions WHERE id=new.head_revision_id AND item_id=new.id))
 OR (new.published_revision_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM item_revisions WHERE id=new.published_revision_id AND item_id=new.id))
 OR (new.schema_head_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM schema_revisions WHERE id=new.schema_head_id AND container_id=new.id))
 OR (new.publishing_enabled AND new.kind NOT IN ('list','library'))
 THEN RAISE(ABORT,'invalid revision pointer') END;
END;
CREATE TRIGGER publication_revision_validate BEFORE INSERT ON publications BEGIN
 SELECT CASE WHEN new.revision_id IS NULL OR new.action NOT IN ('publish','unpublish') OR NOT EXISTS(SELECT 1 FROM item_revisions WHERE id=new.revision_id AND item_id=new.item_id)
 THEN RAISE(ABORT,'invalid publication revision') END;
END;
CREATE TRIGGER publication_retained BEFORE DELETE ON publications BEGIN SELECT RAISE(ABORT,'publication events are retained'); END;
CREATE TRIGGER resource_revision_insert BEFORE INSERT ON resources BEGIN
 SELECT CASE WHEN new.head_revision_id IS NOT NULL OR new.published_revision_id IS NOT NULL OR new.schema_head_id IS NOT NULL
 OR (new.publishing_enabled AND new.kind NOT IN ('list','library')) OR new.next_revision_number<>1
 THEN RAISE(ABORT,'invalid initial revision state') END;
END;
CREATE TRIGGER resource_revision_counter BEFORE UPDATE OF next_revision_number ON resources BEGIN
 SELECT CASE WHEN new.next_revision_number<old.next_revision_number OR new.next_revision_number<1 THEN RAISE(ABORT,'revision counter cannot decrease') END;
END;
CREATE TRIGGER field_identity_immutable BEFORE UPDATE OF key,type,scale,container_id ON field_definitions BEGIN
 SELECT CASE WHEN new.key IS NOT old.key OR new.type IS NOT old.type OR new.scale IS NOT old.scale OR new.container_id IS NOT old.container_id
 THEN RAISE(ABORT,'field keys, types, scales and ownership are immutable') END;
END;
CREATE TRIGGER field_type_validate BEFORE INSERT ON field_definitions BEGIN
 SELECT CASE WHEN new.type NOT IN ('text','choice','datetime','integer','decimal','number','boolean') OR new.scale<0 OR new.scale>9 OR (new.type<>'decimal' AND new.scale<>0)
 THEN RAISE(ABORT,'invalid field type or scale') END;
END;
CREATE TRIGGER item_surface_validate_insert BEFORE INSERT ON item_surfaces BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM resources r JOIN item_revisions v ON v.id=new.revision_id WHERE r.id=new.item_id AND r.kind='item' AND r.container_id=new.container_id AND r.workspace_id=new.workspace_id AND v.item_id=r.id AND v.name=new.name AND v.tags=new.tags AND v.payload=new.payload)
 THEN RAISE(ABORT,'invalid surface projection') END;
END;
CREATE TRIGGER item_surface_validate_update BEFORE UPDATE ON item_surfaces BEGIN
 SELECT CASE WHEN new.item_id IS NOT old.item_id OR new.container_id IS NOT old.container_id OR new.workspace_id IS NOT old.workspace_id OR new.surface IS NOT old.surface
 OR NOT EXISTS(SELECT 1 FROM item_revisions WHERE id=new.revision_id AND item_id=new.item_id AND name=new.name AND tags=new.tags AND payload=new.payload)
 THEN RAISE(ABORT,'invalid surface projection') END;
END;
CREATE TRIGGER field_value_validate BEFORE INSERT ON field_values BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM item_surfaces WHERE id=new.surface_id AND item_id=new.item_id AND container_id=new.container_id AND surface=new.surface)
 THEN RAISE(ABORT,'invalid field index ownership') END;
END;
CREATE TRIGGER field_value_no_update BEFORE UPDATE ON field_values BEGIN SELECT RAISE(ABORT,'replace derived field index rows'); END;
CREATE TRIGGER additive_grants_insert BEFORE INSERT ON grants BEGIN
 SELECT CASE WHEN new.effect<>'allow' OR new.action NOT IN ('read','read_draft','write','publish','manage') THEN RAISE(ABORT,'only additive grants are supported') END;
END;
CREATE TRIGGER additive_grants_update BEFORE UPDATE ON grants BEGIN
 SELECT CASE WHEN new.effect<>'allow' OR new.action NOT IN ('read','read_draft','write','publish','manage') THEN RAISE(ABORT,'only additive grants are supported') END;
END;
