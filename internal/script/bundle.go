// Package script runs workflow scripts: TypeScript or JavaScript modules
// whose default export is a @papergo/workflow workflow(). A run executes in
// a fresh goja runtime from the start each time. Every API request, platform
// action, clock reading and random value is a recorded step, so a replay
// returns recorded results instead of repeating them, and a run waiting for
// an action holds no runtime between slices.
package script

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// SDKModule is the module scripts import the workflow API from.
const SDKModule = "@papergo/workflow"

// Artifact is a bundled script, ready to run. Hash identifies its code.
type Artifact struct {
	Code string
	Hash string
}

// entry calls the engine's start function with the script's default export.
const entry = `import definition from "workflow:script";
import { __start } from "@papergo/workflow";
globalThis.__papergoMain = (runId, input) => __start(definition, runId, input);
`

// BundleError lists what is wrong with a script, one problem per line.
type BundleError struct{ Problems []string }

func (e *BundleError) Error() string { return strings.Join(e.Problems, "\n") }

// Bundle compiles a script into an artifact. A script is one file and
// imports only @papergo/workflow, which the engine supplies at run time.
func Bundle(name, source string) (Artifact, error) {
	if name == "" {
		name = "workflow.ts"
	}
	loader := api.LoaderTS
	if strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".mjs") {
		loader = api.LoaderJS
	}
	result := api.Build(api.BuildOptions{
		Stdin:          &api.StdinOptions{Contents: entry, Sourcefile: "entry.js", Loader: api.LoaderJS},
		Bundle:         true,
		Write:          false,
		Format:         api.FormatIIFE,
		Platform:       api.PlatformNeutral,
		Target:         api.ES2020,
		Charset:        api.CharsetUTF8,
		LogLevel:       api.LogLevelSilent,
		External:       []string{SDKModule},
		Sourcemap:      api.SourceMapInline,
		SourcesContent: api.SourcesContentInclude,
		Plugins: []api.Plugin{{
			Name: "workflow",
			Setup: func(build api.PluginBuild) {
				build.OnResolve(api.OnResolveOptions{Filter: `^workflow:script$`}, func(api.OnResolveArgs) (api.OnResolveResult, error) {
					return api.OnResolveResult{Path: name, Namespace: "workflow"}, nil
				})
				build.OnResolve(api.OnResolveOptions{Filter: `.*`, Namespace: "workflow"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					if args.Path == SDKModule {
						return api.OnResolveResult{Path: SDKModule, External: true}, nil
					}
					return api.OnResolveResult{}, fmt.Errorf("a workflow script can import only %s, not %q", SDKModule, args.Path)
				})
				build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: "workflow"}, func(api.OnLoadArgs) (api.OnLoadResult, error) {
					return api.OnLoadResult{Contents: &source, Loader: loader}, nil
				})
			},
		}},
	})
	if len(result.Errors) > 0 {
		problems := make([]string, len(result.Errors))
		for i, m := range result.Errors {
			problems[i] = message(m)
		}
		return Artifact{}, &BundleError{Problems: problems}
	}
	if len(result.OutputFiles) != 1 {
		return Artifact{}, errors.New("the bundler produced no script")
	}
	code := string(result.OutputFiles[0].Contents)
	sum := sha256.Sum256([]byte(code))
	return Artifact{Code: code, Hash: hex.EncodeToString(sum[:])}, nil
}

func message(m api.Message) string {
	if m.Location == nil {
		return m.Text
	}
	return fmt.Sprintf("%s:%d:%d: %s", m.Location.File, m.Location.Line, m.Location.Column+1, m.Text)
}
