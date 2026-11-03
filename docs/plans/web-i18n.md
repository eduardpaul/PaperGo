# Web UI translation (plan)

Status: **planned.** Goal: the web UI in English and Spanish, built so that
another language is a new folder of JSON files and one registry line, with no
code changes.

## Where the app stands

- The UI is React with Radix UI and Tailwind (`web/src/components/ui`). About
  150 screen files hold their text inline: roughly 500 JSX text nodes, 600
  quoted sentences in props and objects, 60 toasts and 25 hand-made plurals
  (`n === 1 ? '…' : '…'`).
- Preferences already carry `language` (built-in default `en`, inheritable from
  the organization). `PreferencesProvider` sets `<html lang>` from it, and the
  `Formatter` uses it for relative times and weekday names. `number_format`,
  `date_format`, `time_format` and `time_zone` are separate fields, so the UI
  language never changes how numbers and dates look.
- API problems carry a stable `code` next to an English `detail`.
- Some text comes from the server in English: notification titles and bodies,
  problem details, and labels from extension manifests.
- Tests find elements by their English text more than 500 times.

## Decisions

1. **Library: i18next with react-i18next.** It handles CLDR plurals through
   `Intl.PluralRules`, interpolation, `<Trans>` for markup inside sentences,
   namespaces and lazy loading, and needs no build plugin. With
   `CustomTypeOptions`, the English JSON becomes the type of every key, so a
   misspelled or missing key fails `npm run typecheck`.
2. **English is the source catalog.** `web/src/locales/en/<namespace>.json`
   is the source of truth. Every other language mirrors its keys exactly.
   English is bundled with the app and is the fallback. Other languages load
   on demand through `import.meta.glob`, so each one becomes its own chunk
   inside the embedded `web/dist`.
3. **One namespace per area**: `common` (Save, Cancel, Close, Delete, empty
   and loading states), `shell`, `auth`, `errors`, `settings`, `admin`,
   `lists`, `files`, `documents`, `fields`, `workflows`, `smartFolders`,
   `comments`, `tasks`, `notifications`, `relationships`, `search`,
   `workspaces`, `archive`. Keys are semantic and nested
   (`lists.empty.title`). A key is never the English sentence.
4. **The language registry is the only list of UI languages.**
   `web/src/i18n/languages.ts` lists each language's code, its name in that
   language (`English`, `Español`) and its direction (`ltr` or `rtl`). The
   language picker, the resolver and the catalog check all read it.
5. **How the UI language is chosen:**
   - Signed in: the `language` preference. A regional locale uses the base
     language when there is no regional catalog (`es-MX` uses `es`). A language
     with no catalog uses English.
   - Before sign-in (the login page and the "Unable to load sign-in settings"
     screen): the last language this browser used, kept in `localStorage`
     the way the theme is, then `navigator.languages`, then English.
   - The provider sets `<html lang>` and `<html dir>`, and calls
     `i18next.changeLanguage` when the preference changes, so the screen
     updates without a reload.
6. **The language picker lists translated languages only.** Settings ›
   Preferences shows the registry's languages by their own names. A value
   saved elsewhere (API, organization default) stays selectable, as
   `withValue` does today. Number format keeps the full culture list.
7. **Errors are translated by problem code.** `problemMessage` looks up
   `errors:<code>`. When there is no translation for a code, it shows the
   server's `detail`, which keeps its useful specifics.
8. **Dates and numbers stay with the `Formatter`.** Intl names that are now
   fixed to English follow the language: `currency-picker.tsx`
   (`Intl.DisplayNames(['en'])`) and `cultureName`. The calendar's
   `date-fns` month and weekday labels move to the `Formatter`, so no
   `date-fns` locale bundles are needed.

## Guarding against regressions

- **Catalog check** (a Vitest test, so `npm test` and CI run it). For each
  registry language it checks that the keys match English exactly, with none
  missing and none extra; that `{{placeholders}}` and `<tags>` match; and that
  each plural key has every CLDR category the language needs (`one`/`other`
  in English; `one`/`many`/`other` in Spanish).
- **Lint rule.** `eslint-plugin-i18next` `no-literal-string`, in JSX-text
  mode, is turned on for each folder as soon as that folder is migrated, and
  for the whole of `src` once the migration is finished. `npm run lint`
  already fails on any warning.
- **Tests keep their English queries.** The test setup starts i18next
  synchronously in English with every English namespace loaded, so existing
  `getByText('Save')` assertions keep working. Each phase adds a Spanish
  render of one of its screens, which catches strings that were missed.
- **Pseudo-locale (development only).** `en-XA` is generated from English,
  with accented letters and text about 35% longer (`[Ŝåṽé ·····]`). It shows
  untranslated text and layouts that break when text gets longer. Spanish
  runs about 20–30% longer than English. It is offered only by `npm run dev`.

## Phases

Each phase is a small change of its own: replace the text, add English keys
and Spanish translations, turn on the lint rule for the migrated folders, and
pass `typecheck`, `lint`, `test` and `build`.

0. **Foundation.** Add the dependencies, `web/src/i18n/` (init, registry,
   resolver, key types), language handling in `PreferencesProvider` and
   `main.tsx`, the test setup, the catalog check, the lint rule (still
   scoped to nothing), and an empty `es` catalog.
1. **Shell and shared components.** `components/ui` (Close, No results,
   Loading), `components/shell` (sidebar, user menu, command palette),
   login, the failure screen in `main.tsx`, `lib/errors.ts`, and the
   `common` and `errors` namespaces.
2. **Settings.** The preferences form including the language picker,
   tokens, WebDAV and assistants. Users can switch language from here on.
3. **Lists and fields** (`features/lists`, 37 files; `features/fields`).
   These are the largest and include most of the plurals.
4. **Files, documents and the viewer** (`features/files`,
   `features/documents`).
5. **Workflows, smart folders, tasks, comments and notifications.**
6. **Admin** (`features/admin`, `routes/_app/admin`), then `search`,
   `relationships`, `workspaces` and `archive`.
7. **Finish.** Turn on the lint rule for all of `src`. Update
   `docs/preferences.md` (which UI languages exist and how one is chosen) and
   `web/README.md` (adding a language). Review the Spanish text in the running
   app at phone and desktop widths.

## Adding a language later

1. Copy `web/src/locales/en/` to `web/src/locales/<code>/` and translate the
   values, keeping the keys.
2. Add a line to `web/src/i18n/languages.ts`.
3. Run `npm test`. The catalog check lists missing keys, placeholders and
   plural forms.

A right-to-left language (Arabic, Hebrew) also needs a layout pass:
`<html dir>` is already set by then, and physical Tailwind classes such as
`ml-`, `pr-` and `left-` change to logical ones (`ms-`, `pe-`, `start-`).
That pass is not needed for Spanish.

## Server text (not part of this plan)

Under the Extension-First Protocol, these change Core. They are listed here
to be proposed and approved separately:

- **Notifications** are stored with English titles and bodies (`Alice
  mentioned you on Lease`). The socket would store a notification type and its
  parameters, and the web UI would render the text itself.
- **Problem codes** that are generic (`invalid_request` and similar) carry
  their meaning only in the English `detail`. The socket would add more
  specific codes, or parameters, for the errors users actually see.
- **Extension manifests** (workflow actions and similar) provide one label.
  The socket would allow labels per language, keyed by the same language
  codes as the registry.

Until those exist, the UI shows that server text as it arrives, in English.

## Spanish

- Neutral Spanish (`es`) only. Regional locales fall back to it.
- Informal `tú` throughout (`Guarda tus cambios`), as the product owner chose.
- Before release, a native speaker reviews the text in the running app.
