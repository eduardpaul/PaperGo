package dms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"papergo/internal/model"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"papergo/internal/identity"
)

// UUID tokens can also occur inside query expressions and workflow bindings.
var packageUUID = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

var packageDataTables = []string{"blobs", "blob_texts", "blob_previews", "item_revisions", "integration_revision_data", "integration_items", "item_histories", "publications", "item_surfaces", "business_keys", "relationships"}

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
			if key == "options" && !portableDependencyType(fmt.Sprint(v["type"])) {
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
	resources := map[string]map[string]any{}
	selected := map[string]map[string]bool{}
	for _, table := range packageTables {
		selected[table] = map[string]bool{}
	}
	for _, row := range packageMaps(p.Tables["resources"]) {
		resources[fmt.Sprint(row["id"])] = row
	}
	root := resources[p.WorkspaceID]
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
	for _, row := range resources {
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
	for _, table := range []string{"blob_texts", "blob_previews"} {
		for _, row := range packageMaps(p.Tables[table]) {
			if hashes[row["sha256"]] {
				add(table, row)
			}
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
			if resource := resources[id]; resource != nil && !selected["resources"][id] {
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
	for _, table := range packageTables {
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
		for _, table := range packageDataTables {
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
			if key == "options" && !portableReferenceType(fmt.Sprint(v["type"])) {
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
			if table == "field_definitions" && key == "options" && !portableReferenceType(fmt.Sprint(row["type"])) {
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

func (s *Service) packageJSONColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.tx.QueryContext(ctx, "PRAGMA table_info("+quotePackageColumn(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaults any
		if err = rows.Scan(&cid, &name, &kind, &notnull, &defaults, &pk); err != nil {
			return nil, err
		}
		out[name] = strings.EqualFold(kind, "json")
	}
	return out, rows.Err()
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
	for _, table := range packageTables {
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
	var exists bool
	if err := s.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM resources WHERE kind='workspace' AND name=? COLLATE NOCASE AND deleted_at IS NULL)", name).Scan(&exists); err != nil {
		return p, warnings, err
	}
	if exists {
		return p, warnings, invalid("a workspace named " + name + " already exists; choose workspace_name for a separate copy")
	}
	// These natural identities are unique in the destination.
	for _, table := range []string{"identity_users", "identity_groups", "term_groups", "term_sets", "content_types", "blob_texts", "blob_previews"} {
		for _, row := range rows[table] {
			var query string
			var args []any
			switch table {
			case "identity_users":
				query, args = "id=?", []any{row["id"]}
			case "identity_groups", "term_groups":
				query, args = "name=?", []any{row["name"]}
			case "term_sets":
				query, args = "key=?", []any{row["key"]}
			case "content_types":
				if row["template_key"] == nil {
					continue
				}
				query, args = "template_key=?", []any{row["template_key"]}
			case "blob_previews":
				query, args = "sha256=?", []any{row["sha256"]}
			case "blob_texts":
				query, args = "sha256=? AND settings=?", []any{row["sha256"], row["settings"]}
			}
			var id string
			err := s.tx.QueryRowContext(ctx, "SELECT id FROM "+quotePackageColumn(table)+" WHERE "+query, args...).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return p, warnings, err
			}
			reused[table][fmt.Sprint(row["id"])] = true
			if table != "identity_users" {
				ids[fmt.Sprint(row["id"])] = id
			}
			if table == "identity_users" {
				warnings = append(warnings, "Kept existing account and organization roles for "+id+".")
			}
			if table == "content_types" {
				var sourceFields, targetFields []string
				var targetJSON string
				if err = json.Unmarshal([]byte(fmt.Sprint(row["field_ids"])), &sourceFields); err != nil {
					return p, warnings, err
				}
				if err = s.tx.QueryRowContext(ctx, "SELECT field_ids FROM content_types WHERE id=?", id).Scan(&targetJSON); err != nil {
					return p, warnings, err
				}
				if err = json.Unmarshal([]byte(targetJSON), &targetFields); err != nil {
					return p, warnings, err
				}
				for _, sourceID := range sourceFields {
					for _, field := range rows["field_definitions"] {
						if field["id"] != sourceID {
							continue
						}
						for _, targetID := range targetFields {
							var key string
							if err = s.tx.QueryRowContext(ctx, "SELECT key FROM field_definitions WHERE id=?", targetID).Scan(&key); err != nil {
								return p, warnings, err
							}
							if key == field["key"] {
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
		var id string
		err = s.tx.QueryRowContext(ctx, "SELECT id FROM terms WHERE term_set_id=? AND parent_id IS ? AND normalized_name=? AND merged_into_id IS NULL", ids[fmt.Sprint(row["term_set_id"])], parent, row["normalized_name"]).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return p, warnings, err
		}
		ids[fmt.Sprint(row["id"])], reused["terms"][fmt.Sprint(row["id"])] = id, true
	}
	for _, row := range rows["relationship_types"] {
		var exists bool
		if err = s.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM relationship_types WHERE id=?)", ids[fmt.Sprint(row["id"])]).Scan(&exists); err != nil {
			return p, warnings, err
		}
		reused["relationship_types"][fmt.Sprint(row["id"])] = exists
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
		var actual int
		if err = s.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM identity_memberships WHERE group_id=?", ids[source]).Scan(&actual); err != nil {
			return p, warnings, err
		}
		if expected != actual {
			return p, warnings, invalid("existing group membership conflicts with workspace package")
		}
	}

	for _, table := range packageTables {
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
			if table == "relationships" && fmt.Sprint(mapped["directed"]) != "1" && fmt.Sprint(mapped["directed"]) != "true" && fmt.Sprint(mapped["source_id"]) > fmt.Sprint(mapped["target_id"]) {
				mapped["source_id"], mapped["target_id"] = mapped["target_id"], mapped["source_id"]
			}
			if table == "resources" && sourceID == p.WorkspaceID {
				mapped["name"] = name
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
				var exists bool
				if err = s.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_memberships WHERE group_id=? AND principal_type=? AND principal_id=?)", mapped["group_id"], mapped["principal_type"], mapped["principal_id"]).Scan(&exists); err != nil {
					return p, warnings, err
				}
				if !exists {
					return p, warnings, invalid("existing group membership conflicts with workspace package")
				}
				continue
			}
			if table == "group_inboxes" {
				var exists bool
				if err = s.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM group_inboxes WHERE group_id=?)", mapped["group_id"]).Scan(&exists); err != nil {
					return p, warnings, err
				}
				if exists {
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
	columns, expressions, err := s.packageColumns(ctx, table)
	if err != nil {
		return err
	}
	values := make([]any, len(columns))
	args := make([]any, len(columns))
	for i := range values {
		args[i] = &values[i]
	}
	if err = s.tx.QueryRowContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM "+quotePackageColumn(table)+" WHERE id=?", row["id"]).Scan(args...); err != nil {
		return err
	}
	for i, column := range columns {
		if (table == "blob_texts" || table == "blob_previews") && column == "object_key" {
			continue
		}
		if slices.Contains([]string{"id", "created_at", "updated_at", "version", "index_status"}, column) {
			continue
		}
		if bytes, ok := values[i].([]byte); ok {
			values[i] = string(bytes)
		}
		left, right := values[i], row[column]
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
		if field.Type == "lookup" || field.Type == "term" || field.Type == "keywords" {
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
		enabled := fmt.Sprint(row["automatic_enabled"])
		if enabled != "1" && enabled != "true" || row["deleted_at"] != nil || row["state"] != "published" || row["role"] != nil && row["collection_id"] != nil && fmt.Sprint(row["automatic_provider"]) != "true" && fmt.Sprint(row["automatic_provider"]) != "1" {
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

func portableDependencyType(kind string) bool { return portableReferenceType(kind) || kind == "person" }

func portableReferenceType(kind string) bool {
	return kind == "lookup" || kind == "term" || kind == "keywords"
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
	resources, terms, users := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, row := range rows["identity_users"] {
		users[fmt.Sprint(row["id"])] = true
	}
	for _, row := range rows["resources"] {
		resources[fmt.Sprint(row["id"])] = true
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
		if field.Type == "person" {
			known = users
		}
		if field.Type == "lookup" {
			known = resources
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
				if portableReferenceType(field.Type) || field.Type == "person" {
					packageReferences(payload[field.ID], visit, fieldTypes)
				}
			}
			continue
		}
		if table == "field_definitions" && column == "options" && !portableDependencyType(fmt.Sprint(row["type"])) {
			if options, ok := node.(map[string]any); ok {
				delete(options, "default_value")
			}
		}
		packageReferences(node, visit, fieldTypes)
	}
}

// Scope owned tables in SQL: exporting a small workspace must not materialize
// every other workspace's item history and files just to discard them later.
func packageExportFilter(table string, options ExportOptions) (string, []any) {
	data := options.IncludeData == nil || *options.IncludeData
	if !data && slices.Contains(packageDataTables, table) {
		return " WHERE 0", nil
	}
	ws := options.WorkspaceID
	if ws == "" {
		if table == "resources" && !data {
			return " WHERE kind IN ('workspace','list','library')", nil
		}
		if table == "grants" && !data {
			return " WHERE resource_id IN (SELECT id FROM resources WHERE kind IN ('workspace','list','library'))", nil
		}
		if table == "schema_revisions" && !data {
			return " WHERE id IN (SELECT schema_head_id FROM resources)", nil
		}
		return "", nil
	}
	if slices.Contains([]string{"identity_roles", "identity_assignments", "identity_preferences"}, table) {
		return " WHERE 0", nil
	}
	scope := "SELECT id FROM resources WHERE workspace_id=?"
	if !data {
		scope += " AND kind IN ('workspace','list','library')"
	}
	switch table {
	case "resources":
		where := " WHERE workspace_id=?"
		if !data {
			where += " AND kind IN ('workspace','list','library')"
		}
		return where, []any{ws}
	case "collection_types", "schema_revisions", "list_views":
		where := " WHERE container_id IN (" + scope + ")"
		if table == "schema_revisions" && !data {
			where += " AND id IN (SELECT schema_head_id FROM resources)"
		}
		return where, []any{ws}
	case "schema_templates", "smart_folders", "workflows", "workflow_triggers":
		return " WHERE workspace_id=?", []any{ws}
	case "blobs", "item_revisions", "integration_items", "item_histories", "publications", "item_surfaces", "business_keys":
		return " WHERE item_id IN (" + scope + ")", []any{ws}
	case "grants":
		return " WHERE resource_id IN (" + scope + ")", []any{ws}
	case "group_inboxes":
		return " WHERE library_id IN (" + scope + ")", []any{ws}
	case "workflow_versions":
		return " WHERE workflow_id IN (SELECT id FROM workflows WHERE workspace_id=?)", []any{ws}
	case "integration_revision_data":
		return " WHERE revision_id IN (SELECT r.id FROM item_revisions r JOIN resources c ON c.id=r.container_id WHERE c.workspace_id=?)", []any{ws}
	case "blob_texts", "blob_previews":
		return " WHERE sha256 IN (SELECT b.sha256 FROM blobs b JOIN resources r ON r.id=b.item_id WHERE r.workspace_id=?)", []any{ws}
	case "relationships":
		return " WHERE source_id IN (" + scope + ") OR target_id IN (" + scope + ")", []any{ws, ws}
	}
	return "", nil
}

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
		var name string
		err := s.tx.QueryRowContext(ctx, "SELECT name FROM resources WHERE id=?", id).Scan(&name)
		if err == nil {
			return invalid("workspace configuration references excluded resource " + name + "; include its data or use an organization package")
		}
		if !errors.Is(err, sql.ErrNoRows) {
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
		types[field.ID] = field.Type
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
	return kind != "" && kind != "mixed" && !portableReferenceType(kind)
}

func personPackageFilter(node map[string]any, types map[string]string) bool {
	field, _ := node["field"].(string)
	return types[field] == "person" || field == "$created_by" || field == "$modified_by"
}
