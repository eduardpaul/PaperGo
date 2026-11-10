# Solutions

A **solution** is the definitions of lists, workflows and apps that belong together, as a folder of files: the fields and content types, the lists and libraries with their views, the [workflows](workflows.md) and the [apps](apps.md). It carries no content: no items, files, people, permissions or IDs.

`papergo solution pack` checks a solution's folder and writes it as one ZIP. Installing a solution into a workspace is [planned](plans/solutions.md); a [workspace package](workspace-portability.md) installs the samples.

## The folder

```
diesel/
  solution.json            what the solution is and declares
  workflows/levels.json    a workflow's settings, naming levels.ts beside it
  workflows/levels.ts
  apps/dashboard.json      an app's settings, naming dashboard.tsx beside it
  apps/dashboard.tsx
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

References stand in parameters, in a view's query and in a lookup field's `lookup_container_id`. An ID anywhere in a solution, its scripts included, is a problem: a script reads IDs from its parameters.

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
- each workflow, compiled as saving it would, with its parameters: its triggers, forms and concurrency;
- each app, compiled as saving it would, and a published app's parameters against its form.

The ZIP holds the manifest and each settings file with the source it names. The same folder always packs to the same bytes, and a packed solution packs again to itself.

## Editors

`papergo solution schema`, `schema workflow` and `schema app` print the JSON Schemas of `solution.json` and of the two settings files, for completion and checking while typing.
