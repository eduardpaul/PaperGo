package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"
	"golang.org/x/text/language"
)

var preferenceNames = []string{"language", "time_zone", "date_format", "time_format", "number_format", "theme", "document_languages"}
var builtInPreferences = map[string]string{"language": "en", "time_zone": "UTC", "date_format": "yyyy-MM-dd", "time_format": "24h", "number_format": "en", "theme": "system", "document_languages": "eng"}
var datePattern = regexp.MustCompile(`^[dMy]+([./ -][dMy]+)*$`)
var preferenceLanguages = regexp.MustCompile(`^[a-z][a-z_]{1,31}(\+[a-z][a-z_]{1,31})*$`)

var preferenceLocales = func() map[string]bool {
	known := map[string]bool{}
	for _, base := range language.Supported.BaseLanguages() {
		if base.String() != "und" {
			known[base.String()] = true
		}
	}
	return known
}()

type Preferences struct {
	Language          string   `json:"language"`
	TimeZone          string   `json:"time_zone"`
	DateFormat        string   `json:"date_format"`
	TimeFormat        string   `json:"time_format"`
	NumberFormat      string   `json:"number_format"`
	Theme             string   `json:"theme"`
	DocumentLanguages string   `json:"document_languages"`
	Inherited         []string `json:"inherited"`
	Version           int      `json:"version"`
}

func preferenceOwner(subject string, organization bool) (string, string) {
	if organization {
		return "organization", ""
	}
	return "user", subject
}

func ownPreferences(ctx context.Context, q Querier, ownerType, ownerID string) (map[string]string, int, error) {
	var raw string
	version := 0
	err := q.QueryRowContext(ctx, "SELECT `values`,version FROM identity_preferences WHERE owner_type=? AND owner_id=?", ownerType, ownerID).Scan(&raw, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]string{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	values := map[string]string{}
	err = json.Unmarshal([]byte(raw), &values)
	return values, version, err
}

func readPreferences(ctx context.Context, q Querier, subject string, organization bool) (Preferences, error) {
	values := map[string]string{}
	for k, v := range builtInPreferences {
		values[k] = v
	}
	defaults, _, err := ownPreferences(ctx, q, "organization", "")
	if err != nil {
		return Preferences{}, err
	}
	if !organization {
		for k, v := range defaults {
			values[k] = v
		}
	}
	ownerType, ownerID := preferenceOwner(subject, organization)
	own, version, err := ownPreferences(ctx, q, ownerType, ownerID)
	if err != nil {
		return Preferences{}, err
	}
	inherited := []string{}
	for _, k := range preferenceNames {
		if v, ok := own[k]; ok {
			values[k] = v
		} else {
			inherited = append(inherited, k)
		}
	}
	return Preferences{values["language"], values["time_zone"], values["date_format"], values["time_format"], values["number_format"], values["theme"], values["document_languages"], inherited, version}, nil
}

func (d Directory) Preferences(ctx context.Context, subject string, organization bool) (out Preferences, err error) {
	err = d.transaction(ctx, func(tx *sql.Tx) error {
		if err := RequireActive(ctx, tx, subject); err != nil {
			return err
		}
		var e error
		out, e = readPreferences(ctx, tx, subject, organization)
		return e
	})
	return
}

func validatePreference(name, value string) error {
	valid := false
	switch name {
	case "language", "number_format":
		if len(value) > 0 && len(value) <= 35 {
			tag, err := language.Parse(value)
			base, confidence := tag.Base()
			valid = err == nil && confidence == language.Exact && preferenceLocales[base.String()] && len(tag.Extensions()) == 0 && len(tag.Variants()) == 0
		}
	case "time_zone":
		_, err := time.LoadLocation(value)
		valid = err == nil && value != "" && value != "Local"
	case "date_format":
		valid = len(value) <= 32 && datePattern.MatchString(value) && strings.Contains(value, "d") && strings.Contains(value, "M") && strings.Contains(value, "y")
	case "time_format":
		valid = value == "24h" || value == "12h"
	case "theme":
		valid = value == "system" || value == "light" || value == "dark"
	case "document_languages":
		valid = len(value) <= 100 && preferenceLanguages.MatchString(value)
	default:
		return invalid("unknown preference: " + name)
	}
	if !valid {
		return invalid("invalid preference: " + name)
	}
	return nil
}

// PatchPreferences uses sparse merge semantics: omitted values stay unchanged;
// null removes the override. Version tracks this owner's overrides independently
// of subsequent organization defaults. A missing row has version zero.
func (d Directory) PatchPreferences(ctx context.Context, subject string, organization bool, version int, patch map[string]*string) (out Preferences, err error) {
	if patch == nil {
		return out, invalid("preferences must be a JSON object")
	}
	for k, v := range patch {
		if v == nil {
			known := false
			for _, name := range preferenceNames {
				if name == k {
					known = true
				}
			}
			if !known {
				return out, invalid("unknown preference: " + k)
			}
			continue
		}
		value := strings.TrimSpace(*v)
		if e := validatePreference(k, value); e != nil {
			return out, e
		}
		patch[k] = &value
	}
	err = d.transaction(ctx, func(tx *sql.Tx) error {
		if e := RequireActive(ctx, tx, subject); e != nil {
			return e
		}
		if organization {
			if e := RequireScope(ctx, tx, subject, "organization.manage"); e != nil {
				return e
			}
		}
		ownerType, ownerID := preferenceOwner(subject, organization)
		own, current, e := ownPreferences(ctx, tx, ownerType, ownerID)
		if e != nil {
			return e
		}
		if version != current {
			return ErrPreconditionFailed
		}
		properties := []string{}
		for k, v := range patch {
			old, exists := own[k]
			if v == nil && exists || v != nil && (!exists || old != *v) {
				properties = append(properties, k)
			}
		}
		sort.Strings(properties)
		for k, v := range patch {
			if v == nil {
				delete(own, k)
			} else {
				own[k] = *v
			}
		}
		raw, e := json.Marshal(own)
		if e != nil {
			return e
		}
		if current == 0 {
			_, e = tx.ExecContext(ctx, "INSERT INTO identity_preferences(id,created_at,owner_type,owner_id,`values`,version) VALUES(?,CURRENT_TIMESTAMP,?,?,?,1)", uuid.NewString(), ownerType, ownerID, string(raw))
			if IsConstraint(e) {
				return ErrPreconditionFailed
			}
			if e != nil {
				return e
			}
		} else {
			result, e := tx.ExecContext(ctx, "UPDATE identity_preferences SET `values`=?,version=version+1 WHERE owner_type=? AND owner_id=? AND version=?", string(raw), ownerType, ownerID, current)
			if e = changed(result, e); e != nil {
				return e
			}
		}
		out, e = readPreferences(ctx, tx, subject, organization)
		return e
	})
	return
}
