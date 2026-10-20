package dms

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"papergo/internal/dal/portable"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/surfaces"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/integrations"
	"papergo/internal/dal/operations"
	"papergo/internal/identity"
	"papergo/internal/storage"
)

const PackageLimit int64 = 4 << 30
const PackageManifestLimit int64 = 64 << 20

// PackageRecordLimit bounds the rows a package carries.
const PackageRecordLimit = 100000
const PackageRetention = 7 * 24 * time.Hour
const OperationKindExport = "portability.export"
const OperationKindImport = "portability.import"

type packageTable struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}
type packageObject struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type portableIntegration struct {
	ContainerID string `json:"container_id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
}

type corePackage struct {
	Scope           string                  `json:"scope"`
	WorkspaceID     string                  `json:"workspace_id,omitempty"`
	TemplateSubject string                  `json:"template_subject,omitempty"`
	IncludeData     bool                    `json:"include_data"`
	Integrations    []portableIntegration   `json:"integrations"`
	Format          string                  `json:"format"`
	Organization    map[string]any          `json:"organization"`
	Tables          map[string]packageTable `json:"tables"`
	Objects         []packageObject         `json:"objects"`
}
type ExportOptions struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	IncludeData *bool  `json:"include_data,omitempty"`
}

type packagePayload struct {
	ExportOptions
	WorkspaceName string `json:"workspace_name,omitempty"`
	ObjectKey     string `json:"object_key,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
}
type PackageResult struct {
	WorkspaceID   string     `json:"workspace_id,omitempty"`
	WorkspaceName string     `json:"workspace_name,omitempty"`
	Warnings      []string   `json:"warnings,omitempty"`
	Valid         bool       `json:"valid"`
	DryRun        bool       `json:"dry_run,omitempty"`
	Rows          int        `json:"rows,omitempty"`
	Objects       int        `json:"objects,omitempty"`
	Errors        []string   `json:"errors,omitempty"`
	ObjectKey     string     `json:"object_key,omitempty"`
	Size          int64      `json:"size,omitempty"`
	SHA256        string     `json:"sha256,omitempty"`
	PackageURL    string     `json:"package_url,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

// RegisterPortability wires storage into the durable handlers before workers start.
func (s *Service) RegisterPortability(store storage.Store) error {
	validate := func(ctx context.Context, t *Service, subject, container string, raw json.RawMessage) error {
		if container != "" {
			return invalid("package operations have no container")
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
		if in.WorkspaceID != "" {
			w, err := t.workspace(ctx, subject, in.WorkspaceID, "manage")
			if err != nil {
				return err
			}
			if w.PersonalOwnerID != nil {
				return invalid("personal Home cannot be exported as a workspace package")
			}
		}
		if in.WorkspaceName != "" {
			return validateContainerMetadata(in.WorkspaceName, "")
		}
		return nil
	}
	if err := s.RegisterOperationHandler(OperationKindExport, OperationHandler{Scope: "organization.manage", Validate: validate, Step: func(ctx context.Context, t *Service, op *operations.Operation) error {
		var in packagePayload
		if err := json.Unmarshal(op.Payload, &in); err != nil {
			return err
		}
		// Recheck access at execution time as well as when the operation is queued.
		if err := validate(ctx, t, op.CreatedBy, "", op.Payload); err != nil {
			return err
		}
		obj, err := t.exportCorePackage(ctx, store, in.ExportOptions)
		if err != nil {
			return err
		}
		expires := time.Now().UTC().Add(PackageRetention)
		return t.finishPackage(ctx, op, PackageResult{Valid: true, ObjectKey: obj.Key, Size: obj.Size, SHA256: obj.SHA256, PackageURL: "/v1/portability/exports/" + op.ID + "/package", ExpiresAt: &expires})
	}}); err != nil {
		return err
	}
	return s.RegisterOperationHandler(OperationKindImport, OperationHandler{Scope: "organization.manage", Validate: validate, Step: func(ctx context.Context, t *Service, op *operations.Operation) error {
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
func (s *Service) finishPackage(ctx context.Context, op *operations.Operation, result PackageResult) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	completed := time.Now().UTC()
	op.Result, op.Status, op.Progress, op.CompletedAt = raw, operations.StatusSucceeded, 100, &completed
	return s.DAL.Operations().Save(ctx, op)
}
func (s *Service) StartExport(ctx context.Context, subject string, options ExportOptions) (*OperationView, error) {
	raw, err := json.Marshal(packagePayload{ExportOptions: options})
	if err != nil {
		return nil, err
	}
	return s.EnqueueOperation(ctx, subject, "", OperationKindExport, raw)
}
func (s *Service) StartImport(ctx context.Context, subject, objectKey string, dryRun bool, workspaceName string) (*OperationView, error) {
	raw, _ := json.Marshal(packagePayload{ObjectKey: objectKey, DryRun: dryRun, WorkspaceName: workspaceName})
	return s.EnqueueOperation(ctx, subject, "", OperationKindImport, raw)
}

// packageColumns lists the names of the columns a package carries of a table.
func (s *Service) packageColumns(ctx context.Context, table string) ([]string, error) {
	cols, err := s.DAL.Portable().Columns(ctx, table)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return names, nil
}
func (s *Service) exportCorePackage(ctx context.Context, store storage.Store, options ExportOptions) (storage.Object, error) {
	p := corePackage{Scope: "organization", IncludeData: options.IncludeData == nil || *options.IncludeData, Format: "papergo-core", Tables: map[string]packageTable{}, Objects: []packageObject{}, Organization: map[string]any{}}
	org, err := s.DAL.Directory().Organization(ctx)
	if err != nil {
		return storage.Object{}, err
	}
	p.Organization = map[string]any{"identifier": org.Identifier, "display_name": org.DisplayName, "description": org.Description}
	sources, err := s.DAL.Integrations().List(ctx, integrations.Filter{})
	if err != nil {
		return storage.Object{}, err
	}
	for _, source := range sources {
		p.Integrations = append(p.Integrations, portableIntegration{source.ContainerID, source.Kind, source.Name})
	}
	scope := portable.Scope{WorkspaceID: options.WorkspaceID, Data: p.IncludeData}
	scanned := 0
	for _, table := range portable.Tables {
		// A workspace package counts its rows once it has selected them.
		limit := 0
		if options.WorkspaceID == "" {
			limit = PackageRecordLimit - scanned + 1
		}
		cols, rows, err := s.DAL.Portable().Export(ctx, table, scope, limit)
		if err != nil {
			return storage.Object{}, err
		}
		data := packageTable{Columns: make([]string, len(cols)), Rows: rows}
		for i, c := range cols {
			data.Columns[i] = c.Name
			if table == "integration_items" && c.Name == "integration_id" {
				for _, row := range rows {
					row[i] = nil
				}
			}
		}
		scanned += len(rows)
		if options.WorkspaceID == "" && scanned > PackageRecordLimit {
			return storage.Object{}, invalid(fmt.Sprintf("package exceeds %d core records", PackageRecordLimit))
		}
		p.Tables[table] = data
	}
	if options.WorkspaceID != "" {
		p.Scope, p.WorkspaceID = "workspace", options.WorkspaceID
		if err := selectWorkspacePackage(&p); err != nil {
			return storage.Object{}, err
		}
		clearMissingPublicationRevisions(&p)
		if err := s.validateWorkspaceSourceReferences(ctx, p); err != nil {
			return storage.Object{}, err
		}
		if err := validateWorkspaceReferences(p); err != nil {
			return storage.Object{}, err
		}
		if err := validateWorkspaceValues(p); err != nil {
			return storage.Object{}, err
		}
	} else if !p.IncludeData {
		if err := s.validateWorkspaceSourceReferences(ctx, p); err != nil {
			return storage.Object{}, err
		}
	}
	count := 0
	for _, table := range p.Tables {
		count += len(table.Rows)
	}
	if count > PackageRecordLimit {
		return storage.Object{}, invalid(fmt.Sprintf("package exceeds %d core records", PackageRecordLimit))
	}
	p.Objects = packageObjects(packageMaps(p.Tables["blobs"]), packageMaps(p.Tables["blob_previews"]))
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

// packageObjects lists each stored object once, in first-seen order. A key is
// left out when every blob that names it has been reclaimed and no extracted
// text still needs the bytes. History metadata stays in the blob rows.
func packageObjects(blobs []map[string]any, renditions ...[]map[string]any) []packageObject {
	type ref struct {
		size int64
		sha  string
		keep bool
		set  bool
	}
	order := []string{}
	meta := map[string]*ref{}
	add := func(key string) *ref {
		if row, ok := meta[key]; ok {
			return row
		}
		row := &ref{}
		meta[key] = row
		order = append(order, key)
		return row
	}
	remember := func(row *ref, size any, sha any) {
		n, ok := size.(int64)
		text, textOK := sha.(string)
		if !ok || !textOK {
			return
		}
		row.size, row.sha, row.set = n, text, true
	}
	for _, blob := range blobs {
		key, _ := blob["object_key"].(string)
		if key == "" {
			continue
		}
		row := add(key)
		if blob["availability"] != "reclaimed" {
			row.keep = true
			remember(row, blob["size"], blob["sha256"])
			continue
		}
		if !row.set {
			remember(row, blob["size"], blob["sha256"])
		}
	}
	for _, texts := range renditions {
		for _, text := range texts {
			key, _ := text["object_key"].(string)
			if key == "" {
				continue
			}
			row := add(key)
			row.keep = true
			if !row.set {
				remember(row, text["object_size"], text["object_sha256"])
			}
		}
	}
	out := []packageObject{}
	for _, key := range order {
		row := meta[key]
		if row.keep {
			out = append(out, packageObject{Key: key, Size: row.size, SHA256: row.sha})
		}
	}
	return out
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
	if err != nil || p.Format != "papergo-core" || (p.Scope != "organization" && p.Scope != "workspace") || len(p.Tables) != len(portable.Tables) {
		return result, invalid("invalid core package manifest")
	}
	if p.Scope == "organization" && p.WorkspaceID != "" {
		return result, invalid("organization packages cannot specify workspace_id")
	}
	if p.TemplateSubject != "" && (p.Scope != "workspace" || p.IncludeData) {
		return result, invalid("template_subject requires a configuration-only workspace package")
	}
	if !p.IncludeData {
		if len(p.Objects) != 0 {
			return result, invalid("configuration package contains file objects")
		}
		for _, table := range portable.DataTables {
			if len(p.Tables[table].Rows) != 0 {
				return result, invalid("configuration package contains content")
			}
		}
		for _, row := range packageMaps(p.Tables["resources"]) {
			if row["kind"] == "item" || row["kind"] == "folder" {
				return result, invalid("configuration package contains items or folders")
			}
		}
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
	for _, table := range portable.Tables {
		data, ok := p.Tables[table]
		if !ok {
			return result, invalid("package table missing")
		}
		columns, err := s.DAL.Portable().Columns(ctx, table)
		if err != nil {
			return result, err
		}
		if !slices.EqualFunc(columns, data.Columns, func(c portable.Column, name string) bool { return c.Name == name }) {
			return result, invalid("package columns do not match the core contract")
		}
		result.Rows += len(data.Rows)
		if result.Rows > PackageRecordLimit {
			return result, invalid(fmt.Sprintf("package exceeds %d core records", PackageRecordLimit))
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
					v = value
				} else {
					switch v.(type) {
					case nil, string, bool:
					default:
						return result, invalid("package records must contain scalar values")
					}
				}
				if data.Rows[i][j], err = portable.Normalize(columns[j], v); err != nil {
					return result, invalid("invalid package record value in " + table + "." + columns[j].Name)
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
		obj, present := objects[key]
		if blob["availability"] == "reclaimed" && !present {
			continue
		}
		if !present || blob["size"] != obj.Size || blob["sha256"] != obj.SHA256 {
			return result, invalid("blob metadata does not match packaged bytes")
		}
		if objectTypes[key] == "" || objectTypes[key] != blob["content_type"] {
			return result, invalid("packaged file signature does not match its supported media type")
		}
	}
	fileHashes := map[any]bool{}
	for _, blob := range packageMaps(p.Tables["blobs"]) {
		fileHashes[blob["sha256"]] = true
	}
	for _, preview := range packageMaps(p.Tables["blob_previews"]) {
		if !fileHashes[preview["sha256"]] {
			return result, invalid("preview has no packaged source file")
		}
		if preview["status"] == "failed" {
			continue
		}
		key, _ := preview["object_key"].(string)
		obj, ok := objects[key]
		if !ok || preview["object_size"] != obj.Size || preview["object_sha256"] != obj.SHA256 || objectTypes[key] != "image/webp" {
			return result, invalid("preview does not match its packaged WebP object")
		}
	}
	if err = validatePackageGroups(p); err != nil {
		return result, err
	}
	result.Objects = len(p.Objects)
	// Use the actual core constraints for the plan and application. A savepoint
	// rolls back a dry run, including identities, indexes and live notifications.
	err = s.DAL.Savepoint(ctx, "core_package", func() (bool, error) {
		populated, err := s.DAL.Resources().Exists(ctx, resources.Filter{})
		if err != nil {
			return false, err
		}
		if populated && p.Scope == "organization" {
			return false, invalid("organization packages require an instance without resources")
		}
		if p.Scope == "organization" && in.WorkspaceName != "" {
			return false, invalid("workspace_name requires a workspace package")
		}
		if p.Scope == "workspace" {
			if err = validateWorkspacePackage(p); err != nil {
				return false, err
			}
			if err = s.bindWorkspaceTemplate(ctx, &p, subject); err != nil {
				return false, err
			}
			var warnings []string
			p, warnings, err = s.prepareWorkspacePackage(ctx, p, in.WorkspaceName)
			if err != nil {
				return false, err
			}
			result.Warnings = append(result.Warnings, warnings...)
			result.WorkspaceID = p.WorkspaceID
			for _, row := range packageMaps(p.Tables["resources"]) {
				if row["id"] == p.WorkspaceID {
					result.WorkspaceName, _ = row["name"].(string)
				}
			}
		}
		for _, integration := range p.Integrations {
			result.Warnings = append(result.Warnings, "Reconfigure "+integration.Name+" on list "+integration.ContainerID+": integration secrets are omitted from portable packages.")
		}
		if !in.DryRun {
			for _, obj := range p.Objects {
				reader, err := entries["objects/"+obj.Key].Open()
				if err != nil {
					return false, err
				}
				stored, err := store.Put(ctx, reader, PackageLimit)
				reader.Close()
				if err != nil {
					return false, err
				}
				objectKeys[obj.Key] = stored.Key
			}
		}
		if err = s.applyCorePackage(ctx, subject, p, objectKeys); err != nil {
			if in.DryRun {
				result.Errors = []string{err.Error()}
				return true, nil
			}
			return false, err
		}
		if err = identity.RequireScope(ctx, s.DAL, subject, "organization.manage"); err != nil {
			return false, invalid("package must retain the importing administrator")
		}
		result.Valid = true
		return in.DryRun, nil
	})
	if err != nil || in.DryRun {
		return result, err
	}
	// Imported entries raised no change events: rebuild the search index,
	// file content included (the imported text is reused, not read again).
	if s.search != nil {
		raw, _ := json.Marshal(searchReindexPayload{Stamp: time.Now().UnixNano(), Content: true})
		if _, err = s.DAL.Operations().Enqueue(ctx, operations.Operation{Kind: OperationKindSearchReindex, CreatedBy: systemSubject, Payload: raw, TraceID: s.traceID}); err != nil {
			return result, err
		}
	}
	return result, s.auditCatalog(ctx, subject, "portability.import", map[string]any{"rows": result.Rows, "objects": result.Objects})
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
	if err := s.DAL.Portable().DeferReferences(ctx); err != nil {
		return err
	}
	packageResources, err := orderPackageParents(packageMaps(p.Tables["resources"]))
	if err != nil {
		return err
	}
	scopes := map[any]any{}
	for _, row := range packageResources {
		scope := row["id"]
		if row["inherit_permissions"] == true {
			scope = scopes[row["parent_id"]]
		}
		if scope == nil || row["scope_id"] != scope {
			return invalid("package contains an invalid permission scope")
		}
		scopes[row["id"]] = scope
	}
	folderDepths := map[any]int{}
	for _, row := range packageResources {
		kind, name := fmt.Sprint(row["kind"]), fmt.Sprint(row["name"])
		if err = validateResourceName(kind, name); err != nil {
			return err
		}
		if kind == "workspace" || kind == "list" || kind == "library" {
			if err = validateContainerMetadata(name, fmt.Sprint(row["description"])); err != nil {
				return err
			}
		}
		depth := folderDepths[row["parent_id"]]
		if kind == "folder" {
			depth++
		}
		if row["deleted_at"] == nil && depth > MaxFolderDepth {
			return invalid("package exceeds maximum folder depth")
		}
		folderDepths[row["id"]] = depth
	}

	for _, table := range portable.Tables {
		if table == "resources" {
			continue
		}
		rows := packageMaps(p.Tables[table])
		for _, row := range rows {
			if table == "integration_items" && row["integration_id"] != nil {
				return invalid("portable integration items must be detached")
			}
			if table == "item_revisions" {
				if err = validateTitle(fmt.Sprint(row["name"])); err != nil {
					return err
				}
			}
			if table == "blobs" && !filenameValid(fmt.Sprint(row["filename"])) {
				return invalid("package contains an invalid filename")
			}
		}
		if table == "terms" {
			rows, err = orderPackageParents(rows)
			if err != nil {
				return err
			}
		}
		// Collections must exist before their schema and attachments; item
		// initial state deliberately has no revision pointers or tombstone.
		if table == "collection_types" {
			for _, row := range packageResources {
				if row["kind"] == "workspace" || row["kind"] == "list" || row["kind"] == "library" {
					if err = s.insertPackageResource(ctx, row); err != nil {
						return err
					}
				}
			}
		}
		if table == "blobs" {
			for _, row := range packageResources {
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
					row["active"] = true
				}
				id, ok := row["id"].(string)
				if !ok || identity.ValidateSubject(id) != nil {
					return invalid("invalid package user")
				}
				n, err := s.DAL.Portable().Count(ctx, "identity_users", portable.Row{"id": id})
				if err != nil {
					return err
				}
				if n > 0 {
					if err = s.DAL.Portable().MergeUser(ctx, row); err != nil {
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
			if table == "blobs" || table == "blob_previews" {
				if key, ok := objects[fmt.Sprint(row["object_key"])]; ok {
					row["object_key"] = key
				}
			}
			if err = s.DAL.Portable().Insert(ctx, table, row); err != nil {
				return fmt.Errorf("package %s: %w", table, err)
			}
		}
	}
	// Restore pointers, counters, tombstones and exact permission scopes only
	// after their dependencies exist. Scope triggers are re-run by inheritance.
	for _, row := range packageResources {
		restored := portable.Row{}
		for _, column := range []string{"head_revision_id", "published_revision_id", "schema_head_id", "next_revision_number", "scope_id", "deleted_at", "deleted_by", "name_key"} {
			restored[column] = row[column]
		}
		if err = s.DAL.Portable().Update(ctx, "resources", portable.Row{"id": row["id"]}, restored); err != nil {
			return err
		}
	}
	for _, user := range packageMaps(p.Tables["identity_users"]) {
		if err = s.DAL.Portable().Update(ctx, "identity_users", portable.Row{"id": user["id"]}, portable.Row{"active": user["active"]}); err != nil {
			return err
		}
	}
	if p.Scope == "organization" {
		if len(p.Organization) != 3 {
			return invalid("invalid package organization profile")
		}
		org, err := s.DAL.Directory().Organization(ctx)
		if err != nil {
			return err
		}
		var ok [3]bool
		org.Identifier, ok[0] = p.Organization["identifier"].(string)
		org.DisplayName, ok[1] = p.Organization["display_name"].(string)
		org.Description, ok[2] = p.Organization["description"].(string)
		if ok != [3]bool{true, true, true} {
			return invalid("invalid package organization profile")
		}
		if err = s.DAL.Directory().SaveOrganization(ctx, org, org.Version); err != nil {
			return err
		}
	}
	// Rebuild typed indexes from retained surfaces, including fields whose
	// source index operation was unfinished. Execution records do not transfer.
	var collections []string
	for _, row := range packageResources {
		if row["kind"] == "list" || row["kind"] == "library" {
			collections = append(collections, fmt.Sprint(row["id"]))
		}
	}
	projections, err := s.DAL.Surfaces().Surfaces(ctx, surfaces.Filter{ContainerIDs: collections})
	if err != nil {
		return err
	}
	for _, projection := range projections {
		revision, err := s.revision(ctx, projection.RevisionID)
		if err != nil {
			return err
		}
		schema, err := s.DAL.Catalog().Schema(ctx, revision.SchemaRevisionID)
		if err != nil {
			return err
		}
		defs, _, err := typedDefinitions(schema.Definition, revision.ContentTypeID)
		if err != nil {
			return err
		}
		current, err := s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: projection.ContainerID})
		if err != nil {
			return err
		}
		if err = s.indexSurface(ctx, projection, indexedDefinitions(current, defs)); err != nil {
			return err
		}
	}
	if err = s.DAL.Catalog().SetIndexStatus(ctx, packageIDs(p.Tables["field_definitions"]), "ready"); err != nil {
		return err
	}
	invalidHome, err := s.DAL.Portable().IncompleteHomes(ctx)
	if err != nil {
		return err
	}
	if invalidHome {
		return invalid("package contains an incomplete or deleted personal Home")
	}
	// Deferral permits cycles such as resource->revision->resource; explicitly
	// check before a dry run reports success (its savepoint never commits).
	unresolved, err := s.DAL.Portable().Unresolved(ctx)
	if err != nil {
		return err
	}
	if unresolved {
		return invalid("package contains unresolved references")
	}
	return nil
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
	return s.DAL.Portable().Insert(ctx, "resources", portable.Row(row))
}
func (s *Service) packageExisting(ctx context.Context, table string, row map[string]any) (bool, error) {
	// Built-ins and the installation's initial administrator may already exist.
	match := portable.Row{"id": row["id"]}
	if table == "identity_assignments" {
		match = portable.Row{"role_id": row["role_id"], "principal_type": row["principal_type"], "principal_id": row["principal_id"]}
	}
	existing, err := s.DAL.Portable().Find(ctx, table, match)
	if dberr.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for key, value := range existing {
		if key == "created_at" || key == "updated_at" || key == "id" || key == "version" {
			continue
		}
		if !reflect.DeepEqual(value, row[key]) {
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
		op, err := t.DAL.Operations().Find(ctx, id)
		if dberr.IsNotFound(err) {
			return result, ErrNotFound
		}
		if err != nil {
			return result, err
		}
		if op.Kind != OperationKindExport || op.CreatedBy != subject || op.Status != operations.StatusSucceeded {
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
		if path[id] || depth > directory.MaxGroupDepth {
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
