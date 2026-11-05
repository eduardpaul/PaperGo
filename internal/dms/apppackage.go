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

// appParameterIDs visits the IDs an app's pickers select: IDs of data, and
// the subjects of people pickers.
func appParameterIDs(form map[string]any, parameters any, visit func(id string)) {
	for _, picker := range pickers.Read(form, parameters, "parameters") {
		for _, id := range picker.IDs {
			visit(id)
		}
	}
}

// currentAppVersions returns the app_versions rows that are their app's
// current version, by row ID. Only these run; earlier versions are history.
func currentAppVersions(rows map[string][]map[string]any) map[string]bool {
	current := map[string]bool{}
	for _, app := range rows["apps"] {
		current[fmt.Sprint(app["id"])+":"+fmt.Sprint(app["current_version"])] = true
	}
	out := map[string]bool{}
	for _, row := range rows["app_versions"] {
		if current[fmt.Sprint(row["app_id"])+":"+fmt.Sprint(row["number"])] {
			out[fmt.Sprint(row["id"])] = true
		}
	}
	return out
}

// compilePackageApps compiles each app's current version from its source
// and keeps what that gives: the code, its hash and the parameters form.
// The code an app runs is never the package's, but always that of the
// source its editors read. Earlier versions never run and keep their
// source, code and form as history. Each distinct source compiles once.
func compilePackageApps(ctx context.Context, p *corePackage) error {
	table := p.Tables["app_versions"]
	column := func(name string) int { return slices.Index(table.Columns, name) }
	bundle, hash, form := column("bundle"), column("bundle_hash"), column("parameters_schema")
	if bundle < 0 || hash < 0 || form < 0 {
		return invalid("invalid app version columns")
	}
	rows := map[string][]map[string]any{"apps": packageMaps(p.Tables["apps"]), "app_versions": packageMaps(table)}
	current := currentAppVersions(rows)
	keys := map[string]string{}
	for _, app := range rows["apps"] {
		keys[fmt.Sprint(app["id"])] = fmt.Sprint(app["key"])
	}
	compiled := map[string]appcode.Compiled{}
	for i, row := range rows["app_versions"] {
		if !current[fmt.Sprint(row["id"])] {
			continue
		}
		text, _ := row["source"].(string)
		c, ok := compiled[text]
		if !ok {
			var err error
			if c, err = appcode.Compile(ctx, text); err != nil {
				var problem *script.BundleError
				if errors.As(err, &problem) {
					return invalid("app " + keys[fmt.Sprint(row["app_id"])] + " does not compile: " + problem.Error())
				}
				return err
			}
			compiled[text] = c
		}
		table.Rows[i][bundle], table.Rows[i][hash], table.Rows[i][form] = c.Code, c.Hash, string(c.ParametersSchema)
	}
	p.Tables["app_versions"] = table
	return nil
}

// remapPackageAppVersion gives an imported app version the IDs of the copy
// where its source names them, as UUIDs in expressions are, and the same in
// its code and form, which hold them as the source does.
func remapPackageAppVersion(source, mapped map[string]any, ids map[string]string) {
	for _, column := range []string{"source", "bundle", "parameters_schema"} {
		mapped[column] = remapPackageJSON(fmt.Sprint(source[column]), ids, nil)
	}
	mapped["bundle_hash"] = script.CodeHash(mapped["bundle"].(string))
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
// that each of its pickers selects, among what the package carries, what
// that kind of picker picks: people its accounts, collections its lists and
// libraries, items its items, and so on.
func validateAppParameters(p corePackage) error {
	rows := map[string][]map[string]any{"apps": packageMaps(p.Tables["apps"]), "app_versions": packageMaps(p.Tables["app_versions"])}
	forms, err := packageAppForms(rows)
	if err != nil {
		return err
	}
	ids := func(table string) map[string]bool {
		out := map[string]bool{}
		for _, row := range packageMaps(p.Tables[table]) {
			out[fmt.Sprint(row["id"])] = true
		}
		return out
	}
	kinds := map[string]map[string]bool{"list": {}, "library": {}, "item": {}}
	for _, row := range packageMaps(p.Tables["resources"]) {
		if kind := fmt.Sprint(row["kind"]); kinds[kind] != nil {
			kinds[kind][fmt.Sprint(row["id"])] = true
		}
	}
	users, types, terms, sets := ids("identity_users"), ids("content_types"), ids("terms"), ids("term_sets")
	picks := func(picker pickers.Picker, id string) bool {
		switch picker.Kind {
		case "collection":
			return kinds["list"][id] || kinds["library"][id]
		case "item", "relationship":
			return kinds["item"][id]
		case "content_type":
			return types[id]
		case "terms":
			if picker.Options["select"] == "sets" {
				return sets[id]
			}
			return terms[id]
		case "keywords":
			return terms[id]
		case "people":
			return users[id]
		}
		return false
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
		for _, picker := range pickers.Read(form, parameters, "parameters") {
			for _, id := range picker.IDs {
				if !picks(picker, id) {
					return invalid(fmt.Sprintf("app %s %s selects %q, which is not a %s the package carries", key, picker.Path, id, strings.ReplaceAll(picker.Kind, "_", " ")))
				}
			}
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
