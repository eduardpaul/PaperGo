package zvecstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	zvec "github.com/zvec-ai/zvec-go"
)

var initialize = sync.OnceValue(func() error {
	if version := zvec.GetVersion(); version != "v0.7.0" {
		return fmt.Errorf("zvec version %q does not match pinned v0.7.0", version)
	}
	config := zvec.NewConfigData()
	if config == nil {
		return errors.New("zvec could not allocate configuration")
	}
	defer config.Destroy()
	// Native warning logs can include a document's complete payload.
	for _, configure := range []func() error{
		func() error { return config.SetConsoleLog(zvec.LogLevelError) },
		// This limits the SDK's buffer pool, not all native allocations. The
		// SDK's default follows the host/container budget; never exceed it.
		func() error { return config.SetMemoryLimit(min(config.GetMemoryLimit(), 128<<20)) },
		func() error { return config.SetQueryThreadCount(2) },
		func() error { return config.SetOptimizeThreadCount(2) },
	} {
		if err := configure(); err != nil {
			return err
		}
	}
	return zvec.Initialize(config)
})

const (
	typeString  = zvec.DataTypeString
	typeInt64   = zvec.DataTypeInt64
	typeVector  = zvec.DataTypeVectorFP32
	typeStrings = zvec.DataTypeArrayString
)

type column struct {
	name  string
	kind  zvec.DataType
	index uint32
}

func createCollection(path string, dimensions int) (*zvec.Collection, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	schema := zvec.NewCollectionSchema("papergo_search")
	if schema == nil {
		return nil, errors.New("zvec could not allocate collection schema")
	}
	defer schema.Destroy()
	columns := []column{
		{"row_key", typeString, 10}, {"document_key", typeString, 10},
		{"row_kind", typeString, 10}, {"resource_id", typeString, 10}, {"surface", typeString, 10}, {"scope_id", typeString, 10},
		{"workspace_id", typeString, 10}, {"collection_id", typeString, 10}, {"content_type_id", typeString, 10},
		{"created_by", typeString, 10}, {"modified_by", typeString, 10}, {"kind", typeString, 10},
		{"views", typeInt64, 10}, {"modified_at", typeInt64, 10}, {"tokens", typeStrings, 10},
		{"payload", typeString, 0}, {"content", typeString, 11}, {"metadata", typeString, 11},
	}
	if dimensions > 0 {
		// Vector collections contain only embedded passage rows. Sparse/null
		// vectors in mixed source collections are unsafe after optimization.
		columns = columns[:len(columns)-2]
		columns = append(columns, column{"embedding", typeVector, 1})
	}
	for _, c := range columns {
		dim := uint32(0)
		if c.kind == typeVector {
			dim = uint32(dimensions)
		}
		field := zvec.NewFieldSchema(c.name, c.kind, c.kind != typeVector, dim)
		if field == nil {
			return nil, errors.New("zvec could not allocate field schema")
		}
		err := func() error {
			defer field.Destroy()
			var params *zvec.IndexParams
			var err error
			switch c.index {
			case 10:
				params, err = zvec.NewInvertIndexParams(true, false)
			case 11:
				params, err = zvec.NewFTSIndexParams("whitespace", nil, "")
			case 1:
				params, err = zvec.NewHNSWIndexParams(zvec.MetricTypeCosine, 16, 100)
			}
			if err != nil {
				return err
			}
			if params != nil {
				defer params.Destroy()
				if err = field.SetIndexParams(params); err != nil {
					return err
				}
			}
			return schema.AddField(field)
		}()
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", c.name, err)
		}
	}
	return zvec.CreateAndOpen(path, schema, nil)
}

type row struct {
	doc *zvec.Doc
	err error
}

func newRow(key string) *row {
	r := &row{doc: zvec.NewDoc()}
	if r.doc == nil {
		r.err = errors.New("zvec could not allocate document")
	} else {
		r.doc.SetPK(key)
	}
	return r
}
func (r *row) text(name, value string) {
	if r.err == nil {
		r.err = r.doc.AddStringField(name, value)
	}
}
func (r *row) number(name string, value int64) {
	if r.err == nil {
		r.err = r.doc.AddInt64Field(name, value)
	}
}
func (r *row) array(name string, values []string) {
	if r.err == nil {
		r.err = r.doc.AddStringArrayField(name, values)
	}
}
func (r *row) vector(values []float32) {
	if r.err == nil && len(values) > 0 {
		r.err = r.doc.AddVectorFP32Field("embedding", values)
	}
}
func (r *row) close() {
	if r.doc != nil {
		r.doc.Destroy()
		r.doc = nil
	}
}

func writeRows(collection *zvec.Collection, rows []*row, update bool) error {
	defer func() {
		for _, r := range rows {
			r.close()
		}
	}()
	for _, r := range rows {
		if r.err != nil {
			return r.err
		}
	}
	for start := 0; start < len(rows); start += 200 {
		batch := rows[start:min(start+200, len(rows))]
		docs := make([]*zvec.Doc, len(batch))
		for i, r := range batch {
			docs[i] = r.doc
		}
		write := collection.Upsert
		if update {
			write = collection.Update
		}
		result, err := write(docs)
		if err != nil {
			return err
		}
		if result.ErrorCount != 0 || result.SuccessCount != uint64(len(docs)) {
			return fmt.Errorf("zvec batch wrote %d of %d rows (%d failed)", result.SuccessCount, len(docs), result.ErrorCount)
		}
	}
	return nil
}

type nativeHit struct {
	key   string
	score float32
}

func nativeQuery(collection *zvec.Collection, field, text, filter string, vector []float32, limit int) ([]nativeHit, error) {
	query := zvec.NewSearchQuery()
	if query == nil {
		return nil, errors.New("zvec could not allocate query")
	}
	defer query.Destroy()
	for _, configure := range []func() error{
		func() error { return query.SetFieldName(field) },
		func() error { return query.SetTopK(min(max(limit, 1), 100000)) },
		func() error { return query.SetFilter(filter) },
	} {
		if err := configure(); err != nil {
			return nil, err
		}
	}
	if vector != nil {
		if err := query.SetQueryVector(vector); err != nil {
			return nil, err
		}
	} else {
		fts := zvec.NewFTS()
		if fts == nil {
			return nil, errors.New("zvec could not allocate FTS query")
		}
		defer fts.Destroy()
		if err := fts.SetQueryString(text); err != nil {
			return nil, err
		}
		if err := query.SetFTS(fts); err != nil {
			return nil, err
		}
	}
	docs, err := collection.Query(query)
	if err != nil {
		return nil, err
	}
	defer zvec.FreeDocs(docs)
	if len(docs) > 100000 {
		return nil, errors.New("zvec returned too many candidates")
	}
	out := make([]nativeHit, len(docs))
	for i, doc := range docs {
		out[i] = nativeHit{doc.GetPK(), doc.GetScore()}
	}
	return out, nil
}
func nativePayload(doc *zvec.Doc) ([]byte, error) {
	payload, err := doc.GetStringField("payload")
	if err != nil {
		return nil, err
	}
	if len(payload) > 512<<20 {
		return nil, errors.New("zvec record exceeds 512 MiB")
	}
	return []byte(payload), nil
}
