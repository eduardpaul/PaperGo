package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
	"papergo/internal/script"
)

// manifests reads versions' definitions; it never runs a script.
var manifests = script.New(nil, 0)

// compile bundles a workflow script and reads its definition from its
// export with params, returning the definition and the version's content.
// The definition is checked for everything that needs no database.
func compile(ctx context.Context, source string, params map[string]any) (Definition, workflows.Content, error) {
	if strings.TrimSpace(source) == "" {
		return Definition{}, workflows.Content{}, dms.Invalid("source is required")
	}
	if len(source) > MaxSourceBytes {
		return Definition{}, workflows.Content{}, dms.Invalid(fmt.Sprintf("source is larger than %d bytes", MaxSourceBytes))
	}
	artifact, err := script.Bundle("workflow.ts", source)
	var problems *script.BundleError
	if errors.As(err, &problems) {
		return Definition{}, workflows.Content{}, dms.Invalid("source: " + problems.Error())
	}
	if err != nil {
		return Definition{}, workflows.Content{}, err
	}
	if params == nil {
		params = map[string]any{}
	}
	raw, err := manifests.Manifest(ctx, artifact, params)
	if err != nil {
		return Definition{}, workflows.Content{}, dms.Invalid("source: " + err.Error())
	}
	def, err := ParseDefinition(raw)
	if err != nil {
		return Definition{}, workflows.Content{}, err
	}
	if err = def.validate(); err != nil {
		return Definition{}, workflows.Content{}, err
	}
	normalized, err := json.Marshal(def)
	if err != nil {
		return Definition{}, workflows.Content{}, err
	}
	return def, workflows.Content{Definition: normalized, Source: source, Bundle: artifact.Code, BundleHash: artifact.Hash}, nil
}

// Artifact is the compiled script of a version.
func Artifact(v workflows.Version) script.Artifact {
	return script.Artifact{Code: v.Bundle, Hash: v.BundleHash}
}
