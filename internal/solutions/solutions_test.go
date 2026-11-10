package solutions

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

const tankWorkflow = `import { workflow } from "@papergo/workflow";
export default workflow<unknown, unknown, { list: string; reading_type: string }>({
  triggers: (p) => [{ type: "item.created", collection_id: p.list, content_type_id: p.reading_type }],
  async run() {},
});
`

const tankApp = `import { app } from "@papergo/app";
export default app({
  parameters: {
    type: "object",
    required: ["list", "reading"],
    properties: {
      list: { type: "string", title: "List", "x-papergo": { kind: "collection" } },
      reading: { type: "string", title: "Reading type", "x-papergo": { kind: "content_type" } },
      capacity_l: { type: "number", title: "Capacity" },
    },
  },
  render() { return null; },
});
`

// tank is a small solution without problems.
func tank() fstest.MapFS {
	return fstest.MapFS{
		"solution.json": {Data: []byte(`{
  "key": "tank", "name": "Tank", "version": "1.0.0", "papergo": ">=0.1.0",
  "requires": {"ocr": false},
  "settings": {"type": "object", "properties": {
    "capacity_l": {"type": "number", "title": "Capacity", "default": 750},
    "time_zone": {"type": "string", "title": "Time zone", "default": "=organization.time_zone"}}},
  "fields": [
    {"key": "tank_measured_at", "label": "Measured at", "type": "datetime", "required": true, "indexed": true},
    {"key": "tank_flag", "label": "Check", "type": "choice", "choices": ["ok", "check"]}],
  "content_types": [{"key": "tank_reading", "name": "Reading", "fields": ["tank_measured_at", "tank_flag"]}],
  "collections": [{"key": "readings", "kind": "list", "name": "Tank", "content_types": ["tank_reading"], "search": false,
    "views": [{"key": "all", "name": "Readings", "layout": "table", "is_default": true, "columns": ["$name", "tank_measured_at"],
      "query": {"content_type_id": "$type:tank_reading", "sort": [{"field": "tank_measured_at", "direction": "desc"}]}}]}]
}`)},
		"workflows/levels.json": {Data: []byte(`{"key": "tank-levels", "name": "Levels", "script": "levels.ts", "state": "published",
  "parameters": {"list": "$collection:readings", "reading_type": "$type:tank_reading", "time_zone": "$setting:time_zone"}}`)},
		"workflows/levels.ts": {Data: []byte(tankWorkflow)},
		"apps/dashboard.json": {Data: []byte(`{"key": "tank-dashboard", "name": "Dashboard", "source": "dashboard.tsx", "state": "published",
  "parameters": {"list": "$collection:readings", "reading": "$type:tank_reading", "capacity_l": "$setting:capacity_l"}}`)},
		"apps/dashboard.tsx": {Data: []byte(tankApp)},
		"README.md":          {Data: []byte("not part of the package")},
	}
}

func check(t *testing.T, files fstest.MapFS) (Solution, Problems) {
	t.Helper()
	s, problems := Load(files)
	return s, append(problems, Check(t.Context(), s)...)
}

func TestSolutionWithoutProblemsPacksTheSameEveryTime(t *testing.T) {
	s, problems := check(t, tank())
	if len(problems) > 0 {
		t.Fatalf("problems:\n%s", problems)
	}
	if s.FileName() != "tank-1.0.0.zip" {
		t.Fatal(s.FileName())
	}
	first, err := Pack(s)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := Pack(s)
	if !bytes.Equal(first, second) {
		t.Fatal("the same solution packed to different bytes")
	}
	files, err := OpenZIP(first)
	if err != nil {
		t.Fatal(err)
	}
	again, problems := Load(files)
	if len(problems) > 0 {
		t.Fatalf("the packed solution has problems:\n%s", problems)
	}
	if !reflect.DeepEqual(s, again) {
		t.Fatalf("the packed solution reads differently:\n%+v\n%+v", s, again)
	}
	if _, err := files.Open("README.md"); err == nil {
		t.Fatal("a file that is no part of the solution was packed")
	}
}

// edit returns the tank with one file's text changed.
func edit(file, old, new string) fstest.MapFS {
	files := tank()
	text := string(files[file].Data)
	if !strings.Contains(text, old) {
		panic(file + " does not hold " + old)
	}
	files[file] = &fstest.MapFile{Data: []byte(strings.Replace(text, old, new, 1))}
	return files
}

func TestProblems(t *testing.T) {
	foreign := `{"key": "fills", "kind": "list", "use": {"solution": "diesel", "collection": "tank", "version": ">=1.2.0"},
      "expects": {"diesel_purchase": ["diesel_total_price"]}, "extends": {"diesel_purchase": ["tank_flag"]}}`
	cases := []struct {
		name  string
		files fstest.MapFS
		want  string // part of a problem; empty for none
	}{
		{"a collection of another solution", edit("solution.json", `"collections": [`, `"collections": [`+foreign+`,`), ""},
		{"an unknown property", edit("solution.json", `"name": "Tank",`, `"name": "Tank", "colour": "red",`), `unknown field "colour"`},
		{"a key with a separator", edit("solution.json", `"key": "tank", "name"`, `"key": "my-tank", "name"`), `"my-tank" must be 2 to 32 lowercase letters and digits`},
		{"a version that is none", edit("solution.json", `"version": "1.0.0"`, `"version": "1.0"`), `must be MAJOR.MINOR.PATCH`},
		{"a range that is none", edit("solution.json", `">=0.1.0"`, `"newer"`), `is not a range of versions`},
		{"an unknown capability", edit("solution.json", `{"ocr": false}`, `{"scanner": true}`), `requires.scanner: is not a capability`},
		{"an unknown organization preference", edit("solution.json", `=organization.time_zone`, `=organization.weather`), `"weather" is not an organization preference`},
		{"a field without the solution's key", edit("solution.json", `{"key": "tank_flag"`, `{"key": "flag"`), `"flag" must start with "tank_"`},
		{"a field declared twice", edit("solution.json", `{"key": "tank_flag"`, `{"key": "tank_measured_at"`), `"tank_measured_at" is declared twice`},
		{"a type that is no field type", edit("solution.json", `"type": "datetime"`, `"type": "moment"`), `"moment" is not a field type`},
		{"choices on a field that is no choice", edit("solution.json", `"type": "choice"`, `"type": "text"`), `only a choice field does`},
		{"a content type with another's field", edit("solution.json", `["tank_measured_at", "tank_flag"]`, `["tank_measured_at", "diesel_level"]`), `"diesel_level" is not a field of the solution`},
		{"a collection of an unknown type", edit("solution.json", `"content_types": ["tank_reading"]`, `"content_types": ["tank_fill"]`), `"tank_fill" is not a content type of the solution`},
		{"a collection of neither kind", edit("solution.json", `"kind": "list", "name": "Tank"`, `"kind": "folder", "name": "Tank"`), `"folder" must be list or library`},
		{"a view of a field the types lack", edit("solution.json", `["$name", "tank_measured_at"]`, `["$name", "tank_level"]`), `"tank_level" is not a field of the collection's content types`},
		{"a view of an unknown type", edit("solution.json", `"$type:tank_reading", "sort"`, `"$type:tank_fill", "sort"`), `$type:tank_fill is neither a content type of the solution`},
		{"a view query with an unknown property", edit("solution.json", `"sort": [`, `"limit": 5, "sort": [`), `unknown field "limit"`},
		{"an ID in the manifest", edit("solution.json", `"$type:tank_reading", "sort"`, `"2776cb2e-3166-4fc7-bd94-fa0adb98d6bb", "sort"`), `holds the ID 2776cb2e-3166-4fc7-bd94-fa0adb98d6bb`},
		{"expecting the solution's own type", edit("solution.json", `"collections": [`, `"collections": [`+strings.Replace(foreign, `"expects": {"diesel_purchase"`, `"expects": {"tank_reading"`, 1)+`,`), `must be a content type of another solution`},
		{"extending a type that is not expected", edit("solution.json", `"collections": [`, `"collections": [`+strings.Replace(foreign, `"extends": {"diesel_purchase"`, `"extends": {"diesel_reading"`, 1)+`,`), `is not a content type the collection is expected to have`},
		{"using its own collection", edit("solution.json", `"collections": [`, `"collections": [`+strings.Replace(foreign, `"solution": "diesel"`, `"solution": "tank"`, 1)+`,`), `must be the key of another solution`},
		{"a workflow without the solution's key", edit("workflows/levels.json", `"tank-levels"`, `"levels"`), `"levels" must start with "tank-"`},
		{"a workflow naming an unknown collection", edit("workflows/levels.json", `$collection:readings`, `$collection:fills`), `$collection:fills is not a collection of the solution`},
		{"a workflow naming an unknown setting", edit("workflows/levels.json", `$setting:time_zone`, `$setting:zone`), `$setting:zone is not a setting of the solution`},
		{"a workflow in an unknown state", edit("workflows/levels.json", `"state": "published"`, `"state": "live"`), `"live" must be draft, published or archived`},
		{"a script in a folder", edit("workflows/levels.json", `"script": "levels.ts"`, `"script": "../levels.ts"`), `must be a file beside this one`},
		{"a script that is missing", edit("workflows/levels.json", `"script": "levels.ts"`, `"script": "level.ts"`), `workflows/level.ts is missing`},
		{"a script that does not compile", edit("workflows/levels.ts", `async run() {},`, `async run() {`), `workflows/levels.ts`},
		{"a script with an unknown trigger", edit("workflows/levels.ts", `"item.created"`, `"item.imagined"`), `workflows/levels.ts`},
		{"a script holding an ID", edit("workflows/levels.ts", `async run() {},`, `async run() { const list = "57cbccd7-6c57-4ff2-89c4-30255398438a"; },`), `read IDs from the parameters instead`},
		{"an app that does not compile", edit("apps/dashboard.tsx", `render() { return null; },`, `render() { return <b>; },`), `apps/dashboard.tsx`},
		{"an app importing what it may not", edit("apps/dashboard.tsx", `import { app } from "@papergo/app";`, `import { app } from "@papergo/app"; import fs from "fs"; export const used = fs;`), `an app can import only`},
		{"a collection where the app picks a type", edit("apps/dashboard.json", `"reading": "$type:tank_reading"`, `"reading": "$collection:readings"`), `picks a content type, so its value must be $type:<key>`},
		{"a published app without a required parameter", edit("apps/dashboard.json", `"list": "$collection:readings", `, ``), `list`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, problems := check(t, c.files)
			if c.want == "" {
				if len(problems) > 0 {
					t.Fatalf("problems:\n%s", problems)
				}
				return
			}
			if !strings.Contains(problems.Error(), c.want) {
				t.Fatalf("want a problem with %q, got:\n%s", c.want, problems)
			}
		})
	}
}

func TestEveryProblemIsReported(t *testing.T) {
	files := edit("solution.json", `"version": "1.0.0"`, `"version": "one"`)
	files["workflows/levels.json"] = edit("workflows/levels.json", `"tank-levels"`, `"levels"`)["workflows/levels.json"]
	files["apps/dashboard.tsx"] = &fstest.MapFile{Data: []byte("export default 1;")}
	_, problems := check(t, files)
	if len(problems) < 3 {
		t.Fatalf("want the problems of all three files, got:\n%s", problems)
	}
}

func TestPackCommand(t *testing.T) {
	folder, out := t.TempDir(), t.TempDir()
	for name, file := range tank() {
		path := filepath.Join(folder, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, file.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var said bytes.Buffer
	if err := Run(t.Context(), []string{"pack", folder, "--out", out}, &said); err != nil {
		t.Fatalf("%v\n%s", err, said.String())
	}
	packed := filepath.Join(out, "tank-1.0.0.zip")
	if _, err := os.Stat(packed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said.String(), "tank 1.0.0: 2 field(s), 1 content type(s), 1 collection(s), 1 workflow(s), 1 app(s)") {
		t.Fatal(said.String())
	}
	// The packed solution is a solution too.
	said.Reset()
	if err := Run(t.Context(), []string{"pack", packed, "--out", filepath.Join(out, "again.zip")}, &said); err != nil {
		t.Fatalf("%v\n%s", err, said.String())
	}
	first, _ := os.ReadFile(packed)
	again, _ := os.ReadFile(filepath.Join(out, "again.zip"))
	if !bytes.Equal(first, again) {
		t.Fatal("packing a packed solution changed it")
	}
	// A solution with problems lists them and writes nothing.
	if err := os.WriteFile(filepath.Join(folder, "apps", "dashboard.tsx"), []byte("export default 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	said.Reset()
	broken := filepath.Join(out, "broken.zip")
	err := Run(t.Context(), []string{"pack", folder, "--out", broken}, &said)
	if err == nil || !strings.Contains(said.String(), "apps/dashboard.tsx") {
		t.Fatalf("err %v, said:\n%s", err, said.String())
	}
	if _, err := os.Stat(broken); err == nil {
		t.Fatal("a solution with problems was written")
	}
}

// tags are the JSON properties of a struct's fields.
func tags(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

func properties(t *testing.T, schema map[string]any) []string {
	t.Helper()
	props, ok := schema["properties"].(map[string]any)
	if !ok || schema["additionalProperties"] != false {
		t.Fatalf("a schema of the files lists its properties and takes no others: %v", schema)
	}
	return sortedKeys(props)
}

// The schemas editors check a solution's files with name exactly the
// properties the files have.
func TestSchemasMatchTheFiles(t *testing.T) {
	read := func(name string) map[string]any {
		raw, err := Schema(name)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(name, err)
		}
		return schema
	}
	solution := read("solution")
	defs := solution["$defs"].(map[string]any)
	cases := []struct {
		name   string
		schema map[string]any
		of     any
	}{
		{"solution", solution, Manifest{}}, {"workflow", read("workflow"), Workflow{}}, {"app", read("app"), App{}},
		{"field", defs["field"].(map[string]any), Field{}}, {"content_type", defs["content_type"].(map[string]any), ContentType{}},
		{"relationship_type", defs["relationship_type"].(map[string]any), RelationshipType{}}, {"collection", defs["collection"].(map[string]any), Collection{}},
		{"view", defs["view"].(map[string]any), View{}}, {"use", defs["use"].(map[string]any), Use{}},
		{"field options", defs["field"].(map[string]any)["properties"].(map[string]any)["options"].(map[string]any), Field{}.Options},
	}
	for _, c := range cases {
		want := tags(reflect.TypeOf(c.of))
		sort.Strings(want)
		if got := properties(t, c.schema); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the schema has %v, the files %v", c.name, got, want)
		}
	}
	requires := solution["properties"].(map[string]any)["requires"].(map[string]any)
	if got := properties(t, requires); !reflect.DeepEqual(got, Capabilities) {
		t.Errorf("requires: the schema has %v, the capabilities are %v", got, Capabilities)
	}
	if _, err := Schema("list"); err == nil {
		t.Error("an unknown schema was served")
	}
}
