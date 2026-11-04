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

// sdkModule stands for @papergo/workflow inside a bundle.
const sdkModule = "module.exports = globalThis.__papergoSdk;"

// entry gives the engine the SDK's start and manifest functions, bound to
// the script's default export.
const entry = `import definition from "workflow:source";
import { __start, __manifest } from "@papergo/workflow";
globalThis.__papergoMain = (run) => __start(definition, run);
globalThis.__papergoManifest = (params) => __manifest(definition, params);
`

// BundleError lists what is wrong with a source, one problem per line.
type BundleError struct{ Problems []string }

func (e *BundleError) Error() string { return strings.Join(e.Problems, "\n") }

// Spec says how to compile one source file into one script.
type Spec struct {
	// Name is the source's file name, whose extension picks its language.
	Name, Source string
	// Namespace names the source in the bundle. Entry imports it as
	// "<namespace>:source".
	Namespace, Entry string
	// Modules are what the source may import, each with the code that stands
	// for it in the bundle.
	Modules map[string]string
	// Refused says why the source may not import a module.
	Refused func(path string) string
	// EntryProblem replaces a problem found in Entry, which the source's
	// author did not write. Empty keeps the compiler's own.
	EntryProblem string
	// Page compiles for a browser page, not the engine: JSX with React, code
	// that can be inlined in a document, and no source map, so the code does
	// not carry its source.
	Page bool
}

func loader(name string) api.Loader {
	switch {
	case strings.HasSuffix(name, ".js"), strings.HasSuffix(name, ".mjs"):
		return api.LoaderJS
	case strings.HasSuffix(name, ".tsx"):
		return api.LoaderTSX
	}
	return api.LoaderTS
}

// Compile compiles a source into an artifact: one file, importing only
// spec.Modules. Everything wrong with the source is a *BundleError.
func Compile(spec Spec) (Artifact, error) {
	sourceImport := spec.Namespace + ":source"
	options := api.BuildOptions{
		Stdin:    &api.StdinOptions{Contents: spec.Entry, Sourcefile: "entry.js", Loader: api.LoaderJS},
		Bundle:   true,
		Write:    false,
		Format:   api.FormatIIFE,
		Target:   api.ES2020,
		Charset:  api.CharsetUTF8,
		LogLevel: api.LogLevelSilent,
		Plugins: []api.Plugin{{
			Name: "papergo",
			Setup: func(build api.PluginBuild) {
				build.OnResolve(api.OnResolveOptions{Filter: `.*`}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					if args.Path == sourceImport {
						return api.OnResolveResult{Path: spec.Name, Namespace: spec.Namespace}, nil
					}
					if _, ok := spec.Modules[args.Path]; ok {
						return api.OnResolveResult{Path: args.Path, Namespace: "module"}, nil
					}
					return api.OnResolveResult{}, errors.New(spec.Refused(args.Path))
				})
				build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: spec.Namespace}, func(api.OnLoadArgs) (api.OnLoadResult, error) {
					return api.OnLoadResult{Contents: &spec.Source, Loader: loader(spec.Name)}, nil
				})
				build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: "module"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
					code := spec.Modules[args.Path]
					return api.OnLoadResult{Contents: &code, Loader: api.LoaderJS}, nil
				})
			},
		}},
	}
	if spec.Page {
		options.Platform = api.PlatformBrowser
		options.Supported = map[string]bool{"inline-script": true}
		options.JSX = api.JSXAutomatic
		options.JSXImportSource = "react"
	} else {
		options.Platform = api.PlatformNeutral
		options.Sourcemap = api.SourceMapInline
		options.SourcesContent = api.SourcesContentInclude
	}
	result := api.Build(options)
	if len(result.Errors) > 0 {
		problems := make([]string, len(result.Errors))
		for i, m := range result.Errors {
			problems[i] = message(m)
			if spec.EntryProblem != "" && m.Location != nil && m.Location.File == "entry.js" {
				problems[i] = spec.EntryProblem
			}
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

// Bundle compiles a workflow script into an artifact. A script is one file
// and imports only @papergo/workflow, which the engine supplies at run time:
// the engine runs the SDK first, and it defines __papergoSdk.
func Bundle(name, source string) (Artifact, error) {
	if name == "" {
		name = "workflow.ts"
	}
	return Compile(Spec{
		Name: name, Source: source, Namespace: "workflow", Entry: entry,
		Modules: map[string]string{SDKModule: sdkModule},
		Refused: func(path string) string {
			return fmt.Sprintf("a workflow script can import only %s, not %q", SDKModule, path)
		},
	})
}

func message(m api.Message) string {
	if m.Location == nil {
		return m.Text
	}
	return fmt.Sprintf("%s:%d:%d: %s", m.Location.File, m.Location.Line, m.Location.Column+1, m.Text)
}
