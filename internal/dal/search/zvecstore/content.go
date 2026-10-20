package zvecstore

import (
	"context"
	"errors"
	"math"
	"slices"
	"sort"
	"time"

	"papergo/internal/search"
)

func (s *Store) StageContent(ctx context.Context, item, blob, generation string, chunks []search.Chunk) error {
	if item == "" || blob == "" || generation == "" {
		return errors.New("content needs an item, file version and generation")
	}
	chunks = slices.Clone(chunks)
	return s.write(ctx, func() error {
		return s.mutation(func() error {
			parts := map[string]bool{}
			for _, c := range chunks {
				parts[c.Part] = true
			}
			published := false
			var remove []string
			for k, c := range s.byGeneration[generation] {
				if c.Generation == generation {
					published = published || c.Published
					if parts[c.Chunk.Part] {
						remove = append(remove, k)
						s.removeChunk(k)
					}
				}
			}
			if len(remove) > 0 {
				if err := s.handle.DeleteByFilter(in("row_key", remove)); err != nil {
					return err
				}
			}
			rows := []*row{}
			for _, c := range chunks {
				if c.InputHash == "" {
					c.InputHash = c.Hash()
				}
				stored := search.StoredChunk{ItemID: item, BlobID: blob, Generation: generation, Published: published, Chunk: c}
				s.putChunk(stored)
				rows = append(rows, s.sourceRow(search.Record{Chunk: &stored}))
			}
			if err := writeRows(s.handle, rows, false); err != nil {
				return err
			}
			return s.syncResources(map[string]bool{item: true}, true)
		})
	})
}

func (s *Store) PublishContent(ctx context.Context, item, blob, generation string) error {
	return s.write(ctx, func() error {
		return s.mutation(func() error {
			var remove []string
			rows := []*row{}
			for k, c := range s.byFile[[2]string{item, blob}] {
				if c.ItemID != item || c.BlobID != blob {
					continue
				}
				if c.Generation != generation {
					remove = append(remove, k)
					s.removeChunk(k)
				} else {
					c.Published = true
					s.putChunk(c)
					rows = append(rows, s.sourceRow(search.Record{Chunk: &c}))
				}
			}
			if len(remove) > 0 {
				if err := s.handle.DeleteByFilter(in("row_key", remove)); err != nil {
					return err
				}
			}
			if err := writeRows(s.handle, rows, false); err != nil {
				return err
			}
			return s.syncResources(map[string]bool{item: true}, true)
		})
	})
}

func (s *Store) DeleteContent(ctx context.Context, item string, keep []string) error {
	keep = slices.Clone(keep)
	return s.write(ctx, func() error {
		return s.mutation(func() error {
			var remove []string
			for k, c := range s.chunks {
				if c.ItemID == item && !slices.Contains(keep, c.BlobID) {
					remove = append(remove, k)
					s.removeChunk(k)
				}
			}
			if len(remove) > 0 {
				if err := s.handle.DeleteByFilter(in("row_key", remove)); err != nil {
					return err
				}
			}
			return s.syncResources(map[string]bool{item: true}, true)
		})
	})
}

func (s *Store) Content(ctx context.Context, item, blob string) (out search.Content, err error) {
	err = s.read(ctx, func() error {
		for _, c := range s.byFile[[2]string{item, blob}] {
			if c.ItemID == item && c.BlobID == blob && c.Published {
				out.Generation = c.Generation
				out.Chunks++
			}
		}
		return nil
	})
	return
}

func (s *Store) GenerationChunks(ctx context.Context, generation string) (out []search.StagedChunk, err error) {
	err = s.read(ctx, func() error {
		for _, c := range s.byGeneration[generation] {
			if c.Generation == generation {
				v := c.Chunk
				out = append(out, search.StagedChunk{Part: v.Part, Ordinal: v.Ordinal, Page: v.Page, Kind: v.Kind, Text: v.Text, Enrichment: v.Enrichment})
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Part != out[j].Part {
				return out[i].Part < out[j].Part
			}
			return out[i].Ordinal < out[j].Ordinal
		})
		return nil
	})
	return
}

func (s *Store) SetEnrichments(ctx context.Context, generation string, values []search.Enrichment) error {
	values = slices.Clone(values)
	return s.write(ctx, func() error {
		return s.mutation(func() error {
			ids := map[string]bool{}
			rows := []*row{}
			for _, c := range s.byGeneration[generation] {
				if c.Generation != generation {
					continue
				}
				for _, e := range values {
					if c.Chunk.Part == e.Part && c.Chunk.Ordinal == e.Ordinal {
						c.Chunk.Enrichment = e.Text
						c.Chunk.InputHash = c.Chunk.Hash()
						s.putChunk(c)
						ids[c.ItemID] = true
						rows = append(rows, s.sourceRow(search.Record{Chunk: &c}))
						break
					}
				}
			}
			if err := writeRows(s.handle, rows, false); err != nil {
				return err
			}
			return s.syncResources(ids, true)
		})
	})
}

func (s *Store) toEmbed(model string, include func(search.StoredChunk) bool, limit int) (out []search.ChunkToEmbed, total int) {
	seen := map[string]bool{}
	chunks := make([]search.StoredChunk, 0, len(s.chunks))
	for _, c := range s.chunks {
		chunks = append(chunks, c)
	}
	sort.Slice(chunks, func(i, j int) bool {
		if chunks[i].Generation != chunks[j].Generation {
			return chunks[i].Generation < chunks[j].Generation
		}
		if chunks[i].Chunk.Part != chunks[j].Chunk.Part {
			return chunks[i].Chunk.Part < chunks[j].Chunk.Part
		}
		return chunks[i].Chunk.Ordinal < chunks[j].Chunk.Ordinal
	})
	for _, c := range chunks {
		hash := c.Chunk.InputHash
		if !include(c) || seen[hash] {
			continue
		}
		seen[hash] = true
		total++
		if _, exists := s.vectors[vectorKey(model, hash)]; !exists && len(out) < limit {
			out = append(out, search.ChunkToEmbed{InputHash: hash, Input: search.EmbeddingInput(c.Chunk.Text, c.Chunk.Enrichment)})
		}
	}
	return
}

func (s *Store) GenerationToEmbed(ctx context.Context, model, generation string) (out []search.ChunkToEmbed, total int, err error) {
	err = s.read(ctx, func() error {
		out, total = s.toEmbed(model, func(c search.StoredChunk) bool { return c.Generation == generation }, int(^uint(0)>>1))
		return nil
	})
	return
}
func (s *Store) ChunksToEmbed(ctx context.Context, model string, limit int) (out []search.ChunkToEmbed, err error) {
	err = s.read(ctx, func() error {
		out, _ = s.toEmbed(model, func(c search.StoredChunk) bool { return c.Published }, max(limit, 0))
		return nil
	})
	return
}

func (s *Store) SetVectors(ctx context.Context, model string, vectors []search.HashVector) error {
	if len(vectors) == 0 {
		return nil
	}
	dims := len(vectors[0].Vector)
	if model == "" || dims == 0 || dims > 65536 {
		return errors.New("invalid embedding model or dimensions")
	}
	copyVectors := make([]search.HashVector, len(vectors))
	for i, v := range vectors {
		if len(v.Vector) != dims || v.InputHash == "" {
			return errors.New("inconsistent embedding dimensions or empty hash")
		}
		sum := float64(0)
		for _, x := range v.Vector {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return errors.New("embedding contains a non-finite number")
			}
			sum += float64(x) * float64(x)
		}
		if sum == 0 {
			return errors.New("embedding is zero")
		}
		copyVectors[i] = search.HashVector{InputHash: v.InputHash, Vector: slices.Clone(v.Vector)}
		for j := range copyVectors[i].Vector {
			copyVectors[i].Vector[j] /= float32(math.Sqrt(sum))
		}
	}
	return s.write(ctx, func() error {
		if s.meta.Model == model && s.meta.Dimensions != dims {
			return errors.New("inconsistent embedding model dimensions")
		}
		for _, v := range s.vectors {
			if v.Model == model && len(v.Vector) != dims {
				return errors.New("inconsistent embedding model dimensions")
			}
		}
		return s.mutation(func() error {
			if err := s.switchModel(model, dims); err != nil {
				return err
			}
			rows := []*row{}
			changed := map[string]bool{}
			for _, v := range copyVectors {
				if _, exists := s.vectors[vectorKey(model, v.InputHash)]; exists {
					continue
				}
				stored := search.StoredVector{Model: model, InputHash: v.InputHash, Vector: v.Vector}
				s.vectors[vectorKey(model, v.InputHash)] = stored
				rows = append(rows, s.sourceRow(search.Record{Vector: &stored}))
				changed[v.InputHash] = true
			}
			if err := writeRows(s.handle, rows, false); err != nil {
				return err
			}
			updates := []*row{}
			for pk, p := range s.passages {
				if changed[s.chunks[p.chunk].Chunk.InputHash] {
					if r := s.vectorRow(pk, p); r != nil {
						updates = append(updates, r)
					}
				}
			}
			return writeRows(s.vectorHandle, updates, false)
		})
	})
}

func (s *Store) CollectVectors(ctx context.Context, model string, now time.Time, keep time.Duration) (count int, err error) {
	err = s.write(ctx, func() error {
		return s.mutation(func() error {
			used := map[string]bool{}
			for _, c := range s.chunks {
				used[c.Chunk.InputHash] = true
			}
			rows := []*row{}
			remove := []string{}
			for k, v := range s.vectors {
				if v.Model == model && used[v.InputHash] {
					v.UnusedSince = nil
				} else if v.UnusedSince == nil {
					n := now.UTC()
					v.UnusedSince = &n
				} else if !v.UnusedSince.After(now.Add(-keep)) {
					remove = append(remove, k)
					delete(s.vectors, k)
					count++
					continue
				}
				s.vectors[k] = v
				rows = append(rows, s.sourceRow(search.Record{Vector: &v}))
			}
			if len(remove) > 0 {
				if err := s.handle.DeleteByFilter(in("row_key", remove)); err != nil {
					return err
				}
			}
			if err := writeRows(s.handle, rows, false); err != nil {
				return err
			}
			if len(remove) > 0 {
				ids := map[string]bool{}
				for _, d := range s.documents {
					ids[d.Document.ResourceID] = true
				}
				return s.syncResources(ids, true)
			}
			return nil
		})
	})
	return
}

func validateStoredVector(v search.StoredVector) error {
	if v.Model == "" || v.InputHash == "" || len(v.Vector) == 0 || len(v.Vector) > 65536 {
		return errors.New("invalid stored vector")
	}
	sum := float64(0)
	for _, x := range v.Vector {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return errors.New("invalid stored vector")
		}
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return errors.New("stored vector is zero")
	}
	return nil
}

func (s *Store) Embeddings(ctx context.Context, model string) (out search.EmbeddingStats, err error) {
	err = s.read(ctx, func() error {
		for _, v := range s.vectors {
			if v.Model == model {
				out.Vectors++
				out.MemoryBytes += int64(len(v.Vector) * 4)
			}
		}
		pending, _ := s.toEmbed(model, func(c search.StoredChunk) bool { return c.Published }, int(^uint(0)>>1))
		out.Pending = int64(len(pending))
		return nil
	})
	return
}
