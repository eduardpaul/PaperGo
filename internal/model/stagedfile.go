package model

// StagedSource is a file version a staged file was made from.
type StagedSource struct {
	ItemID string `json:"item_id"`
	BlobID string `json:"blob_id"`
}
