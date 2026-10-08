-- Denormalize each resource's nearest exclusive ACL scope so permission checks
-- are one indexed lookup instead of a recursive walk up the hierarchy.
-- Triggers own scope_id: the API never writes it.
ALTER TABLE `resources` ADD COLUMN `scope_id` text NULL;
-- Covering browse indexes: rows a subject cannot read are rejected from the
-- index alone instead of loading every resource row in the scope.
DROP INDEX `resource_workspace_id_id`;
DROP INDEX `resource_parent_id_id`;

-- Search rows depend only on these columns; firing on every update would also
-- rewrite FTS for version bumps and scope maintenance.
DROP TRIGGER resource_search_update;
CREATE TRIGGER resource_search_update AFTER UPDATE OF name,tags,"values" ON resources BEGIN
  INSERT INTO resource_search(resource_search,rowid,id,name,tags,"values")
    VALUES ('delete',old.rowid,old.id,old.name,old.tags,old."values");
  INSERT INTO resource_search(rowid,id,name,tags,"values")
    VALUES (new.rowid,new.id,new.name,new.tags,new."values");
END;

CREATE TEMP TABLE scope_backfill AS
 WITH RECURSIVE s(id,scope) AS (
  SELECT id,id FROM resources WHERE NOT inherit_permissions
  UNION ALL SELECT c.id,s.scope FROM resources c JOIN s ON c.parent_id=s.id WHERE c.inherit_permissions
 ) SELECT id,scope FROM s;
UPDATE resources SET scope_id=b.scope FROM scope_backfill b WHERE b.id=resources.id;
DROP TABLE scope_backfill;
CREATE INDEX `resource_workspace_id_id_scope_id` ON `resources` (`workspace_id`, `id`, `scope_id`);
CREATE INDEX `resource_parent_id_id_scope_id` ON `resources` (`parent_id`, `id`, `scope_id`);

-- parent_id is immutable, so a scope changes only on insert or when a resource
-- breaks or resets inheritance; then every descendant reached through
-- inheriting resources follows it, while nested exclusive scopes keep their own.
CREATE TRIGGER resource_scope_insert AFTER INSERT ON resources BEGIN
  UPDATE resources SET scope_id=CASE WHEN new.inherit_permissions THEN (SELECT p.scope_id FROM resources p WHERE p.id=new.parent_id) ELSE new.id END
   WHERE id=new.id;
END;
CREATE TRIGGER resource_scope_inheritance AFTER UPDATE OF inherit_permissions ON resources WHEN old.inherit_permissions IS NOT new.inherit_permissions BEGIN
  UPDATE resources SET scope_id=CASE WHEN new.inherit_permissions THEN (SELECT p.scope_id FROM resources p WHERE p.id=new.parent_id) ELSE new.id END
   WHERE id IN (WITH RECURSIVE sub(id) AS (
    SELECT new.id UNION ALL SELECT c.id FROM resources c JOIN sub ON c.parent_id=sub.id WHERE c.inherit_permissions
   ) SELECT id FROM sub);
END;
