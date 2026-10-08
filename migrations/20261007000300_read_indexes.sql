-- Cover keyset reads when a relationship name or resource kind is not supplied.
CREATE INDEX resource_workspace_id_id_scope_id ON resources(workspace_id,id,scope_id);
CREATE INDEX relationship_source_id_id ON relationships(source_id,id);
CREATE INDEX relationship_target_id_id ON relationships(target_id,id);
