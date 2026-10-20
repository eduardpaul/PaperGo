package dms

import (
	"bytes"
	"encoding/json"
	"papergo/internal/jsonutil"
	"sort"
	"strings"
)

// trackAuditProperties compares business values captured before a mutation.
// Storage implementations do not decide the properties exposed in an audit.
func (s *Service) trackAuditProperties(id string, before json.RawMessage, current any, properties ...string) error {
	after, err := json.Marshal(current)
	if err != nil {
		return err
	}
	var oldValues, newValues map[string]json.RawMessage
	if err := json.Unmarshal(before, &oldValues); err != nil {
		return err
	}
	if err := json.Unmarshal(after, &newValues); err != nil {
		return err
	}
	if s.auditProperties == nil {
		s.auditProperties = map[string]map[string]bool{}
	}
	if s.auditProperties[id] == nil {
		s.auditProperties[id] = map[string]bool{}
	}
	for _, property := range properties {
		old, latest := oldValues[property], newValues[property]
		if !bytes.Equal(old, latest) && !jsonutil.Equal(old, latest) {
			s.auditProperties[id][property] = true
		}
	}
	return nil
}

func (s *Service) changedProperties(id, action string) []string {
	properties := []string{}
	if strings.HasSuffix(action, ".create") || strings.HasSuffix(action, ".delete") || strings.HasSuffix(action, ".purge") {
		return properties
	}
	if action == "permissions.replace" {
		properties = append(properties, "grants")
	}
	for name := range s.auditProperties[id] {
		properties = append(properties, name)
	}
	sort.Strings(properties)
	return properties
}

func auditEntity(action, kind, resourceID string, details map[string]any) (string, string) {
	prefix := strings.Split(action, ".")[0]
	keys := map[string]string{"term": "term_id", "keyword": "term_id", "term_set": "term_set_id", "term_group": "term_group_id", "relationship": "relationship_id", "relationship_type": "relationship_type_id", "field": "field_id", "content_type": "content_type_id", "view": "view_id", "template": "template_id", "smart_folder": "smart_folder_id", "workflow": "workflow_id", "bulk_update": "operation_id", "webdav_credential": "webdav_credential_id", "operation": "operation_id"}
	if key := keys[prefix]; key != "" {
		if id, ok := details[key].(string); ok && id != "" {
			return prefix, id
		}
	}
	if resourceID != "" {
		return kind, resourceID
	}
	return prefix, ""
}

var resourceAuditProperties = []string{"name", "description", "allow_folders", "keywords", "values", "content_type_id", "parent_id", "inherit_permissions", "versioning", "max_versions", "publishing_enabled", "webdav_enabled", "default_currency", "duplicate_policy"}
