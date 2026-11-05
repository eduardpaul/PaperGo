// Package appcode compiles an app's TSX source into the code its runtime
// runs, and reads the parameters form the code declares. Saving an app and
// importing a workspace package both compile through it, so stored code
// always comes from its stored source.
package appcode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"papergo/internal/script"
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

func problem(format string, args ...any) error {
	return &script.BundleError{Problems: []string{fmt.Sprintf(format, args...)}}
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
// modules, and its default export is app({ render, parameters? }). Everything
// wrong with the source is a *script.BundleError.
func Compile(ctx context.Context, source string) (Compiled, error) {
	if strings.TrimSpace(source) == "" {
		return Compiled{}, problem("the app has no source")
	}
	if len(source) > MaxSource {
		return Compiled{}, problem("an app's source is limited to %d bytes", MaxSource)
	}
	artifact, err := script.Compile(script.Spec{
		Name: "app.tsx", Source: source, Namespace: "app", Entry: entry, Page: true,
		Modules: map[string]string{ModuleApp: runtimeModule(ModuleApp), ModuleReact: runtimeModule(ModuleReact), ModuleJSXRuntime: runtimeModule(ModuleJSXRuntime)},
		Refused: func(path string) string {
			return fmt.Sprintf("an app can import only %s, %s and %s, not %q", ModuleApp, ModuleReact, ModuleJSXRuntime, path)
		},
		EntryProblem: "the app's default export must be app({ render })",
	})
	if err != nil {
		return Compiled{}, err
	}
	schema, err := declarations(ctx, artifact.Code)
	if err != nil {
		return Compiled{}, err
	}
	return Compiled{Code: artifact.Code, Hash: artifact.Hash, ParametersSchema: schema}, nil
}

// stubs stand for the runtime's modules while the server reads an app's
// declarations: app() returns its definition, and React and the kit render
// nothing, since nothing renders. Classes stay classes, which code may extend
// as it loads.
const stubs = `(() => {
  const nothing = () => null;
  const names = (list, value) => Object.fromEntries(list.map((n) => [n, value]));
  const react = Object.assign(names(["createElement", "cloneElement", "createContext", "forwardRef", "memo", "lazy", "useState", "useEffect", "useLayoutEffect", "useMemo", "useCallback", "useRef", "useReducer", "useContext", "useId", "useTransition", "useDeferredValue", "useImperativeHandle", "useInsertionEffect", "useSyncExternalStore", "useDebugValue", "useActionState", "useOptimistic", "use", "createRef", "startTransition", "isValidElement"], nothing), { Fragment: "fragment", Suspense: "suspense", StrictMode: "strict", Children: {}, Component: class {}, PureComponent: class {} });
  const app = Object.assign(names(["useApp", "useQuery", "useAggregates", "useItem", "json", "plain", "Card", "Grid", "Gauge", "Stat", "LineChart", "BarChart", "Table", "Button", "NumberField", "TextField", "DateField", "DateTimeField", "Notice", "Legend", "Toolbar", "Modal", "useNarrow"], nothing), { app: (definition) => definition, models: {}, narrowWidth: 640, RequestError: class extends Error {} });
  globalThis.__papergoApp = { modules: { "react": react, "react/jsx-runtime": { jsx: nothing, jsxs: nothing, Fragment: "fragment" }, "@papergo/app": app } };
})();`

// declarationTimeout bounds reading an app's declarations.
const declarationTimeout = 2 * time.Second

// defaultParameters is the form of an app that declares none.
var defaultParameters = json.RawMessage(`{"type":"object","properties":{}}`)

// readDefinition reads what an app's default export declares, as JSON.
const readDefinition = `JSON.stringify((() => {
  const d = globalThis.__papergoAppDefinition;
  if (!d || typeof d !== "object" || typeof d.render !== "function") return { error: "the app's default export must be app({ render })" };
  if (d.parameters === undefined) return {};
  if (!d.parameters || typeof d.parameters !== "object" || Array.isArray(d.parameters)) return { error: "parameters must be a form: a JSON Schema object" };
  return { schema: d.parameters };
})())`

// declarations runs an app's code with stub modules and returns the
// parameters form its default export declares.
func declarations(ctx context.Context, code string) (json.RawMessage, error) {
	raw, err := script.Declare(ctx, script.Declarations{Prelude: stubs, Code: code, Read: readDefinition, Budget: declarationTimeout, TooLong: "the app took too long to declare itself"})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, problem("the app failed while it was loaded: %v", err)
	}
	var result struct {
		Error  string          `json:"error"`
		Schema json.RawMessage `json:"schema"`
	}
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, err
	}
	if result.Error != "" {
		return nil, problem("%s", result.Error)
	}
	if result.Schema == nil {
		return defaultParameters, nil
	}
	return result.Schema, nil
}
