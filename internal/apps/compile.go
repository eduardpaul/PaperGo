// Package apps is the apps feature: React screens that belong to a
// workspace, written as one TSX file the server compiles, with parameters
// chosen through a form and immutable versions. An app runs in a sandboxed
// frame of the web app and acts as the person viewing it.
package apps

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

// Modules an app may import; the runtime supplies them.
const (
	ModuleApp        = "@papergo/app"
	ModuleReact      = "react"
	ModuleJSXRuntime = "react/jsx-runtime"
)

// MaxSource bounds an app's source.
const MaxSource = 1 << 20

// Compiled is an app compiled for the runtime: its code, the code's hash and
// the parameters form the source declares.
type Compiled struct {
	Code, Hash       string
	ParametersSchema json.RawMessage
}

// CompileError lists what is wrong with a source, one problem per line.
type CompileError struct{ Problems []string }

func (e *CompileError) Error() string { return strings.Join(e.Problems, "\n") }

func problem(format string, args ...any) error {
	return &CompileError{Problems: []string{fmt.Sprintf(format, args...)}}
}

// entry hands the runtime the app's default export.
const entry = `import definition from "app:source";
globalThis.__papergoAppDefinition = definition;
`

// The runtime defines globalThis.__papergoApp.modules before the app's code
// runs; a bundle reads the modules it imports from there.
func runtimeModule(name string) string {
	return fmt.Sprintf("module.exports = globalThis.__papergoApp.modules[%q];", name)
}

// Compile compiles an app's TSX source. It imports only the runtime's
// modules, and its default export is app({ render, parameters? }).
func Compile(source string) (Compiled, error) {
	if strings.TrimSpace(source) == "" {
		return Compiled{}, problem("the app has no source")
	}
	if len(source) > MaxSource {
		return Compiled{}, problem("an app's source is limited to %d bytes", MaxSource)
	}
	result := api.Build(api.BuildOptions{
		Stdin:    &api.StdinOptions{Contents: entry, Sourcefile: "entry.js", Loader: api.LoaderJS},
		Bundle:   true,
		Write:    false,
		Format:   api.FormatIIFE,
		Platform: api.PlatformBrowser,
		Target:   api.ES2020,
		Charset:  api.CharsetUTF8,
		LogLevel: api.LogLevelSilent,
		// The page inlines the code in the frame's document.
		Supported:       map[string]bool{"inline-script": true},
		JSX:             api.JSXAutomatic,
		JSXImportSource: ModuleReact,
		Sourcemap:       api.SourceMapInline,
		SourcesContent:  api.SourcesContentInclude,
		Plugins: []api.Plugin{{
			Name: "app",
			Setup: func(build api.PluginBuild) {
				build.OnResolve(api.OnResolveOptions{Filter: `^app:source$`}, func(api.OnResolveArgs) (api.OnResolveResult, error) {
					return api.OnResolveResult{Path: "app.tsx", Namespace: "app"}, nil
				})
				build.OnResolve(api.OnResolveOptions{Filter: `.*`}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					switch args.Path {
					case ModuleApp, ModuleReact, ModuleJSXRuntime:
						return api.OnResolveResult{Path: args.Path, Namespace: "runtime"}, nil
					}
					return api.OnResolveResult{}, fmt.Errorf("an app can import only %s, %s and %s, not %q", ModuleApp, ModuleReact, ModuleJSXRuntime, args.Path)
				})
				build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: "app"}, func(api.OnLoadArgs) (api.OnLoadResult, error) {
					return api.OnLoadResult{Contents: &source, Loader: api.LoaderTSX}, nil
				})
				build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: "runtime"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
					code := runtimeModule(args.Path)
					return api.OnLoadResult{Contents: &code, Loader: api.LoaderJS}, nil
				})
			},
		}},
	})
	if len(result.Errors) > 0 {
		problems := make([]string, len(result.Errors))
		for i, m := range result.Errors {
			problems[i] = message(m)
			if m.Location != nil && m.Location.File == "entry.js" {
				problems[i] = "the app's default export must be app({ render })"
			}
		}
		return Compiled{}, &CompileError{Problems: problems}
	}
	if len(result.OutputFiles) != 1 {
		return Compiled{}, errors.New("the compiler produced no code")
	}
	code := string(result.OutputFiles[0].Contents)
	schema, err := declarations(code)
	if err != nil {
		return Compiled{}, err
	}
	sum := sha256.Sum256([]byte(code))
	return Compiled{Code: code, Hash: hex.EncodeToString(sum[:]), ParametersSchema: schema}, nil
}

func message(m api.Message) string {
	if m.Location == nil {
		return m.Text
	}
	return fmt.Sprintf("%s:%d:%d: %s", m.Location.File, m.Location.Line, m.Location.Column+1, m.Text)
}

// stubs stand for the runtime's modules while the server reads an app's
// declarations: app() returns its definition, and React and the kit render
// nothing, since nothing renders.
const stubs = `(() => {
  const nothing = () => null;
  const names = (list, value) => Object.fromEntries(list.map((n) => [n, value]));
  const react = Object.assign(names(["createElement", "cloneElement", "createContext", "forwardRef", "memo", "lazy", "useState", "useEffect", "useLayoutEffect", "useMemo", "useCallback", "useRef", "useReducer", "useContext", "useId", "useTransition", "useDeferredValue", "startTransition", "isValidElement"], nothing), { Fragment: "fragment", Suspense: "suspense", StrictMode: "strict", Children: {} });
  const app = Object.assign(names(["useApp", "useQuery", "useItem", "json", "plain", "Card", "Grid", "Gauge", "Stat", "LineChart", "BarChart", "Table", "Button", "NumberField", "TextField", "DateField", "DateTimeField", "Notice", "Legend", "Toolbar", "Modal", "RequestError"], nothing), { app: (definition) => definition, models: {} });
  globalThis.__papergoApp = { modules: { "react": react, "react/jsx-runtime": { jsx: nothing, jsxs: nothing, Fragment: "fragment" }, "@papergo/app": app } };
})();`

// declarationTimeout bounds reading an app's declarations.
const declarationTimeout = 2 * time.Second

// defaultParameters is the form of an app that declares none.
var defaultParameters = json.RawMessage(`{"type":"object","properties":{}}`)

// declarations runs an app's code with stub modules and returns the
// parameters form its default export declares.
func declarations(code string) (json.RawMessage, error) {
	vm := goja.New()
	timer := time.AfterFunc(declarationTimeout, func() { vm.Interrupt("the app took too long to declare itself") })
	defer timer.Stop()
	if _, err := vm.RunString(stubs); err != nil {
		return nil, err
	}
	if _, err := vm.RunString(code); err != nil {
		return nil, problem("the app failed while it was loaded: %v", err)
	}
	out, err := vm.RunString(`(() => {
  const d = globalThis.__papergoAppDefinition;
  if (!d || typeof d !== "object" || typeof d.render !== "function") return { error: "the app's default export must be app({ render })" };
  if (d.parameters === undefined) return { schema: null };
  if (!d.parameters || typeof d.parameters !== "object" || Array.isArray(d.parameters)) return { error: "parameters must be a form: a JSON Schema object" };
  return { schema: JSON.stringify(d.parameters) };
})()`)
	if err != nil {
		return nil, problem("the app failed while it was loaded: %v", err)
	}
	var result struct {
		Error  string  `json:"error"`
		Schema *string `json:"schema"`
	}
	raw, err := json.Marshal(out.Export())
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.Error != "" {
		return nil, problem("%s", result.Error)
	}
	if result.Schema == nil {
		return defaultParameters, nil
	}
	return json.RawMessage(*result.Schema), nil
}
