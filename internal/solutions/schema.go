package solutions

import (
	"embed"
	"fmt"
	"io"
)

// The JSON Schemas of a solution's files, for editors: solution.json, a
// workflow's settings, an app's settings and a locale.
//
//go:embed schema/*.schema.json
var schemas embed.FS

// SchemaNames are the files a schema describes.
var SchemaNames = []string{"solution", "workflow", "app", "locale"}

// Schema returns the JSON Schema of one of a solution's files.
func Schema(name string) ([]byte, error) {
	if !contains(SchemaNames, name) {
		return nil, fmt.Errorf("the schemas are solution, workflow, app and locale, not %q", name)
	}
	return schemas.ReadFile("schema/" + name + ".schema.json")
}

// schemaCommand prints a schema: papergo solution schema [solution|workflow|app|locale].
func schemaCommand(args []string, out io.Writer) error {
	name := "solution"
	if len(args) == 1 {
		name = args[0]
	} else if len(args) > 1 {
		return fmt.Errorf("%s", usage)
	}
	raw, err := Schema(name)
	if err != nil {
		return err
	}
	_, err = out.Write(raw)
	return err
}
