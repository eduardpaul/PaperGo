package workflow

import "papergo/internal/dms"
import "papergo/internal/forms"

func init() {
	register(&Activity{Key: "keywords.apply", Kind: "action", Description: "Adds keywords while preserving existing keywords. Requires the supplied file version to remain current.", InputSchema: inputs(itemIDProp+`,"keywords":{"type":"array","title":"Keywords","items":{"type":"string"}},"blob_id":{"type":"string","title":"Source file version"}`, "keywords", "blob_id"), OutputSchema: forms.MustParse(itemOutput), run: runKeywordsApply})
}

func runKeywordsApply(e *env, in map[string]any) (outcome, error) {
	id, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	if _, err = e.t.Authorize(e.ctx, e.author, id, "write"); err != nil {
		return outcome{}, err
	}
	blobID, err := e.optionalText(in, "blob_id")
	if err != nil {
		return outcome{}, err
	}
	b, err := e.t.CurrentFile(e.ctx, e.author, id)
	if err != nil {
		return outcome{}, err
	}
	if blobID == "" || b.ID != blobID {
		return outcome{}, dms.ErrPreconditionFailed
	}
	r, err := e.t.GetSurface(e.ctx, e.author, id, "head")
	if err != nil {
		return outcome{}, err
	}
	add, err := e.strings("keywords", in["keywords"])
	if err != nil {
		return outcome{}, err
	}
	keywords := append([]string{}, r.Keywords...)
	seen := map[string]bool{}
	for _, keyword := range keywords {
		seen[keyword] = true
	}
	for _, keyword := range add {
		if !seen[keyword] {
			keywords = append(keywords, keyword)
			seen[keyword] = true
		}
	}
	if len(keywords) == len(r.Keywords) {
		return outcome{Output: snapshot(r)}, nil
	}
	updated, err := e.t.Update(e.ctx, e.author, id, r.Version, dms.UpdateResource{Keywords: &keywords})
	if err != nil {
		return outcome{}, err
	}
	return outcome{Output: snapshot(updated)}, nil
}
