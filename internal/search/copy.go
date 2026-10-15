package search

import (
	"context"
	"time"
)

// Record is one portable search-store record. Exactly one payload is set.
type Record struct {
	Document *Document     `json:"document,omitempty"`
	Stamp    int64         `json:"stamp,omitempty"`
	Chunk    *StoredChunk  `json:"chunk,omitempty"`
	Vector   *StoredVector `json:"vector,omitempty"`
}

type StoredChunk struct {
	ItemID, BlobID, Generation string
	Published                  bool
	Chunk                      Chunk
}

type StoredVector struct {
	Model, InputHash string
	Vector           []float32
	UnusedSince      *time.Time
}

// Copy transfers an offline store in bounded batches. Both stores are owned
// by the caller; neither provider nor file-reading tools are involved.
func Copy(ctx context.Context, from, to Store) error {
	batch := make([]Record, 0, 200)
	err := from.Export(ctx, func(r Record) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch = append(batch, r)
		if len(batch) < cap(batch) {
			return nil
		}
		if err := to.Import(ctx, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	})
	if err != nil {
		return err
	}
	if len(batch) > 0 {
		return to.Import(ctx, batch)
	}
	return nil
}
