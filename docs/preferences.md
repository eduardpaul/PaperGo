# Preferences

`GET /v1/identity/me/preferences` returns the active user's effective preferences. Personal overrides take precedence over organization defaults, then built-in values: language `en`, time zone `UTC`, date format `yyyy-MM-dd`, time format `24h`, number format `en`, theme `system`, document languages `eng` and, for the organization, default currency `USD`. `inherited` names the fields without a personal override. `GET /v1/organization/preferences` shows organization defaults and fields inherited from the built-in values.

PATCH either endpoint with a JSON object. Omitted fields stay unchanged; a string sets an override; `null` removes it and resumes inheritance. The fields are `language`, `time_zone`, `date_format`, `time_format`, `number_format`, `theme`, `document_languages` and, for the organization only, `default_currency`. Locale names, IANA time zones, date patterns containing `d`, `M` and `y`, `12h`/`24h`, `system`/`light`/`dark` and lowercase document language codes joined by `+` are validated. Unknown fields are rejected.

Both endpoints return a quoted numeric ETag. PATCH requires that ETag in `If-Match`; a missing preference row starts at `"0"`. Missing preconditions return 428 and stale ones return 409. Versions track each owner's overrides. Changing organization defaults affects inherited personal values immediately without advancing the personal version. Personal settings are isolated by the authenticated subject. Any active user can read defaults and edit their own preferences; modifying organization defaults requires `organization.manage`, including through a delegated role. Inactive users cannot read or change preferences.

The organization defaults can also come from the [configuration](configuration.md#organization-defaults), such as `organization.locale: es-ES`; PaperGo applies them when it creates the database, and they change here afterwards.

## Web UI language

The web UI is available in English and Spanish and shows the `language` preference. A regional locale uses its base language (`es-MX` shows Spanish), and a language without a translation shows English. Relative dates and weekday names follow the same language; `number_format`, `date_format`, `time_format` and `time_zone` keep their own settings. Decimals typed into fields, filters and forms use the `number_format`'s decimal separator, so `41,5` is 41.5 where that separator is a comma; a dot is always accepted as a decimal point. The language picker offers the translated languages, and a value set through the API or as an organization default stays selectable.

Before sign-in, the UI uses the language this browser showed last, then the first of the browser's languages that has a translation. API problems whose code has a fixed meaning are shown in the UI language; other problems show the server's English `detail`, as do notification texts. [`web/src/locales/README.md`](../web/src/locales/README.md) describes adding a language.
