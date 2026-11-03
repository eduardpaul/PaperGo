# Screen text

Every text the web UI shows comes from these catalogs. `en` is the source: its keys are the keys of every language,
and `src/i18n/resources.ts` types them, so a missing key fails `npm run typecheck`. Each other folder is one language
from `src/i18n/languages.ts`, loaded when a user picks it.

## Adding a language

1. Copy `en/` to `<code>/` (a BCP 47 code such as `fr` or `pt-BR`) and translate the values. Keep the keys.
2. Add the language to `src/i18n/languages.ts` with its own name for itself and its direction.
3. Run `npm test`. `src/i18n/catalogs.test.ts` lists missing and extra keys, changed placeholders and missing plural
   forms.

Add a glossary section below for the new language before translating, so the same English word always becomes the same
word in it.

## Writing keys

- One namespace per area of the app (`lists`, `files`, `admin` …). Generic words used everywhere (Save, Cancel, Delete,
  Loading…) live in `common`. Problem codes from the API live in `errors`.
- Keys are nested and describe the meaning, not the English words: `lists.empty.title`, not `"No items yet"`.
- Whole sentences only. Never build a sentence from pieces; use placeholders: `"Moved {{count}} items to {{folder}}"`.
  When a sentence holds markup or a link, use `<Trans>` with named tags: `"Open <link>settings</link> to change it."`.
- Plurals use i18next’s CLDR suffixes on a key that takes `count`: English needs `_one` and `_other`; Spanish needs
  `_one`, `_many` and `_other` (`_many` is the form for large round numbers such as a million and is usually the same as
  `_other`).
- Do not translate user data, the product name PaperGo, API values and codes, permission scopes such as `item.write`,
  keyboard key names, file extensions and patterns such as `yyyy-MM-dd`.
- Dates, times, numbers, sizes and currencies are formatted with `useFormat()`, never written into a text.

## Style

- Sentence case for labels, buttons and titles: “New smart folder”, not “New Smart Folder”.
- `…` (one character) for an action that needs more input and for progress (“Saving…”).
- Quotes: English “ ”, Spanish « ».

## Spanish glossary

Neutral Spanish for every region, addressing the user as `tú` (“Guarda tus cambios”). Buttons are infinitives
(“Guardar”, “Crear lista”); hints and messages speak to the user (“Elige una carpeta”).

| English | Spanish |
| --- | --- |
| workspace | espacio de trabajo |
| list | lista |
| library (of documents) | biblioteca |
| item | elemento |
| file | archivo |
| document | documento |
| folder | carpeta |
| smart folder | carpeta inteligente |
| field / property | campo / propiedad |
| view | vista |
| filter / sort / group | filtro / ordenar / agrupar |
| workflow | flujo de trabajo |
| action (workflow step) | acción |
| trigger | desencadenador |
| run (noun) | ejecución |
| task | tarea |
| assignee | persona asignada |
| due date | fecha de vencimiento |
| comment / reply / mention | comentario / respuesta / mención |
| notification | notificación |
| inbox | bandeja de entrada |
| archive (verb) / archived | archivar / archivado |
| Archive (the page) | Archivados |
| recycle bin | papelera |
| version | versión |
| upload / download | subir / descargar |
| preview | vista previa |
| relationship | relación |
| keyword | palabra clave |
| term / taxonomy | término / taxonomía |
| content type | tipo de contenido |
| member / owner / editor / viewer | miembro / propietario / editor / lector |
| role / group / permission | rol / grupo / permiso |
| organization | organización |
| user / account | usuario / cuenta |
| sign in / sign out | iniciar sesión / cerrar sesión |
| personal access token | token de acceso personal |
| settings / preferences | configuración / preferencias |
| administration | administración |
| audit log | registro de auditoría |
| assistant (AI) | asistente |
| script | script |
| webhook | webhook |
| OCR | OCR |
