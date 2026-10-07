-- create "audit_events" table
CREATE TABLE `audit_events` (`id` text NOT NULL, `created_at` datetime NOT NULL, `workspace_id` text NOT NULL, `resource_id` text NOT NULL, `subject` text NOT NULL, `action` text NOT NULL, `details` json NOT NULL, PRIMARY KEY (`id`));
-- create index "auditevent_workspace_id_created_at" to table: "audit_events"
CREATE INDEX `auditevent_workspace_id_created_at` ON `audit_events` (`workspace_id`, `created_at`);
-- create index "auditevent_resource_id_created_at" to table: "audit_events"
CREATE INDEX `auditevent_resource_id_created_at` ON `audit_events` (`resource_id`, `created_at`);
-- create "blobs" table
CREATE TABLE `blobs` (`id` text NOT NULL, `created_at` datetime NOT NULL, `version` integer NOT NULL, `object_key` text NOT NULL, `filename` text NOT NULL, `content_type` text NOT NULL, `size` integer NOT NULL, `sha256` text NOT NULL, `item_id` text NOT NULL, PRIMARY KEY (`id`), CONSTRAINT `blobs_resources_blobs` FOREIGN KEY (`item_id`) REFERENCES `resources` (`id`) ON DELETE NO ACTION);
-- create index "blobs_object_key_key" to table: "blobs"
CREATE UNIQUE INDEX `blobs_object_key_key` ON `blobs` (`object_key`);
-- create index "blob_item_id_version" to table: "blobs"
CREATE UNIQUE INDEX `blob_item_id_version` ON `blobs` (`item_id`, `version`);
-- create "field_definitions" table
CREATE TABLE `field_definitions` (`id` text NOT NULL, `created_at` datetime NOT NULL, `key` text NOT NULL, `label` text NOT NULL, `type` text NOT NULL, `required` bool NOT NULL DEFAULT (false), `choices` json NOT NULL, `container_id` text NOT NULL, PRIMARY KEY (`id`), CONSTRAINT `field_definitions_resources_definitions` FOREIGN KEY (`container_id`) REFERENCES `resources` (`id`) ON DELETE NO ACTION);
-- create index "fielddefinition_container_id_key" to table: "field_definitions"
CREATE UNIQUE INDEX `fielddefinition_container_id_key` ON `field_definitions` (`container_id`, `key`);
-- create "grants" table
CREATE TABLE `grants` (`id` text NOT NULL, `created_at` datetime NOT NULL, `subject` text NOT NULL, `action` text NOT NULL, `effect` text NOT NULL, `resource_id` text NOT NULL, PRIMARY KEY (`id`), CONSTRAINT `grants_resources_grants` FOREIGN KEY (`resource_id`) REFERENCES `resources` (`id`) ON DELETE NO ACTION);
-- create index "grant_resource_id_subject_action" to table: "grants"
CREATE UNIQUE INDEX `grant_resource_id_subject_action` ON `grants` (`resource_id`, `subject`, `action`);
-- create index "grant_subject_resource_id" to table: "grants"
CREATE INDEX `grant_subject_resource_id` ON `grants` (`subject`, `resource_id`);
-- create "publications" table
CREATE TABLE `publications` (`id` text NOT NULL, `created_at` datetime NOT NULL, `version` integer NOT NULL, `published_by` text NOT NULL, `snapshot` json NOT NULL, `item_id` text NOT NULL, PRIMARY KEY (`id`), CONSTRAINT `publications_resources_publications` FOREIGN KEY (`item_id`) REFERENCES `resources` (`id`) ON DELETE NO ACTION);
-- create index "publication_item_id_version" to table: "publications"
CREATE UNIQUE INDEX `publication_item_id_version` ON `publications` (`item_id`, `version`);
-- create "relationships" table
CREATE TABLE `relationships` (`id` text NOT NULL, `created_at` datetime NOT NULL, `workspace_id` text NOT NULL, `name` text NOT NULL, `inverse_name` text NULL, `metadata` json NOT NULL, `source_id` text NOT NULL, `target_id` text NOT NULL, PRIMARY KEY (`id`), CONSTRAINT `relationships_resources_outgoing` FOREIGN KEY (`source_id`) REFERENCES `resources` (`id`) ON DELETE NO ACTION, CONSTRAINT `relationships_resources_incoming` FOREIGN KEY (`target_id`) REFERENCES `resources` (`id`) ON DELETE NO ACTION);
-- create index "relationship_source_id_name_target_id" to table: "relationships"
CREATE UNIQUE INDEX `relationship_source_id_name_target_id` ON `relationships` (`source_id`, `name`, `target_id`);
-- create index "relationship_source_id_name_id" to table: "relationships"
CREATE INDEX `relationship_source_id_name_id` ON `relationships` (`source_id`, `name`, `id`);
-- create index "relationship_target_id_name_id" to table: "relationships"
CREATE INDEX `relationship_target_id_name_id` ON `relationships` (`target_id`, `name`, `id`);
-- create index "relationship_target_id_inverse_name_id" to table: "relationships"
CREATE INDEX `relationship_target_id_inverse_name_id` ON `relationships` (`target_id`, `inverse_name`, `id`);
-- create "resources" table
CREATE TABLE `resources` (`id` text NOT NULL, `created_at` datetime NOT NULL, `workspace_id` text NOT NULL, `kind` text NOT NULL, `name` text NOT NULL, `tags` json NOT NULL, `values` json NOT NULL, `inherit_permissions` bool NOT NULL DEFAULT (true), `version` integer NOT NULL DEFAULT (1), `updated_at` datetime NOT NULL, `parent_id` text NULL, `container_id` text NULL, PRIMARY KEY (`id`), CONSTRAINT `resources_resources_children` FOREIGN KEY (`parent_id`) REFERENCES `resources` (`id`) ON DELETE SET NULL, CONSTRAINT `resources_resources_contained_items` FOREIGN KEY (`container_id`) REFERENCES `resources` (`id`) ON DELETE SET NULL);
-- create index "resource_workspace_id_kind_id" to table: "resources"
CREATE INDEX `resource_workspace_id_kind_id` ON `resources` (`workspace_id`, `kind`, `id`);
-- create index "resource_parent_id_id" to table: "resources"
CREATE INDEX `resource_parent_id_id` ON `resources` (`parent_id`, `id`);
-- create index "resource_container_id_id" to table: "resources"
CREATE INDEX `resource_container_id_id` ON `resources` (`container_id`, `id`);
