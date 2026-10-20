package identity

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	"papergo/internal/model"
	_ "time/tzdata"

	"golang.org/x/text/language"
)

var preferenceNames = []string{"language", "time_zone", "date_format", "time_format", "number_format", "theme", "document_languages", "default_currency"}
var builtInPreferences = map[string]string{"language": "en", "time_zone": "UTC", "date_format": "yyyy-MM-dd", "time_format": "24h", "number_format": "en", "theme": "system", "document_languages": "eng", "default_currency": "USD"}
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
	DefaultCurrency   string   `json:"default_currency"`
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

func preferenceOwner(subject string, organization bool) (directory.PreferenceOwner, string) {
	if organization {
		return directory.OwnerOrganization, ""
	}
	return directory.OwnerUser, subject
}

func ownPreferences(ctx context.Context, store *dal.Store, ownerType directory.PreferenceOwner, ownerID string) (map[string]string, int, error) {
	row, err := store.Directory().Preferences(ctx, ownerType, ownerID)
	return row.Values, int(row.Version), err
}

func readPreferences(ctx context.Context, store *dal.Store, subject string, organization bool) (Preferences, error) {
	values := map[string]string{}
	for k, v := range builtInPreferences {
		values[k] = v
	}
	defaults, _, err := ownPreferences(ctx, store, directory.OwnerOrganization, "")
	if err != nil {
		return Preferences{}, err
	}
	if !organization {
		for k, v := range defaults {
			values[k] = v
		}
	}
	ownerType, ownerID := preferenceOwner(subject, organization)
	own, version, err := ownPreferences(ctx, store, ownerType, ownerID)
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
	return Preferences{values["default_currency"], values["language"], values["time_zone"], values["date_format"], values["time_format"], values["number_format"], values["theme"], values["document_languages"], inherited, version}, nil
}

func (d Directory) Preferences(ctx context.Context, subject string, organization bool) (out Preferences, err error) {
	err = d.transaction(ctx, func(tx *sql.Tx) error {
		if err := RequireActive(ctx, d.store(tx), subject); err != nil {
			return err
		}
		var e error
		out, e = readPreferences(ctx, d.store(tx), subject, organization)
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
	case "default_currency":
		valid = model.ValidCurrencyCode(value)
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
		if k == "default_currency" && !organization {
			return out, invalid("default_currency is an organization setting")
		}
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
		if e := RequireActive(ctx, d.store(tx), subject); e != nil {
			return e
		}
		if organization {
			if e := RequireScope(ctx, d.store(tx), subject, "organization.manage"); e != nil {
				return e
			}
		}
		ownerType, ownerID := preferenceOwner(subject, organization)
		own, current, e := ownPreferences(ctx, d.store(tx), ownerType, ownerID)
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
		if e = d.store(tx).Directory().SavePreferences(ctx, ownerType, ownerID, int64(current), own); e != nil {
			if errors.Is(e, dberr.ErrConcurrentUpdate) {
				return ErrPreconditionFailed
			}
			return e
		}

		out, e = readPreferences(ctx, d.store(tx), subject, organization)
		return e
	})
	return
}
