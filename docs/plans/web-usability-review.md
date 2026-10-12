# Web usability: workspaces, lists and libraries

This plan records how the workspace, list and library screens are meant to work for people, and the usability work still open. PaperDotNet's web UI (`docs/frontend.md` there) is the reference; differences come from the PaperGo API.

## Principles

1. **People's words, not API words.** Screens say list, library, column, permissions and version. They do not show ids, keys, schema or field type identifiers unless someone asks for them (Field definitions, column keys in the column dialog).
2. **One obvious main action per screen.** A library's main action is Upload; a list's is New item. Secondary actions sit next to it. Rare actions go in the "…" menu.
3. **Nothing destructive in one click.** Deleting a workspace, list, library, folder, item, column, view, member, or emptying a recycle bin entry asks first and says what happens and whether it can be undone.
4. **Pick, don't type.** People, groups, lists and folders are chosen from pickers.
5. **Show what is there.** A list shows its columns without a saved view; empty screens explain themselves and offer the next step.
6. **Save is explicit and safe.** Forms enable Save only when something changed, keep edits while switching tabs and ask before discarding them.
7. **Actions follow access.** Screens offer only what `access` on the resource allows: write for adding, editing, moving and deleting, publish for publishing, manage for settings and permissions. Readers get read-only forms.

## Screens

| Screen | Route | What people do there |
| --- | --- | --- |
| Workspace | `/w/$workspaceId` | Open a list or library (icon, description, last update), create one from a blank list, blank library or template, open Tags and Settings |
| Workspace settings | `/w/$workspaceId/settings/*` | Rename, delete (confirmed); members with a people picker, role changes in place and confirmed removal; workflows; runs |
| List or library | `/w/$workspaceId/l/$listId` | Views as tabs, search as you type, folders with breadcrumbs, table with select all, bulk field edit and confirmed delete; upload by button or drag and drop into the current folder |
| Item panel | `…?item=$itemId&tab=` | Details with a sticky Save/Discard bar; File, Versions, Links, Permissions and (with publishing on) Publishing tabs; Move (folder browser), Add to smart folder, Field definitions and Delete in the "…" menu |
| List settings | `/w/$workspaceId/l/$listId/settings/*` | General (folders, version history, publishing, duplicates and network drive for libraries, delete), Columns and content types, Views, Permissions, Background work |
| Recycle bin | `/w/$workspaceId/l/$listId/recycle-bin` | Restore, or delete permanently after confirming |

## Open work

Ordered by how much it helps people.

1. **Sorting by column header.** Clicking a column header should sort, with the sort in the URL. The API sorts saved-view queries on indexed columns only; allow an ad hoc `sort` on `POST /v1/resources/{id}/query` for indexed columns and system columns.
2. **Names for lookup and term values.** Table cells and the item form show ids for lookups and terms until a search loads them. Resolve names for the visible page (as PaperDotNet's `ValueNamesProvider`) using `GET /v1/terms?ids=` and a batch read of looked-up items.
3. **Tags as chips.** Tags are typed comma separated. Use a chip input with suggestions from `GET /v1/workspaces/{id}/tags`.
4. **File versions list (API).** The File tab restores by typing a version number because there is no list endpoint. Add `GET /v1/items/{id}/content/versions` and show a list with dates, sizes and Restore.
5. **Document preview.** Libraries show no thumbnail or preview; the File tab only downloads. Add an inline PDF/image preview from `GET /v1/items/{id}/content` and thumbnails when the API offers them.
6. **Keyboard use of lists.** Arrow keys to move between rows, Enter to open, `x` to select, as in PaperDotNet.
7. **Folder listing in Move.** Each level shows folders among its first 200 children. A folder filter on children (`kind=folder`) would make large libraries reliable.
8. **Edit views from the list.** A "Save as view" or column picker on the list page, so people do not need settings to change what they see.
9. **Live upload tray.** Several uploads run one after another with one toast at the end. Show per-file progress, duplicates and links as PaperDotNet's upload tray does.
