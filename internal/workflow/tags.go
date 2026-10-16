package workflow

import "papergo/internal/dms"
import "papergo/internal/forms"

func init() {
	register(&Activity{Key: "tags.apply", Kind: "action", Description: "Adds tags while preserving existing tags. Requires the supplied file version to remain current.", InputSchema: inputs(itemIDProp+`,"tags":{"type":"array","title":"Tags","items":{"type":"string"}},"blob_id":{"type":"string","title":"Source file version"}`, "tags", "blob_id"), OutputSchema: forms.MustParse(itemOutput), run: runTagsApply})
}

func runTagsApply(e *env, in map[string]any) (outcome, error) {
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
	add, err := e.strings("tags", in["tags"])
	if err != nil {
		return outcome{}, err
	}
	tags := append([]string{}, r.Tags...)
	seen := map[string]bool{}
	for _, tag := range tags {
		seen[tag] = true
	}
	for _, tag := range add {
		if !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}
	if len(tags) == len(r.Tags) {
		return outcome{Output: snapshot(r)}, nil
	}
	updated, err := e.t.Update(e.ctx, e.author, id, r.Version, dms.UpdateResource{Tags: &tags})
	if err != nil {
		return outcome{}, err
	}
	return outcome{Output: snapshot(updated)}, nil
}
