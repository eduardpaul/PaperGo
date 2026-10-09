package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"papergo/ent"
	"papergo/internal/dms"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

type SmartFolder struct {
	ID          string                    `json:"id" format:"uuid"`
	WorkspaceID *string                   `json:"workspace_id,omitempty" format:"uuid" doc:"Workspace of a shared folder; absent for personal folders."`
	OwnerID     *string                   `json:"owner_id,omitempty" doc:"Owner of a personal folder; absent for shared folders."`
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Definition  dms.SmartFolderDefinition `json:"definition"`
	Version     int                       `json:"version" minimum:"1"`
	CreatedBy   string                    `json:"created_by"`
	UpdatedBy   string                    `json:"updated_by"`
	CreatedAt   time.Time                 `json:"created_at"`
	UpdatedAt   time.Time                 `json:"updated_at"`
}

func (f SmartFolder) etag() string { return etag(f.Version) }
func smartFolderOut(f *ent.SmartFolder) (SmartFolder, error) {
	out := SmartFolder{ID: f.ID, WorkspaceID: f.WorkspaceID, OwnerID: f.OwnerID, Name: f.Name, Description: f.Description, Version: f.Version, CreatedBy: f.CreatedBy, UpdatedBy: f.UpdatedBy, CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt}
	if len(f.Definition) == 0 {
		return out, nil
	}
	return out, json.Unmarshal(f.Definition, &out.Definition)
}

type SmartFolderEntry struct {
	WorkspaceID    string   `json:"workspace_id" format:"uuid"`
	CollectionID   string   `json:"collection_id" format:"uuid"`
	CollectionName string   `json:"collection_name"`
	Item           Resource `json:"item"`
}
type SmartFolderResult struct {
	Data       []SmartFolderEntry `json:"data"`
	NextCursor string             `json:"next_cursor,omitempty" doc:"Opaque cursor for the next page; absent on the last page."`
	Total      int                `json:"total" minimum:"0" doc:"Authorized members before pagination."`
}
type SmartFolderImportResult struct {
	Created          int           `json:"created" minimum:"0"`
	Updated          int           `json:"updated" minimum:"0"`
	WorkspaceVersion int           `json:"workspace_version" minimum:"1" doc:"Also returned as the ETag."`
	Data             []SmartFolder `json:"data" doc:"Created and updated definitions."`
}
type Classified struct {
	Status int
	ETag   string `header:"ETag" doc:"Quoted item version to send as If-Match on the next change."`
	Body   Resource
}
type SmartFolderPath struct {
	ID string `path:"id" doc:"Smart folder ID."`
}

func (a *API) registerSmartFolders(api huma.API) {
	tags := []string{"Smart folders"}
	register(a, api, huma.Operation{OperationID: "listSmartFolders", Method: http.MethodGet, Path: "/v1/smart-folders", Summary: "List own personal and readable shared smart folders", Tags: tags},
		func(ctx context.Context, in *struct {
			WorkspaceID string `query:"workspace_id" doc:"Limits the list to one workspace's shared folders."`
			Paging
		}) (*Body[Page[SmartFolder]], error) {
			out, err := a.DMS.SmartFolders(ctx, subject(ctx), in.WorkspaceID, in.After, in.Limit)
			return page(out, err, smartFolderOut)
		})
	register(a, api, huma.Operation{OperationID: "createSmartFolder", Method: http.MethodPost, Path: "/v1/smart-folders", Summary: "Create a smart folder", Description: "Shared folders require workspace manage permission and advance the workspace version.", Tags: tags, DefaultStatus: 201},
		func(ctx context.Context, in *struct{ Body dms.SmartFolderInput }) (*Tagged[SmartFolder], error) {
			out, err := a.DMS.CreateSmartFolder(ctx, subject(ctx), in.Body)
			return tagged(out, err, smartFolderOut)
		})
	register(a, api, huma.Operation{OperationID: "getSmartFolder", Method: http.MethodGet, Path: "/v1/smart-folders/{id}", Summary: "Read a smart folder", Tags: tags},
		func(ctx context.Context, in *struct{ SmartFolderPath }) (*Tagged[SmartFolder], error) {
			out, err := a.DMS.SmartFolder(ctx, subject(ctx), in.ID)
			return tagged(out, err, smartFolderOut)
		})
	register(a, api, huma.Operation{OperationID: "updateSmartFolder", Method: http.MethodPut, Path: "/v1/smart-folders/{id}", Summary: "Replace a smart folder definition", Description: "Uses the smart folder ETag. Ownership and workspace are immutable.", Tags: tags},
		func(ctx context.Context, in *struct {
			SmartFolderPath
			precondition
			Body dms.SmartFolderInput
		}) (*Tagged[SmartFolder], error) {
			out, err := a.DMS.UpdateSmartFolder(ctx, subject(ctx), in.ID, in.Version, in.Body)
			return tagged(out, err, smartFolderOut)
		})
	register(a, api, huma.Operation{OperationID: "deleteSmartFolder", Method: http.MethodDelete, Path: "/v1/smart-folders/{id}", Summary: "Delete a smart folder definition", Description: "Uses the smart folder ETag. Items are never affected.", Tags: tags},
		func(ctx context.Context, in *struct {
			SmartFolderPath
			precondition
		}) (*Empty, error) {
			if err := a.DMS.DeleteSmartFolder(ctx, subject(ctx), in.ID, in.Version); err != nil {
				return nil, err
			}
			return &Empty{}, nil
		})
	register(a, api, huma.Operation{OperationID: "querySmartFolder", Method: http.MethodPost, Path: "/v1/smart-folders/{id}/query", Summary: "Query authorized live membership", Tags: tags},
		func(ctx context.Context, in *struct {
			SmartFolderPath
			Body dms.SmartFolderQueryRequest
		}) (*Body[SmartFolderResult], error) {
			out, err := a.DMS.QuerySmartFolder(ctx, subject(ctx), in.ID, in.Body)
			if err != nil {
				return nil, err
			}
			data, err := convert(out.Data, func(e dms.SmartFolderEntry) (SmartFolderEntry, error) {
				return SmartFolderEntry{WorkspaceID: e.WorkspaceID, CollectionID: e.CollectionID, CollectionName: e.CollectionName, Item: resourceOut(e.Item)}, nil
			})
			return &Body[SmartFolderResult]{SmartFolderResult{Data: data, NextCursor: out.NextCursor, Total: out.Total}}, err
		})
	register(a, api, huma.Operation{OperationID: "groupSmartFolder", Method: http.MethodPost, Path: "/v1/smart-folders/{id}/query/groups", Summary: "Read counts and labels for the next navigation level", Tags: tags},
		func(ctx context.Context, in *struct {
			SmartFolderPath
			Body dms.SmartFolderQueryRequest
		}) (*Body[dms.SmartFolderGroupsResult], error) {
			out, err := a.DMS.SmartFolderGroups(ctx, subject(ctx), in.ID, in.Body)
			if err != nil {
				return nil, err
			}
			if out.Data == nil {
				out.Data = []dms.SmartFolderGroup{}
			}
			return &Body[dms.SmartFolderGroupsResult]{out}, nil
		})
	resource := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[Resource](), true, "Resource")
	itemETag := map[string]*huma.Header{"ETag": {Description: "Quoted item version to send as If-Match on the next change.", Schema: &huma.Schema{Type: huma.TypeString}}}
	register(a, api, huma.Operation{OperationID: "classifySmartFolder", Method: http.MethodPost, Path: "/v1/smart-folders/{id}/items", Summary: "Classify or create an item in a smart folder", Description: "Returns 201 when an item is created and 200 when an existing item is classified. Read access to a definition grants no write access to content.", Tags: tags,
		Responses: map[string]*huma.Response{"201": {Description: "Created", Headers: itemETag, Content: map[string]*huma.MediaType{"application/json": {Schema: resource}}}}},
		func(ctx context.Context, in *struct {
			SmartFolderPath
			Body dms.SmartFolderDrop
		}) (*Classified, error) {
			out, created, err := a.DMS.ClassifySmartFolder(ctx, subject(ctx), in.ID, in.Body)
			if err != nil {
				return nil, err
			}
			status := http.StatusOK
			if created {
				status = http.StatusCreated
			}
			return &Classified{Status: status, ETag: etag(out.Version), Body: resourceOut(out)}, nil
		})
	register(a, api, huma.Operation{OperationID: "unclassifySmartFolder", Method: http.MethodDelete, Path: "/v1/smart-folders/{id}/items/{itemID}", Summary: "Remove classification while preserving item name and location", Description: "Requires item If-Match and current folder_version. Clears matching equality values/tags and selected terms including descendants; ordinary validation and publication rules apply. Rolls back if the result still matches the definition.", Tags: tags},
		func(ctx context.Context, in *struct {
			SmartFolderPath
			ItemID string `path:"itemID" doc:"Item ID."`
			precondition
			Body dms.SmartFolderUnclassify
		}) (*Tagged[Resource], error) {
			out, err := a.DMS.UnclassifySmartFolder(ctx, subject(ctx), in.ID, in.ItemID, in.Body.FolderVersion, in.Version)
			return tagged(out, err, resourceConv)
		})
	register(a, api, huma.Operation{OperationID: "exportSmartFolders", Method: http.MethodGet, Path: "/v1/workspaces/{id}/smart-folders/export", Summary: "Export portable shared definitions (workspace manage required)", Tags: tags},
		func(ctx context.Context, in *struct{ WorkspacePath }) (*Tagged[dms.SmartFolderPackage], error) {
			out, version, err := a.DMS.ExportSmartFolders(ctx, subject(ctx), in.ID)
			if err != nil {
				return nil, err
			}
			if out.Folders == nil {
				out.Folders = []dms.PortableSmartFolder{}
			}
			return &Tagged[dms.SmartFolderPackage]{ETag: etag(version), Body: out}, nil
		})
	register(a, api, huma.Operation{OperationID: "importSmartFolders", Method: http.MethodPost, Path: "/v1/workspaces/{id}/smart-folders/import", Summary: "Atomically merge portable shared definitions (workspace manage required)", Description: "Uses the workspace ETag.", Tags: tags},
		func(ctx context.Context, in *struct {
			WorkspacePath
			precondition
			Body dms.SmartFolderPackage
		}) (*Tagged[SmartFolderImportResult], error) {
			out, err := a.DMS.ImportSmartFolders(ctx, subject(ctx), in.ID, in.Version, in.Body)
			if err != nil {
				return nil, err
			}
			data, err := convert(out.Data, smartFolderOut)
			return &Tagged[SmartFolderImportResult]{ETag: etag(out.WorkspaceVersion), Body: SmartFolderImportResult{Created: out.Created, Updated: out.Updated, WorkspaceVersion: out.WorkspaceVersion, Data: data}}, err
		})
}
