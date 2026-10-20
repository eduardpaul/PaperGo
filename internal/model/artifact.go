package model

// ArtifactSource is a file version an artifact was made from.
type ArtifactSource struct {
	ItemID string `json:"item_id"`
	BlobID string `json:"blob_id"`
}
