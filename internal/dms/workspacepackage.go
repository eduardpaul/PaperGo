package dms

import (
	"context"
	"encoding/json"
	"fmt"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/portable"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/model"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/catalog"
	"papergo/internal/identity"
)

// UUID tokens can also occur inside query expressions and workflow bindings.
var packageUUID = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func packageIDs(table packageTable) []string {
	ids := make([]string, 0, len(table.Rows))
	for _, row := range packageMaps(table) {
		ids = append(ids, fmt.Sprint(row["id"]))
	}
	return ids
}

func filterPackageTable(p *corePackage, name string, keep func(map[string]any) bool) {
	table := p.Tables[name]
	rows := table.Rows
	table.Rows = [][]any{}
	for i, row := range packageMaps(p.Tables[name]) {
		if keep(row) {
			table.Rows = append(table.Rows, rows[i])
		}
	}
	p.Tables[name] = table
}

// Visit JSON values as well as scalar columns. UUIDs embedded in expressions
// are references too; a dependency outside the workspace must never silently
// point at an unrelated object in the destination instance.
func packageReferences(value any, visit func(string), fieldTypes map[string]string) {
	switch v := value.(type) {
	case string:
		visit(v)
		for _, id := range packageUUID.FindAllString(v, -1) {
			visit(id)
		}
		if strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{") {
			var node any
			if json.Unmarshal([]byte(v), &node) == nil {
				packageReferences(node, visit, fieldTypes)
			}
		}
	case map[string]any:
		for key, child := range v {
			if slices.Contains([]string{"name", "label", "title", "description", "choices", "search"}, key) || (key == "value" && literalPackageFilter(v, fieldTypes) && !personPackageFilter(v, fieldTypes)) {
				continue
			}
			if key == "options" && !portableDependencyType(catalog.FieldType(fmt.Sprint(v["type"]))) {
				if options, ok := child.(map[string]any); ok {
					for option, value := range options {
						if option != "default_value" {
							packageReferences(value, visit, fieldTypes)
						}
					}
					continue
				}
			}
			visit(key)
			packageReferences(child, visit, fieldTypes)
		}
	case []any:
		for _, child := range v {
			packageReferences(child, visit, fieldTypes)
		}
	}
}

func selectWorkspacePackage(p *corePackage) error {
	byID := map[string]map[string]any{}
	selected := map[string]map[string]bool{}
	for _, table := range portable.Tables {
		selected[table] = map[string]bool{}
	}
	for _, row := range packageMaps(p.Tables["resources"]) {
		byID[fmt.Sprint(row["id"])] = row
	}
	root := byID[p.WorkspaceID]
	if root == nil || root["kind"] != "workspace" || root["personal_owner_id"] != nil || root["deleted_at"] != nil {
		return invalid("workspace packages require one live, non-personal workspace")
	}
	type record struct {
		table string
		row   map[string]any
	}
	var queue []record
	add := func(table string, row map[string]any) {
		id := fmt.Sprint(row["id"])
		if !selected[table][id] {
			selected[table][id] = true
			queue = append(queue, record{table, row})
		}
	}
	for _, row := range byID {
		if row["workspace_id"] == p.WorkspaceID && (p.IncludeData || row["kind"] == "workspace" || row["kind"] == "list" || row["kind"] == "library") {
			add("resources", row)
		}
	}
	// Owned rows are selected by their owner, never by the incidental appearance
	// of an ID elsewhere. This also excludes personal saved queries.
	owners := map[string]string{"collection_types": "container_id", "schema_revisions": "container_id", "schema_templates": "workspace_id", "list_views": "container_id", "smart_folders": "workspace_id", "blobs": "item_id", "item_revisions": "item_id", "integration_items": "item_id", "item_histories": "item_id", "publications": "item_id", "item_surfaces": "item_id", "business_keys": "item_id", "grants": "resource_id", "workflows": "workspace_id", "workflow_triggers": "workspace_id", "group_inboxes": "library_id"}
	for table, owner := range owners {
		for _, row := range packageMaps(p.Tables[table]) {
			if selected["resources"][fmt.Sprint(row[owner])] && (table != "smart_folders" || row["owner_id"] == nil) {
				add(table, row)
			}
		}
	}
	for _, row := range packageMaps(p.Tables["relationships"]) {
		source, target := selected["resources"][fmt.Sprint(row["source_id"])], selected["resources"][fmt.Sprint(row["target_id"])]
		if source != target {
			return invalid("workspace has a relationship to another workspace; include both endpoints in one organization package")
		}
		if source && target {
			add("relationships", row)
		}
	}
	for _, row := range packageMaps(p.Tables["workflow_versions"]) {
		if selected["workflows"][fmt.Sprint(row["workflow_id"])] {
			add("workflow_versions", row)
		}
	}
	for _, row := range packageMaps(p.Tables["integration_revision_data"]) {
		if selected["item_revisions"][fmt.Sprint(row["revision_id"])] {
			add("integration_revision_data", row)
		}
	}
	hashes := map[any]bool{}
	for _, row := range packageMaps(p.Tables["blobs"]) {
		if selected["blobs"][fmt.Sprint(row["id"])] {
			hashes[row["sha256"]] = true
		}
	}
	for _, row := range packageMaps(p.Tables["blob_previews"]) {
		if hashes[row["sha256"]] {
			add("blob_previews", row)
		}
	}
	dependencies := map[string][]record{}
	for _, table := range []string{"identity_users", "identity_groups", "term_groups", "term_sets", "terms", "relationship_types", "field_definitions", "content_types"} {
		for _, row := range packageMaps(p.Tables[table]) {
			id := fmt.Sprint(row["id"])
			dependencies[id] = append(dependencies[id], record{table, row})
		}
	}
	allRows := map[string][]map[string]any{}
	for table, data := range p.Tables {
		allRows[table] = packageMaps(data)
	}
	valueGraph, err := workspaceValueGraphFor(allRows)
	if err != nil {
		return err
	}
	for at := 0; at < len(queue); at++ {
		entry := queue[at]
		var outside string
		workspaceRowReferences(entry.table, entry.row, valueGraph, func(id string) {
			if resource := byID[id]; resource != nil && !selected["resources"][id] {
				outside = fmt.Sprint(resource["name"])
			}
			for _, dependency := range dependencies[id] {
				add(dependency.table, dependency.row)
			}
		})
		if outside != "" {
			return invalid("workspace configuration references excluded resource " + outside + "; use an organization package or remove the reference")
		}
		if entry.table == "smart_folders" {
			var definition SmartFolderDefinition
			if err := json.Unmarshal([]byte(fmt.Sprint(entry.row["definition"])), &definition); err != nil {
				return err
			}
			for _, row := range allRows["content_types"] {
				if slices.Contains(definition.ContentTypes, fmt.Sprint(row["key"])) {
					add("content_types", row)
				}
			}
		}
		if entry.table == "term_sets" {
			for _, row := range packageMaps(p.Tables["terms"]) {
				if row["term_set_id"] == entry.row["id"] {
					add("terms", row)
				}
			}
		}
		if entry.table == "identity_groups" {
			for _, row := range packageMaps(p.Tables["identity_memberships"]) {
				if row["group_id"] == entry.row["id"] {
					add("identity_memberships", row)
				}
			}
		}
	}
	for _, table := range portable.Tables {
		filterPackageTable(p, table, func(row map[string]any) bool { return selected[table][fmt.Sprint(row["id"])] })
	}
	integrations := p.Integrations[:0]
	for _, integration := range p.Integrations {
		if selected["resources"][integration.ContainerID] {
			integrations = append(integrations, integration)
		}
	}
	p.Integrations = integrations
	p.Organization = map[string]any{}
	return nil
}

// Publications retain their frozen snapshots after revision collection. Only
// links to revisions carried by the package are restored in the copied workspace.
func clearMissingPublicationRevisions(p *corePackage) {
	revisions := map[string]bool{}
	for _, id := range packageIDs(p.Tables["item_revisions"]) {
		revisions[id] = true
	}
	table := p.Tables["publications"]
	column := slices.Index(table.Columns, "revision_id")
	if column < 0 {
		return
	}
	for _, row := range table.Rows {
		if !revisions[fmt.Sprint(row[column])] {
			row[column] = nil
		}
	}
}

func validateWorkspacePackage(p corePackage) error {
	// Run the export selector on a separate graph to reject injected identities,
	// organization roles/preferences, extra workspaces and unowned content.
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	var scoped corePackage
	if err = json.Unmarshal(raw, &scoped); err != nil {
		return err
	}
	if err = selectWorkspacePackage(&scoped); err != nil {
		return err
	}
	if len(p.Organization) != 0 {
		return invalid("workspace packages cannot change the organization profile")
	}
	for table, original := range p.Tables {
		if len(scoped.Tables[table].Rows) != len(original.Rows) {
			return invalid("workspace package contains unrelated records in " + table)
		}
	}
	if !p.IncludeData {
		for _, table := range portable.DataTables {
			if len(p.Tables[table].Rows) != 0 {
				return invalid("configuration package contains content")
			}
		}
	}
	if err := validateWorkspaceReferences(p); err != nil {
		return err
	}
	return validateWorkspaceValues(p)
}

func remapPackageJSON(value any, ids map[string]string, fieldTypes map[string]string) any {
	switch v := value.(type) {
	case string:
		if id, ok := ids[v]; ok {
			return id
		}
		return packageUUID.ReplaceAllStringFunc(v, func(id string) string {
			if target, ok := ids[id]; ok {
				return target
			}
			return id
		})
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = remapPackageJSON(v[i], ids, fieldTypes)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			if slices.Contains([]string{"choices", "name", "label", "title", "description", "search"}, key) || (key == "value" && literalPackageFilter(v, fieldTypes)) {
				out[key] = child
				continue
			}
			if key == "options" && !portableReferenceType(catalog.FieldType(fmt.Sprint(v["type"]))) {
				mapped := remapPackageJSON(child, ids, fieldTypes)
				if options, ok := child.(map[string]any); ok {
					if target, ok := mapped.(map[string]any); ok {
						if value, exists := options["default_value"]; exists {
							target["default_value"] = value
						}
					}
				}
				out[key] = mapped
				continue
			}
			if slices.Contains([]string{"assignees", "recipients"}, key) {
				out[key] = remapPackageRecipients(child, ids)
			} else {
				out[remapPackageJSON(key, ids, fieldTypes).(string)] = remapPackageJSON(child, ids, fieldTypes)
			}
		}
		return out
	}
	return value
}

func remapPackageRow(table string, row map[string]any, ids map[string]string, jsonColumns map[string]bool, fieldTypes map[string]string) (map[string]any, error) {
	out := make(map[string]any, len(row))
	for key, value := range row {
		text, ok := value.(string)
		if ok && jsonColumns[key] {
			var node any
			decoder := json.NewDecoder(strings.NewReader(text))
			decoder.UseNumber()
			if err := decoder.Decode(&node); err != nil {
				return nil, invalid("invalid package JSON in " + key)
			}
			mapped := remapPackageJSON(node, ids, fieldTypes)
			if table == "field_definitions" && key == "options" && !portableReferenceType(catalog.FieldType(fmt.Sprint(row["type"]))) {
				if options, ok := node.(map[string]any); ok {
					if target, ok := mapped.(map[string]any); ok {
						if value, exists := options["default_value"]; exists {
							target["default_value"] = value
						}
					}
				}
			}
			if slices.Contains([]string{"choices", "changed_fields", "columns", "template_keys", "labels", "synonyms"}, key) {
				mapped = node
			}
			raw, err := json.Marshal(mapped)
			if err != nil {
				return nil, err
			}
			value = string(raw)
		} else if ok && key != "external_id" && (key == "id" || strings.HasSuffix(key, "_id")) && table != "identity_users" && !(key == "principal_id" && row["principal_type"] == "user") {
			if id, exists := ids[text]; exists {
				value = id
			}
		}
		out[key] = value
	}
	return out, nil
}

// packageJSONColumns marks the JSON columns of a table.
func (s *Service) packageJSONColumns(ctx context.Context, table string) (map[string]bool, error) {
	cols, err := s.DAL.Portable().Columns(ctx, table)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, c := range cols {
		out[c.Name] = c.Kind == portable.JSON
	}
	return out, nil
}

// Existing directory subjects retain their target account status and profile.
// Shared catalogs are reused only when their configuration agrees; imports
// never overwrite configuration used by a different workspace.
func (s *Service) prepareWorkspacePackage(ctx context.Context, p corePackage, name string) (corePackage, []string, error) {
	var warnings []string
	if err := rebuildWorkspaceTriggers(&p, time.Now().UTC()); err != nil {
		return p, warnings, err
	}
	ids := map[string]string{}
	reused := map[string]map[string]bool{}
	jsonColumns := map[string]map[string]bool{}
	rows := map[string][]map[string]any{}
	for _, table := range portable.Tables {
		reused[table] = map[string]bool{}
		var err error
		jsonColumns[table], err = s.packageJSONColumns(ctx, table)
		if err != nil {
			return p, warnings, err
		}
		rows[table] = packageMaps(p.Tables[table])
		for _, row := range rows[table] {
			id := fmt.Sprint(row["id"])
			if table != "identity_users" && ids[id] == "" {
				ids[id] = uuid.NewString()
			}
		}
	}
	graph, err := workspaceValueGraphFor(rows)
	if err != nil {
		return p, warnings, err
	}
	if name == "" {
		for _, row := range rows["resources"] {
			if row["id"] == p.WorkspaceID {
				name, _ = row["name"].(string)
			}
		}
	}
	if err := validateContainerMetadata(name, ""); err != nil {
		return p, warnings, err
	}
	exists, err := s.DAL.Resources().Exists(ctx, resources.Filter{Kinds: []refs.ResourceKind{refs.KindWorkspace}, FoldedName: nameKey(name), State: resources.Live})
	if err != nil {
		return p, warnings, err
	}
	if exists {
		return p, warnings, invalid("a workspace named " + name + " already exists; choose workspace_name for a separate copy")
	}
	// These natural identities are unique in the destination.
	for _, table := range []string{"identity_users", "identity_groups", "term_groups", "term_sets", "content_types", "blob_previews"} {
		for _, row := range rows[table] {
			var match portable.Row
			switch table {
			case "identity_users":
				match = portable.Row{"id": row["id"]}
			case "identity_groups", "term_groups":
				match = portable.Row{"name": row["name"]}
			case "term_sets":
				match = portable.Row{"key": row["key"]}
			case "content_types":
				if row["template_key"] == nil {
					continue
				}
				match = portable.Row{"template_key": row["template_key"]}
			case "blob_previews":
				match = portable.Row{"sha256": row["sha256"]}
			}
			existing, err := s.DAL.Portable().Find(ctx, table, match)
			if dberr.IsNotFound(err) {
				continue
			}
			if err != nil {
				return p, warnings, err
			}
			id := fmt.Sprint(existing["id"])
			reused[table][fmt.Sprint(row["id"])] = true
			if table != "identity_users" {
				ids[fmt.Sprint(row["id"])] = id
			}
			if table == "identity_users" {
				warnings = append(warnings, "Kept existing account and organization roles for "+id+".")
			}
			if table == "content_types" {
				var sourceFields, targetFields []string
				if err = json.Unmarshal([]byte(fmt.Sprint(row["field_ids"])), &sourceFields); err != nil {
					return p, warnings, err
				}
				if err = json.Unmarshal([]byte(fmt.Sprint(existing["field_ids"])), &targetFields); err != nil {
					return p, warnings, err
				}
				for _, sourceID := range sourceFields {
					for _, field := range rows["field_definitions"] {
						if field["id"] != sourceID {
							continue
						}
						for _, targetID := range targetFields {
							target, err := s.DAL.Portable().Find(ctx, "field_definitions", portable.Row{"id": targetID})
							if err != nil {
								return p, warnings, err
							}
							if target["key"] == field["key"] {
								ids[sourceID], reused["field_definitions"][sourceID] = targetID, true
							}
						}
					}
				}
			}
		}
	}
	// Terms are matched within their remapped set and parent; groups/sets have
	// already been resolved, and parent order makes nested trees portable.
	ordered, err := orderPackageParents(rows["terms"])
	if err != nil {
		return p, warnings, err
	}
	for _, row := range ordered {
		if row["merged_into_id"] != nil {
			continue
		}
		parent := row["parent_id"]
		if parent != nil {
			parent = ids[fmt.Sprint(parent)]
		}
		term, err := s.DAL.Portable().Find(ctx, "terms", portable.Row{"term_set_id": ids[fmt.Sprint(row["term_set_id"])], "parent_id": parent, "normalized_name": row["normalized_name"], "merged_into_id": nil})
		if dberr.IsNotFound(err) {
			continue
		}
		if err != nil {
			return p, warnings, err
		}
		ids[fmt.Sprint(row["id"])], reused["terms"][fmt.Sprint(row["id"])] = fmt.Sprint(term["id"]), true
	}
	for _, row := range rows["relationship_types"] {
		n, err := s.DAL.Portable().Count(ctx, "relationship_types", portable.Row{"id": ids[fmt.Sprint(row["id"])]})
		if err != nil {
			return p, warnings, err
		}
		reused["relationship_types"][fmt.Sprint(row["id"])] = n > 0
	}
	for source, exists := range reused["identity_groups"] {
		if !exists {
			continue
		}
		expected := 0
		for _, member := range rows["identity_memberships"] {
			if member["group_id"] == source {
				expected++
			}
		}
		actual, err := s.DAL.Portable().Count(ctx, "identity_memberships", portable.Row{"group_id": ids[source]})
		if err != nil {
			return p, warnings, err
		}
		if expected != actual {
			return p, warnings, invalid("existing group membership conflicts with workspace package")
		}
	}

	for _, table := range portable.Tables {
		output := p.Tables[table]
		output.Rows = [][]any{}
		for _, row := range rows[table] {
			sourceID := fmt.Sprint(row["id"])
			mapped, err := remapPackageRow(table, row, ids, jsonColumns[table], graph.filterTypes(row["container_id"]))
			if err != nil {
				return p, warnings, err
			}
			if err = preserveWorkspaceValues(table, row, mapped, graph, ids); err != nil {
				return p, warnings, err
			}
			if table == "relationships" && mapped["directed"] != true && fmt.Sprint(mapped["source_id"]) > fmt.Sprint(mapped["target_id"]) {
				mapped["source_id"], mapped["target_id"] = mapped["target_id"], mapped["source_id"]
			}
			if table == "resources" && sourceID == p.WorkspaceID {
				mapped["name"] = name
			}
			if table == "item_surfaces" {
				mapped["folded_name"] = refs.FoldName(fmt.Sprint(mapped["name"]))
			}
			if table == "identity_users" {
				if reused[table][sourceID] {
					continue
				}
				// Source provider sync markers are not configuration of this instance.
				mapped["sync_hash"] = ""
			}
			if reused[table][sourceID] {
				if err = s.compareWorkspaceDependency(ctx, table, mapped, jsonColumns[table]); err != nil {
					return p, warnings, err
				}
				continue
			}
			if table == "identity_memberships" && reused["identity_groups"][fmt.Sprint(row["group_id"])] {
				n, err := s.DAL.Portable().Count(ctx, "identity_memberships", portable.Row{"group_id": mapped["group_id"], "principal_type": mapped["principal_type"], "principal_id": mapped["principal_id"]})
				if err != nil {
					return p, warnings, err
				}
				if n == 0 {
					return p, warnings, invalid("existing group membership conflicts with workspace package")
				}
				continue
			}
			if table == "group_inboxes" {
				n, err := s.DAL.Portable().Count(ctx, "group_inboxes", portable.Row{"group_id": mapped["group_id"]})
				if err != nil {
					return p, warnings, err
				}
				if n > 0 {
					return p, warnings, invalid("an imported group's inbox is already bound to another library")
				}
			}
			values := make([]any, len(output.Columns))
			for i, column := range output.Columns {
				values[i] = mapped[column]
			}
			output.Rows = append(output.Rows, values)
		}
		p.Tables[table] = output
	}
	// New subjects receive the ordinary Member role, never source organization
	// administration. Existing subjects' assignments are entirely untouched.
	assignments := p.Tables["identity_assignments"]
	for _, user := range packageMaps(p.Tables["identity_users"]) {
		if user["deleted_at"] != nil {
			continue
		}
		row := map[string]any{"id": uuid.NewString(), "created_at": user["created_at"], "role_id": identity.MemberRole, "principal_type": "user", "principal_id": user["id"]}
		values := make([]any, len(assignments.Columns))
		for i, column := range assignments.Columns {
			values[i] = row[column]
		}
		assignments.Rows = append(assignments.Rows, values)
	}
	p.Tables["identity_assignments"] = assignments
	p.WorkspaceID = ids[p.WorkspaceID]
	for i := range p.Integrations {
		if target, ok := ids[p.Integrations[i].ContainerID]; ok {
			p.Integrations[i].ContainerID = target
		}
	}
	return p, warnings, nil
}

func (s *Service) compareWorkspaceDependency(ctx context.Context, table string, row map[string]any, jsonColumns map[string]bool) error {
	existing, err := s.DAL.Portable().Find(ctx, table, portable.Row{"id": row["id"]})
	if err != nil {
		return err
	}
	columns, err := s.packageColumns(ctx, table)
	if err != nil {
		return err
	}
	for _, column := range columns {
		if table == "blob_previews" && column == "object_key" {
			continue
		}
		if slices.Contains([]string{"id", "created_at", "updated_at", "version", "index_status"}, column) {
			continue
		}
		left, right := existing[column], row[column]
		if jsonColumns[column] {
			var a, b any
			da, db := json.NewDecoder(strings.NewReader(fmt.Sprint(left))), json.NewDecoder(strings.NewReader(fmt.Sprint(right)))
			da.UseNumber()
			db.UseNumber()
			if da.Decode(&a) != nil || db.Decode(&b) != nil {
				return invalid("invalid shared dependency JSON")
			}
			left, right = a, b
		}
		if !reflect.DeepEqual(left, right) && fmt.Sprint(left) != fmt.Sprint(right) {
			return invalid("workspace dependency conflicts with existing " + table + " " + fmt.Sprint(row["id"]) + " (" + column + ")")
		}
	}
	return nil
}

func remapPackageRecipients(value any, ids map[string]string) any {
	switch v := value.(type) {
	case string:
		if strings.HasPrefix(v, "group:") {
			return remapPackageJSON(v, ids, nil)
		}
	case []any:
		for i := range v {
			v[i] = remapPackageRecipients(v[i], ids)
		}
	}
	return value
}

// Item data is remapped according to the frozen schema, so a text or person
// field whose value happens to equal an entity UUID remains ordinary data.
func preserveWorkspaceValues(table string, source, mapped map[string]any, graph workspaceValueGraph, ids map[string]string) error {
	column := ""
	var revision map[string]any
	switch table {
	case "resources":
		if source["kind"] != "item" && source["kind"] != "folder" {
			return nil
		}
		column = "values"
		revision = graph.revisions[fmt.Sprint(source["head_revision_id"])]
	case "item_revisions":
		column = "payload"
		revision = source
	case "item_surfaces", "publications":
		revision = graph.revisions[fmt.Sprint(source["revision_id"])]
		if table == "item_surfaces" {
			column = "payload"
		} else {
			column = "snapshot"
		}
	case "integration_revision_data":
		mapped["data"] = source["data"]
		return nil
	default:
		return nil
	}
	values, err := decodeValues(json.RawMessage(fmt.Sprint(source[column])))
	if err != nil {
		return err
	}
	payload := values
	if table == "publications" {
		if nested, ok := values["values"].(map[string]any); ok {
			payload = nested
		}
	}
	var schema SchemaDefinition
	if table == "publications" {
		schema = graph.schemas[fmt.Sprint(values["schema_revision_id"])]
	} else if revision != nil {
		schema = graph.schemas[fmt.Sprint(revision["schema_revision_id"])]
	}
	for _, field := range schema.Fields {
		if field.Type == catalog.FieldLookup || field.Type == catalog.FieldTerm || field.Type == catalog.FieldKeywords {
			if value, ok := payload[field.ID]; ok {
				payload[field.ID] = remapPackageJSON(value, ids, nil)
			}
		}
	}
	if table == "publications" {
		// Snapshot keyword IDs and other entity references follow the imported graph.
		values["keywords"] = remapPackageJSON(values["keywords"], ids, nil)
		for _, key := range []string{"id", "revision_id", "schema_revision_id", "blob_id"} {
			if id, ok := values[key].(string); ok && ids[id] != "" {
				values[key] = ids[id]
			}
		}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	mapped[column] = string(raw)
	return nil
}

// SQL checks foreign keys when applying; this checks the references carried
// inside JSON before any target IDs can accidentally satisfy an outside link.
func validateWorkspaceReferences(p corePackage) error {
	known := map[string]bool{}
	for table, data := range p.Tables {
		if table != "identity_users" {
			for _, id := range packageIDs(data) {
				known[id] = true
			}
		}
	}
	referenceKeys := []string{"lookup_container_id", "term_set_id", "definition_id", "collection_id", "container_id", "workspace_id", "content_type_id", "schema_revision_id", "item_id", "workflow_id", "blob_id", "task_id", "run_id"}
	var walk func(any, string) error
	walk = func(value any, key string) error {
		switch v := value.(type) {
		case map[string]any:
			for childKey, child := range v {
				if err := walk(child, childKey); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := walk(child, key); err != nil {
					return err
				}
			}
		case string:
			if slices.Contains(referenceKeys, key) || key == "field_ids" || key == "terms" || key == "keywords" || key == "keyword" {
				if _, err := uuid.Parse(v); err == nil && !known[v] {
					return invalid("workspace package has an unresolved reference in " + key)
				}
			}
			if (key == "assignees" || key == "recipients") && strings.HasPrefix(v, "group:") && !known[strings.TrimPrefix(v, "group:")] {
				return invalid("workspace package has an unresolved group recipient")
			}
		}
		return nil
	}
	for table, data := range p.Tables {
		for _, row := range packageMaps(data) {
			for column, value := range row {
				if table == "integration_revision_data" && column == "data" {
					continue
				}
				if text, ok := value.(string); ok && (strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[")) {
					var node any
					if json.Unmarshal([]byte(text), &node) == nil {
						if err := walk(node, column); err != nil {
							return err
						}
					}
				}
				if column != "id" && column != "external_id" && strings.HasSuffix(column, "_id") && value != nil && !(column == "principal_id" && row["principal_type"] == "user") && column != "owner_id" && column != "personal_owner_id" && table != "identity_users" {
					id, ok := value.(string)
					if !ok || !known[id] {
						return invalid("workspace package has an unresolved reference in " + table + "." + column)
					}
				}
			}
		}
	}
	return nil
}

type workspaceValueGraph struct {
	fieldTypes      map[string]string
	resourceSchemas map[string]string
	revisions       map[string]map[string]any
	schemas         map[string]SchemaDefinition
}

func workspaceValueGraphFor(rows map[string][]map[string]any) (workspaceValueGraph, error) {
	graph := workspaceValueGraph{fieldTypes: map[string]string{}, resourceSchemas: map[string]string{}, revisions: map[string]map[string]any{}, schemas: map[string]SchemaDefinition{}}
	for _, row := range rows["resources"] {
		graph.resourceSchemas[fmt.Sprint(row["id"])] = fmt.Sprint(row["schema_head_id"])
	}
	for _, row := range rows["field_definitions"] {
		key, kind := fmt.Sprint(row["key"]), fmt.Sprint(row["type"])
		if prior := graph.fieldTypes[key]; prior != "" && prior != kind {
			kind = "mixed"
		}
		graph.fieldTypes[key] = kind
	}
	for _, row := range rows["item_revisions"] {
		graph.revisions[fmt.Sprint(row["id"])] = row
	}
	for _, row := range rows["schema_revisions"] {
		var schema SchemaDefinition
		if err := json.Unmarshal([]byte(fmt.Sprint(row["definition"])), &schema); err != nil {
			return graph, err
		}
		graph.schemas[fmt.Sprint(row["id"])] = schema
	}
	return graph, nil
}

// The trigger index is derived from published definitions with automatic use on. Schedule
// cursors start at import time, so a copy never replays the source's overdue run.
func rebuildWorkspaceTriggers(p *corePackage, now time.Time) error {
	versions := map[string]map[string]any{}
	for _, row := range packageMaps(p.Tables["workflow_versions"]) {
		versions[fmt.Sprint(row["workflow_id"])+":"+fmt.Sprint(row["number"])] = row
	}
	table := p.Tables["workflow_triggers"]
	table.Rows = [][]any{}
	for _, row := range packageMaps(p.Tables["workflows"]) {
		if row["automatic_enabled"] != true || row["deleted_at"] != nil || row["state"] != "published" || row["role"] != nil && row["collection_id"] != nil && row["automatic_provider"] != true {
			continue
		}
		if row["role"] != nil && row["collection_id"] == nil {
			return invalid("automatic role providers require a specific list or library")
		}
		version := versions[fmt.Sprint(row["id"])+":"+fmt.Sprint(row["current_version"])]
		if version == nil {
			return invalid("workspace workflow has no current definition")
		}
		var definition struct {
			Triggers []struct {
				Disabled     bool   `json:"disabled"`
				Type         string `json:"type"`
				CollectionID string `json:"collection_id"`
				Cron         string `json:"cron"`
				TimeZone     string `json:"time_zone"`
			} `json:"triggers"`
		}
		if err := json.Unmarshal([]byte(fmt.Sprint(version["definition"])), &definition); err != nil {
			return invalid("invalid workspace workflow definition")
		}
		for position, trigger := range definition.Triggers {
			if trigger.Type == "manual" || trigger.Disabled {
				continue
			}
			if trigger.CollectionID == "" && (strings.HasPrefix(trigger.Type, "file.") || strings.HasPrefix(trigger.Type, "item.") || strings.HasPrefix(trigger.Type, "folder.") || trigger.Type == EventPermissionsChanged) {
				return invalid("automatic content processing requires a specific list or library")
			}
			tr := map[string]any{"id": uuid.NewString(), "created_at": row["created_at"], "workflow_id": row["id"], "workspace_id": row["workspace_id"], "collection_id": nil, "type": trigger.Type, "position": int64(position), "next_at": nil}
			if trigger.CollectionID != "" {
				tr["collection_id"] = trigger.CollectionID
			}
			if trigger.Type == "schedule" {
				schedule, err := model.WorkflowSchedule(trigger.Cron, trigger.TimeZone)
				if err != nil {
					return invalid(err.Error())
				}
				next := schedule.Next(now).UTC()
				if next.IsZero() {
					return invalid("workspace schedule has no next occurrence")
				}
				tr["next_at"] = next.Format(time.RFC3339Nano)
			}
			values := make([]any, len(table.Columns))
			for i, col := range table.Columns {
				values[i] = tr[col]
			}
			table.Rows = append(table.Rows, values)
		}
	}
	p.Tables["workflow_triggers"] = table
	return nil
}

func portableDependencyType(kind catalog.FieldType) bool {
	return portableReferenceType(catalog.FieldType(kind)) || kind == catalog.FieldPerson
}

func portableReferenceType(kind catalog.FieldType) bool {
	return kind == catalog.FieldLookup || kind.Terms()
}

func validateWorkspaceValues(p corePackage) error {
	rows := map[string][]map[string]any{}
	for table, data := range p.Tables {
		rows[table] = packageMaps(data)
	}
	graph, err := workspaceValueGraphFor(rows)
	if err != nil {
		return err
	}
	knownResources, terms, users := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, row := range rows["identity_users"] {
		users[fmt.Sprint(row["id"])] = true
	}
	for _, row := range rows["resources"] {
		knownResources[fmt.Sprint(row["id"])] = true
	}
	for _, row := range rows["terms"] {
		terms[fmt.Sprint(row["id"])] = true
	}
	var check func(any, map[string]bool) error
	check = func(value any, known map[string]bool) error {
		switch v := value.(type) {
		case nil:
			return nil
		case string:
			if v != "" && !known[v] {
				return invalid("workspace item/default references data outside the package")
			}
		case []any:
			for _, child := range v {
				if err := check(child, known); err != nil {
					return err
				}
			}
		default:
			return invalid("invalid portable item reference")
		}
		return nil
	}
	checkField := func(field SchemaField, value any) error {
		if !portableDependencyType(field.Type) {
			return nil
		}
		known := terms
		if field.Type == catalog.FieldPerson {
			known = users
		}
		if field.Type == catalog.FieldLookup {
			known = knownResources
		}
		return check(value, known)
	}
	for _, table := range []string{"resources", "item_revisions", "item_surfaces"} {
		for _, row := range rows[table] {
			var ids any
			if err := json.Unmarshal([]byte(fmt.Sprint(row["keywords"])), &ids); err != nil {
				return err
			}
			if err := check(ids, terms); err != nil {
				return err
			}
		}
	}
	for _, schema := range graph.schemas {
		for _, field := range schema.Fields {
			if len(field.Options.DefaultValue) > 0 && portableDependencyType(field.Type) {
				var value any
				if err := json.Unmarshal(field.Options.DefaultValue, &value); err != nil {
					return err
				}
				if err := checkField(field, value); err != nil {
					return err
				}
			}
		}
	}
	for _, table := range []string{"item_revisions", "publications"} {
		for _, row := range rows[table] {
			column := "payload"
			if table == "publications" {
				column = "snapshot"
			}
			values, err := decodeValues(json.RawMessage(fmt.Sprint(row[column])))
			if err != nil {
				return err
			}
			schemaID := row["schema_revision_id"]
			if table == "publications" {
				if err := check(values["keywords"], terms); err != nil {
					return err
				}
				schemaID = values["schema_revision_id"]
				values, _ = values["values"].(map[string]any)
			}
			for _, field := range graph.schemas[fmt.Sprint(schemaID)].Fields {
				if err := checkField(field, values[field.ID]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func workspaceRowReferences(table string, row map[string]any, graph workspaceValueGraph, visit func(string)) {
	fieldTypes := graph.filterTypes(row["container_id"])
	for column, value := range row {
		if column == "external_id" {
			continue
		}
		if column == "id" || strings.HasSuffix(column, "_id") || strings.HasSuffix(column, "_by") {
			packageReferences(value, visit, fieldTypes)
			continue
		}
		text, ok := value.(string)
		if !ok || !(strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[")) {
			continue
		}
		if slices.Contains([]string{"choices", "changed_fields", "columns", "template_keys", "models", "labels", "synonyms"}, column) || table == "integration_revision_data" {
			continue
		}
		var node any
		if json.Unmarshal([]byte(text), &node) != nil {
			continue
		}
		if (column == "values" && table == "resources") || (column == "payload" && (table == "item_revisions" || table == "item_surfaces")) || (column == "snapshot" && table == "publications") {
			revision := row
			if table == "resources" {
				revision = graph.revisions[fmt.Sprint(row["head_revision_id"])]
			} else if table != "item_revisions" {
				revision = graph.revisions[fmt.Sprint(row["revision_id"])]
			}
			payload, ok := node.(map[string]any)
			if !ok {
				continue
			}
			var schema SchemaDefinition
			if table == "publications" {
				schema = graph.schemas[fmt.Sprint(payload["schema_revision_id"])]
				packageReferences(payload["keywords"], visit, fieldTypes)
				packageReferences(payload["schema_revision_id"], visit, fieldTypes)
				payload, _ = payload["values"].(map[string]any)
			} else if revision != nil {
				schema = graph.schemas[fmt.Sprint(revision["schema_revision_id"])]
			}
			for _, field := range schema.Fields {
				if portableReferenceType(field.Type) || field.Type == catalog.FieldPerson {
					packageReferences(payload[field.ID], visit, fieldTypes)
				}
			}
			continue
		}
		if table == "field_definitions" && column == "options" && !portableDependencyType(catalog.FieldType(fmt.Sprint(row["type"]))) {
			if options, ok := node.(map[string]any); ok {
				delete(options, "default_value")
			}
		}
		packageReferences(node, visit, fieldTypes)
	}
}

// Scope owned tables in SQL: exporting a small workspace must not materialize
// every other workspace's item history and files just to discard them later.
func (s *Service) validateWorkspaceSourceReferences(ctx context.Context, p corePackage) error {
	known := map[string]bool{}
	rows := map[string][]map[string]any{}
	for table, data := range p.Tables {
		rows[table] = packageMaps(data)
		for _, id := range packageIDs(data) {
			known[id] = true
		}
	}
	graph, err := workspaceValueGraphFor(rows)
	if err != nil {
		return err
	}
	candidates := map[string]bool{}
	for table, entries := range rows {
		if table == "identity_preferences" {
			continue
		}
		for _, row := range entries {
			workspaceRowReferences(table, row, graph, func(id string) {
				if _, err := uuid.Parse(id); err == nil && !known[id] {
					candidates[id] = true
				}
			})
		}
	}
	for id := range candidates {
		r, err := s.DAL.Resources().Resource(ctx, id)
		if err == nil {
			return invalid("workspace configuration references excluded resource " + r.Name + "; include its data or use an organization package")
		}
		if !dberr.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (g workspaceValueGraph) filterTypes(container any) map[string]string {
	schema, ok := g.schemas[g.resourceSchemas[fmt.Sprint(container)]]
	if !ok {
		return g.fieldTypes
	}
	types := map[string]string{}
	for _, field := range schema.Fields {
		types[field.ID] = string(field.Type)
	}
	return types
}

func literalPackageFilter(node map[string]any, types map[string]string) bool {
	field, ok := node["field"].(string)
	if !ok || node["op"] == nil {
		return false
	}
	if strings.HasPrefix(field, "$") {
		return !slices.Contains([]string{"$id", "$keywords", "$parent_id", "$container_id", "$workspace_id", "$content_type_id"}, field)
	}
	kind := types[field]
	return kind != "" && kind != "mixed" && !portableReferenceType(catalog.FieldType(kind))
}

func personPackageFilter(node map[string]any, types map[string]string) bool {
	field, _ := node["field"].(string)
	return types[field] == "person" || field == "$created_by" || field == "$modified_by" || field == "$archived_by"
}
