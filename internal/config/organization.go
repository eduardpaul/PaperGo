package config

import (
	"fmt"
	"sort"
	"strings"

	"papergo/internal/identity"
)

// localePreset is what an organization locale (ORGANIZATION_LOCALE) sets:
// the organization's preference defaults and the languages search stems.
// A preset names a time zone only where one zone fits the country.
type localePreset struct {
	preferences map[string]string
	search      string
}

func preset(locale, timeZone, dateFormat, timeFormat, currency, documents, search string) localePreset {
	p := map[string]string{"language": locale, "number_format": locale, "date_format": dateFormat, "time_format": timeFormat, "default_currency": currency, "document_languages": documents}
	if timeZone != "" {
		p["time_zone"] = timeZone
	}
	return localePreset{preferences: p, search: search}
}

var localePresets = map[string]localePreset{
	"en-US": preset("en-US", "", "MM/dd/yyyy", "12h", "USD", "eng", "english"),
	"en-GB": preset("en-GB", "Europe/London", "dd/MM/yyyy", "24h", "GBP", "eng", "english"),
	"en-IE": preset("en-IE", "Europe/Dublin", "dd/MM/yyyy", "24h", "EUR", "eng", "english"),
	"es-ES": preset("es-ES", "Europe/Madrid", "dd/MM/yyyy", "24h", "EUR", "spa+eng", "spanish,english"),
	"fr-FR": preset("fr-FR", "Europe/Paris", "dd/MM/yyyy", "24h", "EUR", "fra+eng", "french,english"),
	"de-DE": preset("de-DE", "Europe/Berlin", "dd.MM.yyyy", "24h", "EUR", "deu+eng", "german,english"),
	"de-AT": preset("de-AT", "Europe/Vienna", "dd.MM.yyyy", "24h", "EUR", "deu+eng", "german,english"),
	"it-IT": preset("it-IT", "Europe/Rome", "dd/MM/yyyy", "24h", "EUR", "ita+eng", "italian,english"),
	"pt-PT": preset("pt-PT", "Europe/Lisbon", "dd/MM/yyyy", "24h", "EUR", "por+eng", "portuguese,english"),
	"nl-NL": preset("nl-NL", "Europe/Amsterdam", "dd-MM-yyyy", "24h", "EUR", "nld+eng", "dutch,english"),
}

// LocalePresets are the organization locales PaperGo has a preset for.
func LocalePresets() []string {
	names := make([]string, 0, len(localePresets))
	for name := range localePresets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// organizationLocale is the preset ORGANIZATION_LOCALE names, matched
// without regard to case; none when it is empty.
func organizationLocale(locale string) (localePreset, error) {
	if locale == "" {
		return localePreset{search: "english"}, nil
	}
	for name, p := range localePresets {
		if strings.EqualFold(name, locale) {
			return p, nil
		}
	}
	return localePreset{}, fmt.Errorf("ORGANIZATION_LOCALE must be one of %s", strings.Join(LocalePresets(), ", "))
}

// organizationSettings are the preferences with an ORGANIZATION_ setting
// of their own, which replaces the locale's.
var organizationSettings = []string{"language", "time_zone", "date_format", "time_format", "number_format", "theme", "default_currency", "document_languages"}

// loadOrganization reads the organization's preference defaults: the
// locale's, then each ORGANIZATION_<PREFERENCE> named.
func (c *Config) loadOrganization(value func(key, fallback string) string, locale localePreset) error {
	seed := map[string]string{}
	for k, v := range locale.preferences {
		seed[k] = v
	}
	for _, name := range organizationSettings {
		key := "ORGANIZATION_" + strings.ToUpper(name)
		v := strings.TrimSpace(value(key, ""))
		if v == "" {
			continue
		}
		if err := identity.ValidatePreference(name, v); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		seed[name] = v
	}
	if len(seed) > 0 {
		c.OrganizationPreferences = seed
	}
	return nil
}
