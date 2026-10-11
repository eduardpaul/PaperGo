package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"papergo/ent"
	"reflect"
	"sort"
	"strings"
)

// Capture changed property names inside the transaction. Technical stamps and
// retained revision storage are excluded; audit records contain no old values.
func (s *Service) captureAuditProperties() {
	s.auditProperties = map[string]map[string]bool{}
	s.Client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Type() == "Resource" && m.Op().Is(ent.OpUpdate) {
				ids, err := m.(interface {
					IDs(context.Context) ([]string, error)
				}).IDs(ctx)
				if err != nil {
					return nil, err
				}
				before := map[string]map[string]any{}
				for _, id := range ids {
					row, e := s.Client.Resource.Get(ctx, id)
					if e != nil {
						return nil, e
					}
					fields := map[string]any{}
					typ := reflect.TypeOf(*row)
					value := reflect.ValueOf(*row)
					for i := 0; i < typ.NumField(); i++ {
						name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
						if name != "" && name != "-" {
							fields[name] = value.Field(i).Interface()
						}
					}
					before[id] = fields
				}
				value, err := next.Mutate(ctx, m)
				if err != nil {
					return value, err
				}
				for _, id := range ids {
					if s.auditProperties[id] == nil {
						s.auditProperties[id] = map[string]bool{}
					}
					for _, field := range append(m.Fields(), m.ClearedFields()...) {
						switch field {
						case "version", "created_at", "created_by", "updated_at", "updated_by", "next_revision_number", "head_revision_id", "index_status", "name_key":
							continue
						}
						current, _ := m.Field(field)
						old, _ := json.Marshal(before[id][field])
						latest, _ := json.Marshal(current)
						if !bytes.Equal(old, latest) {
							s.auditProperties[id][field] = true
						}
					}
				}
				return value, nil
			}
			switch m.Type() {
			case "Resource", "Relationship", "FieldDefinition", "ContentType", "Term", "TermSet", "TermGroup", "ListView", "SchemaTemplate", "SmartFolder", "Workflow", "WebDAVCredential":
			default:
				return next.Mutate(ctx, m)
			}
			ids, ok := m.(interface{ ID() (string, bool) })
			if !ok || m.Type() == "AuditEvent" {
				return next.Mutate(ctx, m)
			}
			id, _ := ids.ID()
			old := map[string]any{}
			if m.Op().Is(ent.OpUpdateOne) {
				for _, field := range m.Fields() {
					if value, err := m.OldField(ctx, field); err == nil {
						old[field] = value
					}
				}
			}
			value, err := next.Mutate(ctx, m)
			if err != nil {
				return value, err
			}
			if id == "" {
				id, _ = ids.ID()
			}
			if id == "" {
				return value, nil
			}
			if s.auditProperties[id] == nil {
				s.auditProperties[id] = map[string]bool{}
			}
			for _, field := range append(m.Fields(), m.ClearedFields()...) {
				switch field {
				case "version", "created_at", "created_by", "updated_at", "updated_by", "next_revision_number", "head_revision_id", "published_revision_id", "index_status":
					continue
				}
				current, present := m.Field(field)
				previous, known := old[field]
				if known && present && reflect.DeepEqual(previous, current) {
					continue
				}
				s.auditProperties[id][field] = true
			}
			// A revision pointer represents a content edit on its owning item.
			if m.Type() == "Resource" && m.Op().Is(ent.OpUpdateOne) {
				if head, changed := m.Field("head_revision_id"); changed {
					latest, e := s.Client.ItemRevision.Get(ctx, head.(string))
					if e != nil {
						return value, e
					}
					oldID, _ := old["head_revision_id"].(string)
					if oldPointer, ok := old["head_revision_id"].(*string); ok && oldPointer != nil {
						oldID = *oldPointer
					}
					prior, e := s.Client.ItemRevision.Get(ctx, oldID)
					if e != nil && !ent.IsNotFound(e) {
						return value, e
					}
					if prior == nil || prior.Name != latest.Name {
						s.auditProperties[id]["name"] = true
					}
					if prior == nil || !bytes.Equal(prior.Payload, latest.Payload) {
						s.auditProperties[id]["values"] = true
					}
					if prior == nil || !reflect.DeepEqual(prior.BlobID, latest.BlobID) {
						s.auditProperties[id]["blob_id"] = true
					}
					if prior == nil || !reflect.DeepEqual(prior.Tags, latest.Tags) {
						s.auditProperties[id]["tags"] = true
					}
					if prior == nil || prior.ContentTypeID != latest.ContentTypeID {
						s.auditProperties[id]["content_type_id"] = true
					}
				}
			}
			return value, nil
		})
	})
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

func auditEntity(action string, r *ent.Resource, details map[string]any) (string, string) {
	prefix := strings.Split(action, ".")[0]
	keys := map[string]string{"term": "term_id", "keyword": "term_id", "term_set": "term_set_id", "term_group": "term_group_id", "relationship": "relationship_id", "relationship_type": "relationship_type_id", "field": "field_id", "content_type": "content_type_id", "view": "view_id", "template": "template_id", "smart_folder": "smart_folder_id", "workflow": "workflow_id", "bulk_update": "operation_id", "webdav_credential": "webdav_credential_id", "operation": "operation_id"}
	if key := keys[prefix]; key != "" {
		if id, ok := details[key].(string); ok && id != "" {
			return prefix, id
		}
	}
	if r != nil {
		return string(r.Kind), r.ID
	}
	return prefix, ""
}
