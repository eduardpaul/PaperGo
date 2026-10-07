-- SQLite-specific read indexes and integrity rules. Keep this migration when
-- generating later Ent diffs; Ent does not model virtual tables or triggers.
CREATE VIRTUAL TABLE resource_search USING fts5(
  id UNINDEXED, name, tags, "values",
  content='resources', content_rowid='rowid', tokenize='unicode61'
);

CREATE TRIGGER resource_search_insert AFTER INSERT ON resources BEGIN
  INSERT INTO resource_search(rowid,id,name,tags,"values")
    VALUES (new.rowid,new.id,new.name,new.tags,new."values");
END;
CREATE TRIGGER resource_search_delete AFTER DELETE ON resources BEGIN
  INSERT INTO resource_search(resource_search,rowid,id,name,tags,"values")
    VALUES ('delete',old.rowid,old.id,old.name,old.tags,old."values");
END;
CREATE TRIGGER resource_search_update AFTER UPDATE ON resources BEGIN
  INSERT INTO resource_search(resource_search,rowid,id,name,tags,"values")
    VALUES ('delete',old.rowid,old.id,old.name,old.tags,old."values");
  INSERT INTO resource_search(rowid,id,name,tags,"values")
    VALUES (new.rowid,new.id,new.name,new.tags,new."values");
END;
INSERT INTO resource_search(resource_search) VALUES ('rebuild');

CREATE TABLE resource_tags (
  resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  tag TEXT NOT NULL,
  PRIMARY KEY (tag,resource_id)
) WITHOUT ROWID;
CREATE INDEX resource_tags_by_resource ON resource_tags(resource_id,tag);
INSERT INTO resource_tags(resource_id,tag)
  SELECT r.id,j.value FROM resources r,json_each(r.tags) j;
CREATE TRIGGER resource_tags_insert AFTER INSERT ON resources BEGIN
  INSERT INTO resource_tags(resource_id,tag) SELECT new.id,value FROM json_each(new.tags);
END;
CREATE TRIGGER resource_tags_update AFTER UPDATE OF tags ON resources BEGIN
  DELETE FROM resource_tags WHERE resource_id=old.id;
  INSERT INTO resource_tags(resource_id,tag) SELECT new.id,value FROM json_each(new.tags);
END;

CREATE TRIGGER resource_validate_insert BEFORE INSERT ON resources BEGIN
  SELECT CASE WHEN new.kind='workspace' AND
    (new.workspace_id<>new.id OR new.parent_id IS NOT NULL OR new.container_id IS NOT NULL OR new.inherit_permissions<>0)
    THEN RAISE(ABORT,'invalid workspace') END;
  SELECT CASE WHEN new.kind<>'workspace' AND NOT EXISTS (
    SELECT 1 FROM resources p WHERE p.id=new.parent_id AND p.workspace_id=new.workspace_id
      AND ((new.kind IN ('list','library') AND p.kind='workspace' AND new.container_id IS NULL)
        OR (new.kind IN ('folder','item') AND (
          (p.kind IN ('list','library') AND new.container_id=p.id)
          OR (p.kind='folder' AND new.container_id=p.container_id))))
  ) THEN RAISE(ABORT,'invalid containment') END;
END;
CREATE TRIGGER resource_immutable_ownership BEFORE UPDATE OF id,workspace_id,parent_id,container_id,kind ON resources BEGIN
  SELECT CASE WHEN new.id IS NOT old.id OR new.workspace_id IS NOT old.workspace_id
    OR new.parent_id IS NOT old.parent_id OR new.container_id IS NOT old.container_id OR new.kind IS NOT old.kind
    THEN RAISE(ABORT,'resource ownership is immutable') END;
END;
CREATE TRIGGER relationship_validate_insert BEFORE INSERT ON relationships BEGIN
  SELECT CASE WHEN new.source_id=new.target_id OR NOT EXISTS (
    SELECT 1 FROM resources s JOIN resources t ON t.id=new.target_id
    WHERE s.id=new.source_id AND s.kind='item' AND t.kind='item'
      AND s.workspace_id=t.workspace_id AND s.workspace_id=new.workspace_id
  ) THEN RAISE(ABORT,'invalid relationship endpoints') END;
END;
CREATE TRIGGER relationship_immutable_endpoints BEFORE UPDATE OF source_id,target_id,workspace_id ON relationships BEGIN
  SELECT CASE WHEN new.source_id IS NOT old.source_id OR new.target_id IS NOT old.target_id OR new.workspace_id IS NOT old.workspace_id
    THEN RAISE(ABORT,'relationship endpoints are immutable') END;
END;
CREATE TRIGGER field_container_validate BEFORE INSERT ON field_definitions BEGIN
  SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM resources WHERE id=new.container_id AND kind IN ('list','library'))
    THEN RAISE(ABORT,'field definitions require list or library') END;
END;
CREATE TRIGGER publication_item_validate BEFORE INSERT ON publications BEGIN
  SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM resources WHERE id=new.item_id AND kind='item')
    THEN RAISE(ABORT,'publication requires item') END;
END;
CREATE TRIGGER publication_immutable BEFORE UPDATE ON publications BEGIN
  SELECT RAISE(ABORT,'publication snapshots are immutable');
END;
CREATE TRIGGER blob_item_validate BEFORE INSERT ON blobs BEGIN
  SELECT CASE WHEN NOT EXISTS (
    SELECT 1 FROM resources i JOIN resources c ON c.id=i.container_id
    WHERE i.id=new.item_id AND i.kind='item' AND c.kind='library'
  ) THEN RAISE(ABORT,'blob requires library item') END;
END;
CREATE TRIGGER blob_immutable BEFORE UPDATE ON blobs BEGIN
  SELECT RAISE(ABORT,'blob revisions are immutable');
END;
CREATE TRIGGER audit_immutable BEFORE UPDATE ON audit_events BEGIN
  SELECT RAISE(ABORT,'audit events are immutable');
END;
CREATE TRIGGER audit_retained BEFORE DELETE ON audit_events BEGIN
  SELECT RAISE(ABORT,'audit events are retained');
END;
