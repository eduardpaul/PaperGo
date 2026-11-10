# Solutions

A **solution** is the definitions of lists, workflows and apps that belong together, as a folder of files: the fields and content types, the lists and libraries with their views, the [workflows](workflows.md) and the [apps](apps.md). It carries no content: no items, files, people, permissions or IDs.

A solution is **applied** to a workspace that exists: PaperGo creates what the solution declares and the workspace lacks, updates what the solution changed since it was last applied, and leaves everything else, stored items first of all. Applying the same solution again changes nothing. A workspace holds any number of solutions, and two can work on the same list.

`papergo solution` checks, packs, plans, applies, pulls and removes solutions, and **Workspace settings → Solutions** installs, updates and removes them in the web app.

## The folder

```
diesel/
  solution.json            what the solution is and declares
  workflows/levels.json    a workflow's settings, naming levels.ts beside it
  workflows/levels.ts
  apps/dashboard.json      an app's settings, naming dashboard.tsx beside it
  apps/dashboard.tsx
  locales/es.json          its display names in another language
```

Other files, such as a README, are left out of the package.

### `solution.json`

| Property | What it is |
| --- | --- |
| `key` | Names the solution: 2 to 32 lowercase letters and digits. Everything the solution owns starts with it |
| `name`, `description` | What people see |
| `version` | The solution's version, `MAJOR.MINOR.PATCH` |
| `papergo` | The range of PaperGo versions it was written for, such as `>=0.2.0` |
| `requires` | Capabilities of the deployment (`ai.batch`, `ai.chat`, `embeddings`, `ocr`, `search`): `true` when the solution cannot work without it, `false` when it works in part |
| `settings` | A [form](workflows.md#forms) asked once at installation. A default of `=organization.time_zone` reads an organization [preference](preferences.md) (`time_zone`, `language`, `default_currency`, `number_format`, `date_format`, `time_format`) |
| `fields` | Catalog fields: `key`, `label`, `type`, and `required`, `choices`, `indexed`, `scale` and `options` as fields have them |
| `content_types` | Content types: `key`, `name` and their `fields` by key |
| `relationship_types` | Relationship types the solution needs, by `name` |
| `collections` | The lists and libraries the solution works on |

A field's and a content type's key starts with the solution's key and an underscore (`diesel_stick_cm`, `diesel_reading`); a workflow's and an app's with the key and a hyphen (`diesel-levels`). Two solutions then never claim the same key.

### Collections

Each collection is a **slot** with a `key`, by which workflows and apps name it, and a `kind` (`list` or `library`).

A collection **the solution owns** has `name`, the `content_types` it accepts (the first is its default), and optionally `webdav_enabled`, `search` (whether its items are indexed for search) and `views`:

```json
{ "key": "tank", "kind": "list", "name": "Diesel", "content_types": ["diesel_reading", "diesel_purchase"], "search": false,
  "views": [{ "key": "readings", "name": "Readings", "layout": "table", "is_default": true,
              "columns": ["$name", "diesel_measured_at"],
              "query": { "content_type_id": "$type:diesel_reading", "sort": [{ "field": "diesel_measured_at", "direction": "desc" }] } }] }
```

A collection **of another solution** says what this one relies on there instead:

```json
{ "key": "fills", "kind": "list",
  "use": { "solution": "diesel", "collection": "tank", "version": ">=1.2.0" },
  "expects": { "diesel_purchase": ["diesel_purchased_at", "diesel_total_price"] },
  "extends": { "diesel_purchase": ["receipts_invoice"] } }
```

- `use` names the collection another solution declares, and the versions of that solution it works with.
- `expects` lists the other solution's content types and the fields of each that this solution needs.
- `extends` lists this solution's own fields to add to those types.

### Workflows and apps

A settings file is what a workflow or an app is saved with, naming its source beside it:

```json
{ "key": "diesel-levels", "name": "Diesel levels", "script": "levels.ts", "state": "published",
  "automatic_enabled": true, "manual_enabled": true,
  "parameters": { "list": "$collection:tank", "reading_type": "$type:diesel_reading", "capacity_l": "$setting:capacity_l" } }
```

An app's file has `source` instead of `script`, and no `automatic_enabled` or `manual_enabled`.

### References

A solution cannot know the IDs its lists and types will have, so it names them:

| Reference | Names |
| --- | --- |
| `$collection:<key>` | a collection of the solution |
| `$type:<key>` | a content type of the solution, or one a collection expects |
| `$setting:<name>` | the value of one of the solution's settings |

References stand in parameters, in a view's query and in a lookup field's `lookup_container_id`. A lookup field may look up a collection of its own solution: it is made once the collection is there, and it keeps that collection afterwards. An ID anywhere in a solution, its scripts included, is a problem: a script reads IDs from its parameters.

### Languages

`locales/<language>.json` gives the solution's display names in a language, by key; the file is named after the language (`es.json`, `pt-BR.json`). What it leaves out keeps the solution's own text.

```json
{ "name": "Gasóleo",
  "settings": { "capacity_l": { "title": "Capacidad del depósito (L)" } },
  "fields": { "diesel_stick_cm": { "label": "Lectura de la varilla (cm)", "description": "…" } },
  "content_types": { "diesel_reading": "Medición" },
  "collections": { "tank": { "name": "Gasóleo", "views": { "readings": "Mediciones" } } },
  "workflows": { "diesel-levels": { "name": "Niveles de gasóleo" } },
  "apps": { "diesel-dashboard": { "name": "Panel del gasóleo", "description": "…" } } }
```

A first installation takes the organization's [language](preferences.md) when the solution has it (`es` also serves `es-MX`), and the solution's own text otherwise. An installation keeps its language; `--language <code>` changes it, and `--language none` returns to the solution's own text. Names are what PaperGo shows: a list's name, a field's label, a view's name. Keys, references and the values of a choice field have no language, since workflows and apps match on them; an app shows them in the viewer's language itself.

Pulling an installation in a language writes what people renamed in PaperGo into that language's file, and leaves the solution's own text as it was.

## Checking and packing

```sh
papergo solution pack ./diesel                 # writes diesel-1.2.0.zip here
papergo solution pack ./diesel --out dist/     # or into a folder, or to a file name
```

`pack` needs no server. It reads the folder, and with problems lists every one, with its file and place, and writes nothing:

```
solution.json: fields[3].key: "flag" must start with "diesel_": everything a solution owns starts with its key
workflows/levels.json: parameters.list: $collection:tanks is not a collection of the solution
apps/dashboard.json: parameters.reading: picks a content type, so its value must be $type:<key>, not "$collection:tank"
apps/dashboard.tsx: ...the compiler's message, with line and column...
```

It checks:

- the files' properties: one the format does not have is refused by name;
- keys: their form, that they start with the solution's key, and that none is declared twice;
- every reference: that what it names is declared, and, for an app, that a parameter the app's form declares as a collection or content type picker gets a reference of that kind;
- content types against fields, collections against content types, a view's columns against the fields of its collection, and a view's query against the properties a saved view has;
- a collection of another solution: that what it expects is not the solution's own, and that it extends only types it expects, with its own fields;
- each locale: that it names only settings, fields, content types, collections, views, workflows and apps the solution has;
- each workflow, compiled as saving it would, with its parameters: its triggers, forms and concurrency;
- each app, compiled as saving it would, and a published app's parameters against its form.

The ZIP holds the manifest and each settings file with the source it names. The same folder always packs to the same bytes, and a packed solution packs again to itself.

## Applying

```sh
export PAPERGO_URL=https://docs.example.com PAPERGO_TOKEN_FILE=~/.papergo-token PAPERGO_WORKSPACE=Casa
papergo solution plan ./diesel            # what applying would change; changes nothing
papergo solution apply ./diesel           # apply it
papergo solution list                     # the solutions installed
papergo solution status diesel            # whether one still has what it needs
papergo solution pull diesel --out ./diesel   # write the installation's files, as it is now
papergo solution remove diesel            # uninstall it; its lists and their items stay
```

The commands take a folder or a packed ZIP, check it as `pack` does, and talk to the server's REST API with a [personal access token](identity.md). The workspace is named by its name or ID.

```
diesel 1.3.0 (installed: 1.2.0)
  create  field diesel_notes: text
  update  content type diesel_purchase: fields
  update  view tank/purchases: columns
  update  app diesel-dashboard: source (version 4)
  kept    workflow diesel-levels, as it was edited in PaperGo
applied: 4 change(s)
```

**One transaction.** Everything a solution brings is applied together or not at all. A plan is the apply itself, rolled back, so it says exactly what applying would do, the errors of each change included.

**What is found, and by what.** A field and a content type are organization catalog entries, found by their key; a workflow and an app by their key in the workspace; a collection and a view by the installation, which remembers which object each key became. A renamed list is still the solution's list.

**Additive.** Applying never deletes. It refuses, and then applies nothing, when a change would break stored values:

| Refused | Why |
| --- | --- |
| a field's type, scale or multiplicity changing | values of the old type are stored |
| a choice the solution gave a field leaving it | stored items may hold it |
| a field the solution gave its content type leaving it | its stored values would have no field |
| a `papergo` range this PaperGo is outside of | the solution was written for another version |
| a capability the solution `requires` as `true` that this PaperGo does not provide | it cannot work here |

A solution's version that no longer declares a workflow, an app or a view leaves it in place, marks it retired and says so once; status no longer looks at it. `--prune` deletes those; a collection, a content type or a field is never deleted by a solution.

**Edits made in PaperGo stay.** The installation keeps, for each object, what the solution said it was and how it then was. On the next apply:

| The object in PaperGo | The solution changes it | Result |
| --- | --- | --- |
| as applied | yes | updated |
| as applied | no | untouched |
| edited since | no | untouched, and listed as kept |
| edited since | yes | refused until `--decide <kind>:<key>=keep` or `=replace` |

A decision to keep is remembered: the object stays as edited until the solution changes it again. What keeps stored values usable is not open to this: a field the solution adds to its content type, or a choice it adds to its field, is added even to one someone edited.

A choice added to a solution's field in PaperGo, and a field added to its content type, are not edits of what the solution says: they stay through every apply, and a [pull](#pulling-and-removing) brings them to the files.

**Settings.** `--set name=value` gives a value of the solution's settings form, and the installation keeps it. A setting nobody gave takes the form's default, read again at each apply: the organization's preference where the form says so, and a newer version's default. `--unset name` returns a setting to its default. Parameters that name `$setting:<name>` get the values.

**Collections.** A collection the solution owns is created with its name, content types, views and search setting. Its name, a library's WebDAV setting and its search setting are the solution's like everything else: changed in PaperGo, they stay. `--bind slot=<list name or ID>` uses a list or library that exists instead: it keeps its name and settings, and gets the solution's content types and views. The binding is remembered.

A collection of another solution (`use`) is found through that solution's installation in the same workspace, which must be there, in the `version` range. What the slot `expects` is checked and never changed. What it `extends` is added to the list's own copy of the content type, so the catalog type, and every other list of that type, stays as its owner made it.

**Paused.** `--paused` installs the solution with its workflows' automatic starts off, for trying a version in another workspace; `--resume` turns them on as the solution says.

**Who may.** Planning and applying need manage access to the workspace and the `solution.manage` scope, which administrators have. Each change is also checked as if made by hand, as the person applying: catalog fields and content types need their scopes, apps need `app.manage`. That person becomes the author the solution's workflows run as.

## In the web app

**Workspace settings → Solutions** lists the solutions installed in the workspace, each with its version, when and by whom it was applied, and whether it has what it needs; what it lacks is listed under it. Everyone who reads the workspace sees the list. Those who manage it and have `solution.manage` also install, update and remove:

1. Choose a packed solution, the ZIP `papergo solution pack` writes.
2. **Review changes.** The screen shows the plan and asks what the solution leaves open:
   - its settings, with the values it would use; one given by hand can be returned to the solution's default;
   - for each list or library it owns, whether to create it or use one the workspace has;
   - for each object edited in PaperGo that the solution changes too, whether to keep the edit or take the solution's;
   - the language of its names, when it has them in several;
   - whether to install it paused, and whether to delete what its version no longer has.
3. **Apply.** A choice changed after the review asks for another review first, so what is applied is what was shown. What cannot be applied says why, and nothing is applied.

**Download files** saves the installation as a packed solution, as pull does. **Remove** uninstalls it.

## Pulling and removing

**Pull** writes an installation as files again, as it is now: names, labels, views, parameters and sources read from the workspace, with the IDs as references and a parameter that still holds a setting's value as `$setting:<name>`. What someone edited in the browser reaches the folder this way, and applying the pulled folder changes nothing; what was reported as kept is then what the solution says. Files of the folder that are not the solution's stay.

**Remove** uninstalls a solution: its workflows, apps and views are deleted, the retired ones too, and its collections, content types and fields stay with every item they hold. It is refused while another installed solution uses one of its collections. Applying the solution again later finds its fields and content types, and makes a new collection unless one is bound.

## Status

`papergo solution status <key>`, or `GET /v1/workspaces/{id}/solutions/{key}/status`, checks an installation against the manifest it was applied with:

- each collection is there and still a list or a library;
- it accepts the solution's content types, with their fields;
- a collection of another solution still has what the solution expects there, and what it added;
- each workflow and app is there and published, and a workflow the solution starts automatically still does, unless the installation is paused;
- the deployment provides what the solution `requires`: OCR (`ocr`), a chat model (`ai.chat`), a batch provider (`ai.batch`), search (`search`) and an embedding model (`embeddings`). One required as `false` is reported as "works in part".

```
receipts 1.0.0 needs attention:
  fills (Diesel): expects the field diesel_total_price on diesel_purchase, which lacks it
  workflow receipts-read is draft, so it does not run
```

## API

| Method | Route | Purpose |
| --- | --- | --- |
| POST | `/v1/workspaces/{id}/solutions/plan` | What applying the packed solution in the body would do; changes nothing |
| POST | `/v1/workspaces/{id}/solutions/apply` | Apply it: 200 with the changes, 409 with the refusals when nothing was applied |
| GET | `/v1/workspaces/{id}/solutions` | The solutions installed |
| GET | `/v1/workspaces/{id}/solutions/{key}` | One installation: version, settings, bindings, the manifest applied, and the object of each key |
| GET | `/v1/workspaces/{id}/solutions/{key}/status` | Whether it has what it needs |
| GET | `/v1/workspaces/{id}/solutions/{key}/package` | Pull: the installation as a packed solution |
| DELETE | `/v1/workspaces/{id}/solutions/{key}` | Remove it |

Plan and apply take the ZIP as the body (`Content-Type: application/zip`, at most 16 MiB) and the choices as JSON in the `options` query parameter: `settings` (`null` returns one to its default), `bindings` (slot key to collection ID), `paused`, `prune`, `language` and `decisions` (`"<kind>:<key>"` to `keep` or `replace`). Reading needs read access to the workspace.

A workspace's installations are kept with the workspace and go with it.

An [app](apps.md) may read these routes as its viewer, to say that its solution needs attention; the app page refuses its plans, applies and removals, whoever views it. A workflow script cannot call them at all, as it cannot change workflows or apps.

## Editors

`papergo solution schema`, `schema workflow`, `schema app` and `schema locale` print the JSON Schemas of `solution.json`, of the two settings files and of a locale, for completion and checking while typing.
