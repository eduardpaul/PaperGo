package dms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"papergo/internal/appcode"
	"papergo/internal/forms/pickers"
	"papergo/internal/script"
)

// An app's parameters are the values of the form its current version
// declares. Packages follow only the IDs its pickers select: other values
// are the app's own and travel as they are, whatever their names.

// packageAppForms returns the parameters form of each app's current
// version, by app ID. An app whose current version is missing has none.
func packageAppForms(rows map[string][]map[string]any) (map[string]map[string]any, error) {
	forms := map[string]map[string]any{}
	versions := map[string]map[string]any{}
	for _, row := range rows["app_versions"] {
		versions[fmt.Sprint(row["app_id"])+":"+fmt.Sprint(row["number"])] = row
	}
	for _, row := range rows["apps"] {
		version := versions[fmt.Sprint(row["id"])+":"+fmt.Sprint(row["current_version"])]
		if version == nil {
			continue
		}
		form, err := decodePackageObject(version["parameters_schema"])
		if err != nil {
			return nil, invalid("app " + fmt.Sprint(row["key"]) + " has an invalid parameters form")
		}
		forms[fmt.Sprint(row["id"])] = form
	}
	return forms, nil
}

func decodePackageObject(value any) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(fmt.Sprint(value)))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, errors.New("not an object")
	}
	return out, nil
}

// appParameterIDs visits the IDs an app's parameters select: IDs of data,
// and the subjects of people pickers.
func appParameterIDs(form map[string]any, parameters any, visit func(kind, path, id string)) {
	for _, picker := range pickers.Read(form, parameters, "parameters") {
		for _, id := range picker.IDs {
			visit(picker.Kind, picker.Path, id)
		}
	}
}

// compilePackageApps compiles every app version of a package from its
// source and keeps what that gives: the code, its hash and the parameters
// form. A package's code is never run as it came, so the code an app runs
// is always the code of the source its editors read.
func compilePackageApps(ctx context.Context, p *corePackage) error {
	table := p.Tables["app_versions"]
	column := func(name string) int { return slices.Index(table.Columns, name) }
	source, bundle, hash, form := column("source"), column("bundle"), column("bundle_hash"), column("parameters_schema")
	if source < 0 || bundle < 0 || hash < 0 || form < 0 {
		return invalid("invalid app version columns")
	}
	compiled := map[string]appcode.Compiled{}
	for i, row := range packageMaps(table) {
		text, _ := row["source"].(string)
		c, ok := compiled[text]
		if !ok {
			var err error
			if c, err = compilePackageApp(ctx, text); err != nil {
				return fmt.Errorf("app version %v: %w", row["number"], err)
			}
			compiled[text] = c
		}
		table.Rows[i][bundle], table.Rows[i][hash], table.Rows[i][form] = c.Code, c.Hash, string(c.ParametersSchema)
	}
	p.Tables["app_versions"] = table
	return nil
}

func compilePackageApp(ctx context.Context, source string) (appcode.Compiled, error) {
	c, err := appcode.Compile(ctx, source)
	var problem *script.BundleError
	if errors.As(err, &problem) {
		return c, invalid("the app does not compile: " + problem.Error())
	}
	return c, err
}

// remapPackageAppVersion gives an imported app version's source the IDs of
// the copy, as UUIDs in expressions are, and compiles it again when that
// changes it, so its code and form name what its source does.
func remapPackageAppVersion(ctx context.Context, source, mapped map[string]any, ids map[string]string) error {
	original, _ := source["source"].(string)
	text := packageUUID.ReplaceAllStringFunc(original, func(id string) string {
		if target, ok := ids[id]; ok {
			return target
		}
		return id
	})
	mapped["source"], mapped["bundle"], mapped["bundle_hash"], mapped["parameters_schema"] = text, source["bundle"], source["bundle_hash"], source["parameters_schema"]
	if text == original {
		return nil
	}
	c, err := compilePackageApp(ctx, text)
	if err != nil {
		return fmt.Errorf("app version %v: %w", source["number"], err)
	}
	mapped["bundle"], mapped["bundle_hash"], mapped["parameters_schema"] = c.Code, c.Hash, string(c.ParametersSchema)
	return nil
}

// remapAppParameters gives the IDs an app's pickers select the IDs of the
// copy. People keep their subjects, as accounts do.
func remapAppParameters(form map[string]any, raw any, ids map[string]string) (string, error) {
	parameters, err := decodePackageObject(raw)
	if err != nil {
		return "", invalid("invalid app parameters")
	}
	mapped := pickers.Map(form, parameters, func(kind, id string) string {
		if target, ok := ids[id]; ok && kind != "people" {
			return target
		}
		return id
	})
	out, err := json.Marshal(mapped)
	return string(out), err
}

// validateAppParameters checks that every app has its current version and
// that its pickers select only what the package carries: people among its
// accounts, everything else among its records.
func validateAppParameters(p corePackage, known map[string]bool) error {
	rows := map[string][]map[string]any{"apps": packageMaps(p.Tables["apps"]), "app_versions": packageMaps(p.Tables["app_versions"])}
	forms, err := packageAppForms(rows)
	if err != nil {
		return err
	}
	users := map[string]bool{}
	for _, row := range packageMaps(p.Tables["identity_users"]) {
		users[fmt.Sprint(row["id"])] = true
	}
	for _, app := range rows["apps"] {
		key := fmt.Sprint(app["key"])
		form, ok := forms[fmt.Sprint(app["id"])]
		if !ok {
			return invalid("app " + key + " has no current version")
		}
		parameters, err := decodePackageObject(app["parameters"])
		if err != nil {
			return invalid("app " + key + " has invalid parameters")
		}
		var outside string
		appParameterIDs(form, parameters, func(kind, path, id string) {
			carried := known[id]
			if kind == "people" {
				carried = users[id]
			}
			if !carried && outside == "" {
				outside = path + " selects " + id
			}
		})
		if outside != "" {
			return invalid("app " + key + " " + outside + ", which the package does not carry")
		}
	}
	return nil
}

// bindAppPeople binds a template's source account where an app's people
// pickers select it, as bindWorkspaceTemplate binds identity columns.
func bindAppPeople(p *corePackage, source, subject string) error {
	rows := map[string][]map[string]any{"apps": packageMaps(p.Tables["apps"]), "app_versions": packageMaps(p.Tables["app_versions"])}
	forms, err := packageAppForms(rows)
	if err != nil {
		return err
	}
	table := p.Tables["apps"]
	column := slices.Index(table.Columns, "parameters")
	for i, app := range rows["apps"] {
		form := forms[fmt.Sprint(app["id"])]
		parameters, err := decodePackageObject(app["parameters"])
		if form == nil || err != nil {
			continue
		}
		var other bool
		bound := pickers.Map(form, parameters, func(kind, id string) string {
			if kind != "people" {
				return id
			}
			if id != source {
				other = true
			}
			return subject
		})
		if other {
			return invalid("workspace template contains another subject in app " + fmt.Sprint(app["key"]) + " parameters")
		}
		raw, err := json.Marshal(bound)
		if err != nil {
			return err
		}
		table.Rows[i][column] = string(raw)
	}
	p.Tables["apps"] = table
	return nil
}
