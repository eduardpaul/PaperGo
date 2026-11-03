package search

import (
	"cmp"
	"container/heap"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"slices"
	"sync"
)

// VectorSource reads the stored vectors of a model, for a VectorIndex.
type VectorSource interface {
	// VectorState is how many vectors the model has and the newest stamp
	// among them.
	VectorState(ctx context.Context, model string) (count, newest int64, err error)
	// LoadVectors yields the model's vectors whose stamp is above after and
	// at most newest, in stamp order: the input hash, the dimensions and the
	// vector as VectorBytes encodes it.
	LoadVectors(ctx context.Context, model string, after, newest int64, yield func(hash string, dims int, raw []byte) error) error
}

// VectorIndex holds the vectors of one model in memory, a port of
// PaperDotNet's VectorIndex: searches scan an immutable snapshot without
// locks. Before each semantic search it checks the store (count and newest
// stamp of the model) and loads only what changed, so writes by any process
// are found. A count that disagrees after loading, as when a write with an
// older stamp committed late, loads everything again.
type VectorIndex struct {
	mu        sync.Mutex
	model     string
	dims      int
	hashes    []string
	data      []float32
	positions map[string]int
	count     int64
	newest    int64
}

// VectorSnapshot is a consistent view of the index. Later loads append past
// its length, so it never changes.
type VectorSnapshot struct {
	dims   int
	hashes []string
	data   []float32
}

func (s VectorSnapshot) vector(i int) []float32 { return s.data[i*s.dims : (i+1)*s.dims] }

// Snapshot returns the index of model, current with the store.
func (x *VectorIndex) Snapshot(ctx context.Context, src VectorSource, model string) (VectorSnapshot, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	count, newest, err := src.VectorState(ctx, model)
	if err != nil {
		return VectorSnapshot{}, err
	}
	if x.model != model || count < x.count {
		// Another model, or vectors were deleted: load everything again.
		x.model, x.dims, x.hashes, x.data, x.positions, x.count, x.newest = model, 0, nil, nil, map[string]int{}, 0, -1
	}
	if count != x.count || newest != x.newest {
		if err := x.load(ctx, src, newest); err != nil {
			return VectorSnapshot{}, err
		}
		if int64(len(x.hashes)) != count {
			x.dims, x.hashes, x.data, x.positions, x.newest = 0, nil, nil, map[string]int{}, -1
			if err := x.load(ctx, src, newest); err != nil {
				return VectorSnapshot{}, err
			}
		}
		x.count, x.newest = count, newest
	}
	return VectorSnapshot{dims: x.dims, hashes: x.hashes[:len(x.hashes):len(x.hashes)], data: x.data[:len(x.data):len(x.data)]}, nil
}

// load appends the model's vectors written after the newest loaded one, up
// to newest.
func (x *VectorIndex) load(ctx context.Context, src VectorSource, newest int64) error {
	return src.LoadVectors(ctx, x.model, x.newest, newest, func(hash string, dims int, raw []byte) error {
		if _, ok := x.positions[hash]; ok || len(raw) != 4*dims {
			return nil
		}
		if x.dims == 0 {
			x.dims = dims
		}
		if dims != x.dims {
			return nil
		}
		x.positions[hash] = len(x.hashes)
		x.hashes = append(x.hashes, hash)
		x.data = appendVector(x.data, raw)
		return nil
	})
}

func appendVector(data []float32, raw []byte) []float32 {
	for i := 0; i < len(raw); i += 4 {
		data = append(data, math.Float32frombits(binary.LittleEndian.Uint32(raw[i:])))
	}
	return data
}

// VectorBytes encodes a vector as stores keep it: float32, little-endian.
func VectorBytes(v []float32) []byte {
	raw := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(x))
	}
	return raw
}

// DecodeVector reads a vector VectorBytes encoded.
func DecodeVector(raw []byte) ([]float32, error) {
	if len(raw)%4 != 0 {
		return nil, errors.New("invalid vector bytes in search store")
	}
	return appendVector(make([]float32, 0, len(raw)/4), raw), nil
}

// dot is the dot product of two vectors of equal length, unrolled by 8.
func dot(a, b []float32) float32 {
	b = b[:len(a)]
	var s0, s1, s2, s3, s4, s5, s6, s7 float32
	i := 0
	for ; i+8 <= len(a); i += 8 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
		s4 += a[i+4] * b[i+4]
		s5 += a[i+5] * b[i+5]
		s6 += a[i+6] * b[i+6]
		s7 += a[i+7] * b[i+7]
	}
	for ; i < len(a); i++ {
		s0 += a[i] * b[i]
	}
	return s0 + s1 + s2 + s3 + s4 + s5 + s6 + s7
}

// Similar is a chunk input and its similarity to the query.
type Similar struct {
	Hash       string
	Similarity float32
}

type similarHeap []Similar

func (h similarHeap) Len() int           { return len(h) }
func (h similarHeap) Less(i, j int) bool { return h[i].Similarity < h[j].Similarity }
func (h similarHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *similarHeap) Push(x any)        { *h = append(*h, x.(Similar)) }
func (h *similarHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// Nearest returns the limit inputs most similar to the normalized query, at
// least minSimilarity, by brute force; allowed, when not nil, restricts the
// inputs.
func (s VectorSnapshot) Nearest(query []float32, limit int, minSimilarity float32, allowed map[string]bool) []Similar {
	if s.dims != len(query) || limit <= 0 {
		return nil
	}
	top := make(similarHeap, 0, limit)
	for i, hash := range s.hashes {
		if allowed != nil && !allowed[hash] {
			continue
		}
		similarity := dot(query, s.vector(i))
		if similarity < minSimilarity {
			continue
		}
		if len(top) < limit {
			heap.Push(&top, Similar{hash, similarity})
		} else if similarity > top[0].Similarity {
			top[0] = Similar{hash, similarity}
			heap.Fix(&top, 0)
		}
	}
	out := make([]Similar, len(top))
	for i := len(top) - 1; i >= 0; i-- {
		out[i] = heap.Pop(&top).(Similar)
	}
	return out
}

// PassageCandidates is how many chunk inputs a semantic search takes from
// the index before it filters them, as PaperDotNet's.
const PassageCandidates = 1000

// Fuse merges a keyword and a semantic list of documents, each best first,
// by reciprocal rank: the fused order, best first with ties by key, and
// each document's score.
func Fuse[K cmp.Ordered](keyword, semantic []K) ([]K, map[K]float64) {
	scores := map[K]float64{}
	for i, id := range keyword {
		scores[id] += 1 / float64(FusionK+i+1)
	}
	for i, id := range semantic {
		scores[id] += 1 / float64(FusionK+i+1)
	}
	ordered := make([]K, 0, len(scores))
	for id := range scores {
		ordered = append(ordered, id)
	}
	slices.SortFunc(ordered, func(a, b K) int {
		if c := cmp.Compare(scores[b], scores[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	return ordered, scores
}
