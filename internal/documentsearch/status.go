package documentsearch

import (
	"context"

	"papergo/internal/dms"
	"papergo/internal/search"
)

// Status describes the search store, for organization managers.
type Status struct {
	Store      string `json:"store"`
	Rebuilding bool   `json:"rebuilding"`
	Documents  int64  `json:"documents"`
	// Chunks counts the published chunks of file content.
	Chunks int64 `json:"chunks"`
	Bytes  int64 `json:"bytes"`
	// Model is the embedding model of semantic search, empty when none is
	// configured or it is unavailable; Embeddings describes its vectors.
	Model      string                 `json:"model,omitempty"`
	Embeddings *search.EmbeddingStats `json:"embeddings,omitempty"`
}

// Status reports the search store's state.
func (x *Search) Status(ctx context.Context, s *dms.Service, subject string) (Status, error) {
	if err := s.RequireScope(ctx, subject, "organization.manage"); err != nil {
		return Status{}, err
	}
	st, err := x.Store.Stats(ctx)
	if err != nil {
		return Status{}, err
	}
	out := Status{Store: x.Store.Name(), Rebuilding: x.rebuilding.Load(), Documents: st.Documents, Chunks: st.Chunks, Bytes: st.Bytes}
	// A model that cannot name its vector space now (a provider measuring
	// its dimensions is down) is left out rather than failing the status.
	if x.Embeddings == nil {
		return out, nil
	}
	if model, err := x.Embeddings.Key(ctx); err == nil && model != "" {
		out.Model = model
		vectors, err := x.Store.Embeddings(ctx, out.Model)
		if err != nil {
			return Status{}, err
		}
		out.Embeddings = &vectors
	}
	return out, nil
}

// storage reports the search store in the storage status.
func (x *Search) storage(ctx context.Context) (dms.StorageUsage, error) {
	st, err := x.Store.Stats(ctx)
	if err != nil {
		return dms.StorageUsage{}, err
	}
	return dms.StorageUsage{Name: "search", Store: x.Store.Name(), Bytes: st.Bytes, Entries: st.Documents}, nil
}
