package dms

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/operation"
	"papergo/internal/identity"
	"papergo/internal/storage"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const PackageLimit int64 = 4 << 30
const PackageManifestLimit int64 = 64 << 20
const PackageRetention = 7 * 24 * time.Hour
const OperationKindExport = "portability.export"
const OperationKindImport = "portability.import"

// This catalog is the portable application graph. Search/tag projections are
// rebuilt by database triggers. Credentials, audit, cursors and execution state
// belong to this installation and are deliberately absent.
var packageTables = []string{
	"identity_users", "identity_groups", "identity_roles", "identity_memberships", "identity_assignments", "identity_preferences",
	"term_groups", "term_sets", "terms", "relationship_types", "field_definitions", "content_types",
	"resources", "collection_types", "schema_revisions", "schema_templates", "list_views", "smart_folders",
	"blobs", "item_revisions", "item_histories", "publications", "item_surfaces", "business_keys", "grants", "relationships",
	"workflows", "workflow_versions", "workflow_triggers",
}

type packageTable struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}
type packageObject struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type corePackage struct {
	Format       string                  `json:"format"`
	Organization map[string]any          `json:"organization"`
	Tables       map[string]packageTable `json:"tables"`
	Objects      []packageObject         `json:"objects"`
}
type packagePayload struct {
	ObjectKey string `json:"object_key,omitempty"`
	DryRun    bool   `json:"dry_run,omitempty"`
}
type PackageResult struct {
	Valid      bool       `json:"valid"`
	DryRun     bool       `json:"dry_run,omitempty"`
	Rows       int        `json:"rows,omitempty"`
	Objects    int        `json:"objects,omitempty"`
	Errors     []string   `json:"errors,omitempty"`
	ObjectKey  string     `json:"object_key,omitempty"`
	Size       int64      `json:"size,omitempty"`
	SHA256     string     `json:"sha256,omitempty"`
	PackageURL string     `json:"package_url,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// RegisterPortability wires storage into the durable handlers before workers start.
func (s *Service) RegisterPortability(store storage.Store) error {
	validate := func(ctx context.Context, t *Service, subject, container string, raw json.RawMessage) error {
		if container != "" {
			return invalid("core packages are organization-wide")
		}
		var in packagePayload
		if err := json.Unmarshal(raw, &in); err != nil {
			return invalid("invalid package request")
		}
		if in.ObjectKey != "" {
			if _, err := uuid.Parse(in.ObjectKey); err != nil {
				return invalid("invalid package object")
			}
		}
		return nil
	}
	if err := s.RegisterOperationHandler(OperationKindExport, OperationHandler{Scope: "organization.manage", Validate: validate, Step: func(ctx context.Context, t *Service, op *ent.Operation) error {
		obj, err := t.exportCorePackage(ctx, store)
		if err != nil {
			return err
		}
		expires := time.Now().UTC().Add(PackageRetention)
		return t.finishPackage(ctx, op, PackageResult{Valid: true, ObjectKey: obj.Key, Size: obj.Size, SHA256: obj.SHA256, PackageURL: "/v1/portability/exports/" + op.ID + "/package", ExpiresAt: &expires})
	}}); err != nil {
		return err
	}
	return s.RegisterOperationHandler(OperationKindImport, OperationHandler{Scope: "organization.manage", Validate: validate, Step: func(ctx context.Context, t *Service, op *ent.Operation) error {
		var in packagePayload
		if err := json.Unmarshal(op.Payload, &in); err != nil {
			return err
		}
		result, err := t.importCorePackage(ctx, store, op.CreatedBy, in)
		if err != nil {
			return err
		}
		return t.finishPackage(ctx, op, result)
	}})
}
func (s *Service) finishPackage(ctx context.Context, op *ent.Operation, result PackageResult) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return s.Client.Operation.UpdateOne(op).SetResult(raw).SetStatus(operation.StatusSucceeded).SetProgress(100).SetCompletedAt(time.Now().UTC()).Exec(ctx)
}
func (s *Service) StartExport(ctx context.Context, subject string) (*ent.Operation, error) {
	return s.EnqueueOperation(ctx, subject, "", OperationKindExport, json.RawMessage(`{}`))
}
func (s *Service) StartImport(ctx context.Context, subject, objectKey string, dryRun bool) (*ent.Operation, error) {
	raw, _ := json.Marshal(packagePayload{objectKey, dryRun})
	return s.EnqueueOperation(ctx, subject, "", OperationKindImport, raw)
}

func quotePackageColumn(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
func (s *Service) packageColumns(ctx context.Context, table string) ([]string, []string, error) {
	rows, err := s.tx.QueryContext(ctx, "PRAGMA table_info("+quotePackageColumn(table)+")")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var columns, expressions []string
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaults any
		if err = rows.Scan(&cid, &name, &kind, &notnull, &defaults, &pk); err != nil {
			return nil, nil, err
		}
		columns = append(columns, name)
		expression := quotePackageColumn(name)
		if strings.Contains(strings.ToUpper(kind), "DATE") {
			expression = "CAST(" + expression + " AS TEXT)"
		}
		expressions = append(expressions, expression)
	}
	return columns, expressions, rows.Err()
}
func (s *Service) exportCorePackage(ctx context.Context, store storage.Store) (storage.Object, error) {
	p := corePackage{Format: "papergo-core", Tables: map[string]packageTable{}, Objects: []packageObject{}, Organization: map[string]any{}}
	var identifier, name, description string
	if err := s.tx.QueryRowContext(ctx, "SELECT identifier,display_name,description FROM identity_organizations WHERE singleton=1").Scan(&identifier, &name, &description); err != nil {
		return storage.Object{}, err
	}
	p.Organization = map[string]any{"identifier": identifier, "display_name": name, "description": description}
	count := 0
	for _, table := range packageTables {
		columns, expressions, err := s.packageColumns(ctx, table)
		if err != nil {
			return storage.Object{}, err
		}
		rows, err := s.tx.QueryContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM "+quotePackageColumn(table)+" ORDER BY id")
		if err != nil {
			return storage.Object{}, err
		}
		data := packageTable{Columns: columns, Rows: [][]any{}}
		for rows.Next() {
			values := make([]any, len(columns))
			args := make([]any, len(columns))
			for i := range values {
				args[i] = &values[i]
			}
			if err = rows.Scan(args...); err != nil {
				rows.Close()
				return storage.Object{}, err
			}
			for i, v := range values {
				if b, ok := v.([]byte); ok {
					values[i] = string(b)
				}
			}
			data.Rows = append(data.Rows, values)
			count++
			if count > 100000 {
				rows.Close()
				return storage.Object{}, invalid("package exceeds 100000 core records")
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return storage.Object{}, err
		}
		p.Tables[table] = data
	}
	seen := map[string]bool{}
	for _, row := range packageMaps(p.Tables["blobs"]) {
		key := row["object_key"].(string)
		if !seen[key] {
			seen[key] = true
			p.Objects = append(p.Objects, packageObject{key, row["size"].(int64), row["sha256"].(string)})
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return storage.Object{}, err
	}
	if int64(len(raw)) > PackageManifestLimit {
		return storage.Object{}, invalid("package manifest exceeds 64 MiB")
	}
	f, err := os.CreateTemp("", "papergo-export-*.zip")
	if err != nil {
		return storage.Object{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	z := zip.NewWriter(f)
	entry, err := z.Create("manifest.json")
	if err != nil {
		return storage.Object{}, err
	}
	if _, err = entry.Write(raw); err != nil {
		return storage.Object{}, err
	}
	var total int64
	for _, obj := range p.Objects {
		total += obj.Size
		if total > PackageLimit {
			return storage.Object{}, storage.ErrTooLarge
		}
		input, err := store.Open(ctx, obj.Key)
		if err != nil {
			return storage.Object{}, err
		}
		entry, err = z.Create("objects/" + obj.Key)
		if err != nil {
			input.Close()
			return storage.Object{}, err
		}
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(entry, h), io.LimitReader(input, obj.Size+1))
		input.Close()
		if e != nil {
			return storage.Object{}, e
		}
		if n != obj.Size || hex.EncodeToString(h.Sum(nil)) != obj.SHA256 {
			return storage.Object{}, invalid("stored package object failed integrity verification")
		}
	}
	if err = z.Close(); err != nil {
		return storage.Object{}, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return storage.Object{}, err
	}
	return store.Put(ctx, f, PackageLimit)
}
func packageMaps(table packageTable) []map[string]any {
	rows := make([]map[string]any, len(table.Rows))
	for i, values := range table.Rows {
		row := map[string]any{}
		for j, key := range table.Columns {
			if j < len(values) {
				row[key] = values[j]
			}
		}
		rows[i] = row
	}
	return rows
}

func (s *Service) importCorePackage(ctx context.Context, store storage.Store, subject string, in packagePayload) (PackageResult, error) {
	result := PackageResult{DryRun: in.DryRun}
	file, err := store.Open(ctx, in.ObjectKey)
	if err != nil {
		return result, err
	}
	defer file.Close()
	size, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return result, err
	}
	if size > PackageLimit {
		return result, storage.ErrTooLarge
	}
	// The storage contract is seekable; spooling avoids assuming ReaderAt.
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	spool, err := os.CreateTemp("", "papergo-import-*.zip")
	if err != nil {
		return result, err
	}
	defer os.Remove(spool.Name())
	defer spool.Close()
	if _, err = io.Copy(spool, file); err != nil {
		return result, err
	}
	z, err := zip.NewReader(spool, size)
	if err != nil {
		return result, invalid("invalid ZIP package")
	}
	entries := map[string]*zip.File{}
	var total uint64
	for _, entry := range z.File {
		if entries[entry.Name] != nil || entry.FileInfo().IsDir() || entry.Mode()&os.ModeSymlink != 0 {
			return result, invalid("duplicate or invalid package entry")
		}
		entries[entry.Name] = entry
		if entry.UncompressedSize64 > uint64(PackageLimit) {
			return result, storage.ErrTooLarge
		}
		total += entry.UncompressedSize64
		if total > uint64(PackageLimit)+uint64(PackageManifestLimit) {
			return result, storage.ErrTooLarge
		}
	}
	manifest := entries["manifest.json"]
	if manifest == nil || manifest.UncompressedSize64 > uint64(PackageManifestLimit) {
		return result, invalid("package manifest missing or too large")
	}
	reader, err := manifest.Open()
	if err != nil {
		return result, err
	}
	dec := json.NewDecoder(io.LimitReader(reader, PackageManifestLimit+1))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var p corePackage
	err = dec.Decode(&p)
	if err == nil && dec.Decode(new(any)) != io.EOF {
		err = invalid("unexpected data after package manifest")
	}
	reader.Close()
	if err != nil || p.Format != "papergo-core" || len(p.Tables) != len(packageTables) {
		return result, invalid("invalid core package manifest")
	}
	if len(entries) != 1+len(p.Objects) {
		return result, invalid("package contains unexpected entries")
	}
	objectKeys := map[string]string{}
	objects := map[string]packageObject{}
	objectTypes := map[string]string{}
	for _, obj := range p.Objects {
		if _, err = uuid.Parse(obj.Key); err != nil || len(obj.Key) != 36 || obj.Size < 0 || len(obj.SHA256) != 64 || objects[obj.Key].Key != "" {
			return result, invalid("invalid package object descriptor")
		}
		entry := entries["objects/"+obj.Key]
		if entry == nil || entry.UncompressedSize64 != uint64(obj.Size) {
			return result, invalid("package object missing or wrong size")
		}
		objects[obj.Key] = obj
		reader, err := entry.Open()
		if err != nil {
			return result, err
		}
		header := make([]byte, 12)
		headerSize, e := io.ReadFull(reader, header)
		if e != nil && !errors.Is(e, io.EOF) && !errors.Is(e, io.ErrUnexpectedEOF) {
			reader.Close()
			return result, e
		}
		objectTypes[obj.Key] = storage.DocumentType(header[:headerSize])
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(io.MultiReader(bytes.NewReader(header[:headerSize]), reader), obj.Size+1))
		reader.Close()
		if err != nil || n != obj.Size || hex.EncodeToString(h.Sum(nil)) != obj.SHA256 {
			return result, invalid("package object checksum mismatch")
		}
	}
	for _, table := range packageTables {
		data, ok := p.Tables[table]
		if !ok {
			return result, invalid("package table missing")
		}
		columns, _, err := s.packageColumns(ctx, table)
		if err != nil {
			return result, err
		}
		if !slices.Equal(columns, data.Columns) {
			return result, invalid("package columns do not match the core contract")
		}
		result.Rows += len(data.Rows)
		if result.Rows > 100000 {
			return result, invalid("package exceeds 100000 core records")
		}
		for i, row := range data.Rows {
			if len(row) != len(columns) {
				return result, invalid("invalid package record")
			}
			for j, v := range row {
				if n, ok := v.(json.Number); ok {
					value, err := n.Int64()
					if err != nil {
						return result, invalid("package integers must be exact")
					}
					data.Rows[i][j] = value
				} else {
					switch v.(type) {
					case nil, string, bool:
					default:
						return result, invalid("package records must contain scalar values")
					}
				}
			}
		}
		p.Tables[table] = data
	}
	for _, blob := range packageMaps(p.Tables["blobs"]) {
		key, ok := blob["object_key"].(string)
		if !ok {
			return result, invalid("invalid blob object")
		}
		obj, ok := objects[key]
		if !ok || blob["size"] != obj.Size || blob["sha256"] != obj.SHA256 {
			return result, invalid("blob metadata does not match packaged bytes")
		}
		if objectTypes[key] == "" || objectTypes[key] != blob["content_type"] {
			return result, invalid("packaged file signature does not match its supported media type")
		}
	}
	if err = validatePackageGroups(p); err != nil {
		return result, err
	}
	result.Objects = len(p.Objects)
	// Use the actual core constraints for the plan and application. A savepoint
	// rolls back a dry run, including identities, indexes and live notifications.
	if _, err = s.tx.ExecContext(ctx, "SAVEPOINT core_package"); err != nil {
		return result, err
	}
	defer func() {
		_, _ = s.tx.ExecContext(ctx, "ROLLBACK TO core_package")
		_, _ = s.tx.ExecContext(ctx, "RELEASE core_package")
	}()
	var populated bool
	if err = s.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM resources)").Scan(&populated); err != nil {
		return result, err
	}
	if populated {
		return result, ErrConflict
	}
	if !in.DryRun {
		for _, obj := range p.Objects {
			reader, err := entries["objects/"+obj.Key].Open()
			if err != nil {
				return result, err
			}
			stored, err := store.Put(ctx, reader, PackageLimit)
			reader.Close()
			if err != nil {
				return result, err
			}
			objectKeys[obj.Key] = stored.Key
		}
	}
	err = s.applyCorePackage(ctx, subject, p, objectKeys)
	if err != nil {
		if in.DryRun {
			result.Errors = []string{err.Error()}
			return result, nil
		}
		return result, err
	}
	if err = identity.RequireScope(ctx, s.tx, subject, "organization.manage"); err != nil {
		return result, invalid("package must retain the importing administrator")
	}
	result.Valid = true
	if in.DryRun {
		return result, nil
	}
	if _, err = s.tx.ExecContext(ctx, "RELEASE core_package"); err != nil {
		return result, err
	}
	return result, s.auditCatalog(ctx, subject, "portability.import", map[string]any{"rows": result.Rows, "objects": result.Objects})
}

func (s *Service) packageInsert(ctx context.Context, table string, row map[string]any) error {
	columns, _, err := s.packageColumns(ctx, table)
	if err != nil {
		return err
	}
	values := make([]any, len(columns))
	marks := make([]string, len(columns))
	for i, key := range columns {
		values[i] = row[key]
		columns[i] = quotePackageColumn(key)
		marks[i] = "?"
	}
	_, err = s.tx.ExecContext(ctx, "INSERT INTO "+quotePackageColumn(table)+" ("+strings.Join(columns, ",")+") VALUES ("+strings.Join(marks, ",")+")", values...)
	return err
}
func orderPackageParents(rows []map[string]any) ([]map[string]any, error) {
	var ordered []map[string]any
	done := map[any]bool{}
	for len(rows) > 0 {
		next := []map[string]any{}
		for _, row := range rows {
			parent := row["parent_id"]
			if parent == nil || done[parent] {
				ordered = append(ordered, row)
				done[row["id"]] = true
			} else {
				next = append(next, row)
			}
		}
		if len(next) == len(rows) {
			return nil, invalid("package containment is cyclic or incomplete")
		}
		rows = next
	}
	return ordered, nil
}

func (s *Service) applyCorePackage(ctx context.Context, subject string, p corePackage, objects map[string]string) error {
	if _, err := s.tx.ExecContext(ctx, "PRAGMA defer_foreign_keys=ON"); err != nil {
		return err
	}
	resources, err := orderPackageParents(packageMaps(p.Tables["resources"]))
	if err != nil {
		return err
	}
	scopes := map[any]any{}
	for _, row := range resources {
		scope := row["id"]
		if fmt.Sprint(row["inherit_permissions"]) == "1" || fmt.Sprint(row["inherit_permissions"]) == "true" {
			scope = scopes[row["parent_id"]]
		}
		if scope == nil || row["scope_id"] != scope {
			return invalid("package contains an invalid permission scope")
		}
		scopes[row["id"]] = scope
	}
	for _, row := range resources {
		if err = validateName(fmt.Sprint(row["name"])); err != nil {
			return err
		}
	}
	for _, table := range packageTables {
		if table == "resources" {
			continue
		}
		rows := packageMaps(p.Tables[table])
		if table == "terms" {
			rows, err = orderPackageParents(rows)
			if err != nil {
				return err
			}
		}
		// Collections must exist before their schema and attachments; item
		// initial state deliberately has no revision pointers or tombstone.
		if table == "collection_types" {
			for _, row := range resources {
				if row["kind"] == "workspace" || row["kind"] == "list" || row["kind"] == "library" {
					if err = s.insertPackageResource(ctx, row); err != nil {
						return err
					}
				}
			}
		}
		if table == "blobs" {
			for _, row := range resources {
				if row["kind"] == "item" || row["kind"] == "folder" {
					if err = s.insertPackageResource(ctx, row); err != nil {
						return err
					}
				}
			}
		}
		for _, row := range rows {
			if table == "identity_users" {
				// Stage inactive memberships atomically, then restore account status.
				if row["deleted_at"] == nil {
					row["active"] = int64(1)
				}
				id, ok := row["id"].(string)
				if !ok || identity.ValidateSubject(id) != nil {
					return invalid("invalid package user")
				}
				var exists bool
				if err = s.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_users WHERE id=?)", id).Scan(&exists); err != nil {
					return err
				}
				if exists {
					if _, err = s.tx.ExecContext(ctx, "UPDATE identity_users SET active=?,display_name=?,email=?,deleted_at=? WHERE id=?", row["active"], row["display_name"], row["email"], row["deleted_at"], id); err != nil {
						return err
					}
					continue
				}
			}
			if table == "identity_roles" || table == "identity_assignments" || table == "term_groups" || table == "term_sets" {
				existing, err := s.packageExisting(ctx, table, row)
				if err != nil {
					return err
				}
				if existing {
					continue
				}
			}
			if table == "blobs" {
				if key, ok := objects[fmt.Sprint(row["object_key"])]; ok {
					row["object_key"] = key
				}
			}
			if err = s.packageInsert(ctx, table, row); err != nil {
				return fmt.Errorf("package %s: %w", table, err)
			}
		}
	}
	// Restore pointers, counters, tombstones and exact permission scopes only
	// after their dependencies exist. Scope triggers are re-run by inheritance.
	for _, row := range resources {
		if _, err = s.tx.ExecContext(ctx, "UPDATE resources SET head_revision_id=?,published_revision_id=?,schema_head_id=?,next_revision_number=?,scope_id=?,deleted_at=?,deleted_by=?,name_key=? WHERE id=?", row["head_revision_id"], row["published_revision_id"], row["schema_head_id"], row["next_revision_number"], row["scope_id"], row["deleted_at"], row["deleted_by"], row["name_key"], row["id"]); err != nil {
			return err
		}
	}
	for _, user := range packageMaps(p.Tables["identity_users"]) {
		if _, err = s.tx.ExecContext(ctx, "UPDATE identity_users SET active=? WHERE id=?", user["active"], user["id"]); err != nil {
			return err
		}
	}
	if len(p.Organization) != 3 {
		return invalid("invalid package organization profile")
	}
	if _, err = s.tx.ExecContext(ctx, "UPDATE identity_organizations SET identifier=?,display_name=?,description=?,version=version+1 WHERE singleton=1", p.Organization["identifier"], p.Organization["display_name"], p.Organization["description"]); err != nil {
		return err
	}
	// Rebuild typed indexes from retained surfaces, including fields whose
	// source index operation was unfinished. Execution records do not transfer.
	projections, err := s.Client.ItemSurface.Query().All(ctx)
	if err != nil {
		return err
	}
	for _, projection := range projections {
		revision, err := s.Client.ItemRevision.Get(ctx, projection.RevisionID)
		if err != nil {
			return err
		}
		schema, err := s.Client.SchemaRevision.Get(ctx, revision.SchemaRevisionID)
		if err != nil {
			return err
		}
		defs, _, err := typedDefinitions(schema.Definition, revision.ContentTypeID)
		if err != nil {
			return err
		}
		current, err := s.Client.FieldDefinition.Query().Where(collectionFieldPredicate(projection.ContainerID)).All(ctx)
		if err != nil {
			return err
		}
		if err = s.indexSurface(ctx, projection, indexedDefinitions(current, defs)); err != nil {
			return err
		}
	}
	if _, err = s.Client.FieldDefinition.Update().SetIndexStatus(fielddefinition.IndexStatusReady).Save(ctx); err != nil {
		return err
	}
	// Deferral permits cycles such as resource->revision->resource; explicitly
	// check before a dry run reports success (its savepoint never commits).
	rows, err := s.tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return invalid("package contains unresolved references")
	}
	return rows.Err()
}
func (s *Service) insertPackageResource(ctx context.Context, source map[string]any) error {
	row := map[string]any{}
	for k, v := range source {
		row[k] = v
	}
	row["head_revision_id"] = nil
	row["published_revision_id"] = nil
	row["schema_head_id"] = nil
	row["next_revision_number"] = int64(1)
	row["deleted_at"] = nil
	row["deleted_by"] = nil
	return s.packageInsert(ctx, "resources", row)
}
func (s *Service) packageExisting(ctx context.Context, table string, row map[string]any) (bool, error) {
	// Built-ins and the installation's initial administrator may already exist.
	query := "id=?"
	args := []any{row["id"]}
	if table == "identity_assignments" {
		query = "role_id=? AND principal_type=? AND principal_id=?"
		args = []any{row["role_id"], row["principal_type"], row["principal_id"]}
	}
	columns, expressions, err := s.packageColumns(ctx, table)
	if err != nil {
		return false, err
	}
	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	err = s.tx.QueryRowContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM "+quotePackageColumn(table)+" WHERE "+query, args...).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for i, key := range columns {
		if key == "created_at" || key == "updated_at" || key == "id" || key == "version" {
			continue
		}
		if b, ok := values[i].([]byte); ok {
			values[i] = string(b)
		}
		if !reflect.DeepEqual(values[i], row[key]) && fmt.Sprint(values[i]) != fmt.Sprint(row[key]) {
			return false, ErrConflict
		}
	}
	return true, nil
}

func (s *Service) ExportPackage(ctx context.Context, subject, id string) (PackageResult, error) {
	return read(ctx, s, func(t *Service) (PackageResult, error) {
		var result PackageResult
		if err := t.RequireScope(ctx, subject, "organization.manage"); err != nil {
			return result, err
		}
		op, err := t.Client.Operation.Get(ctx, id)
		if ent.IsNotFound(err) {
			return result, ErrNotFound
		}
		if err != nil {
			return result, err
		}
		if op.Kind != OperationKindExport || op.CreatedBy != subject || op.Status != operation.StatusSucceeded {
			return result, ErrNotFound
		}
		if err = json.Unmarshal(op.Result, &result); err != nil {
			return result, err
		}
		if result.ExpiresAt == nil || !time.Now().UTC().Before(*result.ExpiresAt) {
			return result, ErrNotFound
		}
		return result, nil
	})
}

func validatePackageGroups(p corePackage) error {
	known := map[string]bool{}
	for _, scope := range identity.Catalog {
		known[scope.Name] = true
	}
	for _, row := range packageMaps(p.Tables["identity_roles"]) {
		raw, ok := row["scopes"].(string)
		if !ok {
			return invalid("invalid package role scopes")
		}
		var scopes []string
		if json.Unmarshal([]byte(raw), &scopes) != nil {
			return invalid("invalid package role scopes")
		}
		for _, scope := range scopes {
			if !known[scope] {
				return invalid("package role contains an unknown scope")
			}
		}
	}
	children := map[string][]string{}
	for _, row := range packageMaps(p.Tables["identity_memberships"]) {
		if row["principal_type"] == "group" {
			children[fmt.Sprint(row["group_id"])] = append(children[fmt.Sprint(row["group_id"])], fmt.Sprint(row["principal_id"]))
		}
	}
	var visit func(string, map[string]bool, int) error
	visit = func(id string, path map[string]bool, depth int) error {
		if path[id] || depth > identity.MaxGroupDepth {
			return invalid("package group membership is cyclic or exceeds the group depth limit")
		}
		path[id] = true
		defer delete(path, id)
		for _, child := range children[id] {
			if err := visit(child, path, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for id := range children {
		if err := visit(id, map[string]bool{}, 1); err != nil {
			return err
		}
	}
	return nil
}
