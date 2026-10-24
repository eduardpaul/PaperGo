# File handlers (plan)

Status: **proposal, nothing implemented.** It waits on the
[open decisions](#open-decisions). Two of them add core sockets, which the
Extension-First protocol requires us to agree on before building.

The goal: a person browsing a library can **open the whole file** inside
PaperGo, not just its 800-pixel first-page thumbnail. Each file type is
handled by a **file handler**: a plug-in that knows how to view the type and,
for some types, edit it. Markdown is the sample handler that does both. PDF
gets a real viewer. Images get zoom.

## Product view

### Who it is for

| Person | Job | What they need today and can't get |
| --- | --- | --- |
| Reader | "What does page 7 of this contract say?" | To read every page without downloading the file and finding it in a downloads folder. |
| Reviewer | "Check the signature block, then the next invoice." | To zoom in, then step to the next item in the same view without closing anything. |
| Writer | "Keep the team's runbook as Markdown next to the PDFs." | To store `.md` files at all (they're refused today), and to edit one in place with a preview. |
| Mobile user | "Show me the delivery note." | A full-screen, pinch-zoomable view on a phone. |
| Link sharer | "Look at page 3." | A link that opens the file at that page. |

### What exists today

| Building block | State | Consequence |
| --- | --- | --- |
| Accepted file types | Hard-coded to PDF, TIFF, JPEG, PNG and WebP, detected from the first 12 bytes (`internal/storage/documents.go`). The same set is a `CHECK` on `blobs.content_type`, the `blobs.MediaType` constants, the web upload `accept` list and the file-type filter labels. | `.md` and `.txt` uploads return 415, and so do WebDAV saves of them. **There is no place for a new type to plug in.** |
| Preview | `GET /v1/items/{id}/preview`: a WebP of page 1, longest side at most 800 px. It shows in gallery and board cards and in the item sheet's **File** tab. | You can never see page 2. There is no zoom, and clicking the preview does nothing. |
| Original file | `GET /v1/items/{id}/content`: always `attachment`, with Range support. The web app only ever saves it to disk. | Opening a file means downloading it. |
| Text | `GET /v1/items/{id}/text` returns text by page once it has been read. It works for PDFs and images (via OCR) only. | Could drive find-in-file for scanned PDFs later. |
| Editing a file | `PUT /v1/items/{id}/content` with `If-Match` makes a new file version. The **Replace file** button uploads one. | The concurrency model an editor needs already exists. |
| Viewer routes | None. An item is always `?item=<id>` on the list route and opens in the side sheet. Search, smart folders and relationship links all link that way. | A viewer can reuse `?item=` and every existing link gains it. |
| Web extension points | None. Layouts, field editors and sheet tabs are `if` chains. | Handlers need a small client-side registry. |
| Rendering libraries | Monaco is a dependency, lazily loaded for the workflow editor. There is no Markdown renderer and no PDF renderer. | New dependencies: a Markdown renderer and PDF.js. |
| Security headers | `nosniff` and `no-store` are set. There is no Content-Security-Policy. | Rendered Markdown must be safe by construction: no raw HTML. |

### Principles

1. **Preview is a glance, the viewer is the file.** Thumbnails stay where they
   are. One consistent **Open** action leads from any of them to the
   full-screen viewer.
2. **Details stay one click away.** PaperGo is metadata-first: clicking a row
   still opens the details sheet. Opening the file is an explicit,
   always-visible action, plus a double-click and a key.
3. **The viewer is a place you can link to.** It lives in the URL, survives a
   reload and closes with Back. Every existing `?item=` link can carry it.
4. **Saving is a file version.** An editor save is exactly a **Replace file**,
   with the same history, publishing, workflows, audit and conflict rules.
   There are no hidden autosave versions.
5. **A handler is a plug-in.** Core knows file types only through the
   registries below. Viewers and editors live in their own lazily loaded
   bundles, built on the public API.

### Out of scope

- Real-time co-editing, presence and in-browser editing of PDFs, images or
  Office files. The editor is single-user, with optimistic concurrency.
- Annotations, and comments anchored to a page or region. This is the
  "point at clause 4 on page 3" need from the comments plan. The viewer's
  page model prepares for it: [phase 4](#phases).
- Office formats. They come later through the same socket
  ([phase 4](#phases)).
- Converting between formats, such as Markdown to PDF.

## The experience

### Words

| Term | Meaning in the UI |
| --- | --- |
| **Preview** | The thumbnail on cards and in the File tab. Unchanged. |
| **Open** | Opens the viewer: the whole file, full screen. |
| **Edit** | Available for editable types (Markdown, plain text). It turns the viewer into an editor. |
| **Save** | Makes a new file version. In libraries with publishing, a save is a draft. |

### Ways in

| Where | Today | Proposed |
| --- | --- | --- |
| Table row (library) | Click or Enter opens the details sheet. | Unchanged. **Double-click** or **`o`** opens the file. The name cell shows a file-type icon. |
| Gallery and board card | Click opens the sheet. | Unchanged. Hovering or focusing the thumbnail shows an **Open** button (expand icon). On touch screens the button is always visible. |
| Item sheet header | Title, then menu. | A primary **Open** button beside the title for library items, and **Edit** for editable types. |
| Sheet File tab | Static preview, Download, Replace. | Clicking the preview opens the viewer. Each version row in the file history gets **Open** to view that version. |
| Context menu | Open, Details, Download… | Adds **Open file** and, for editable types, **Edit**. |
| Search results, smart folders, notifications | Link to `?item=`. | Unchanged. The sheet's **Open** button is one click away. Search hits get an **Open file** action. |
| Library toolbar **New** | Item, folder, upload. | Adds **Markdown document**: ask for a name, create the file, open it in the editor. |

### The viewer

The viewer is a full-screen layer above the list. The list stays mounted, so
closing returns to the same scroll position and selection. It is addressed
as `?item=<id>&open=file`, with optional `&page=<n>` (PDF),
`&blob=<id>` (an earlier version) and `&edit=1`.

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ ←  Finance / Invoices / 2026            ACME-0042.pdf  [Draft]    ‹ 12 of 80 › │
│                                     [Edit] [Download] [⋯] [ⓘ Info] [✕]       │
├────────┬────────────────────────────────────────────────────┬────────────────┤
│ ▢ 1    │                                                    │ Details        │
│ ▣ 2    │                 ┌──────────────────┐               │ Discussion (2) │
│ ▢ 3    │                 │                  │               │ Versions       │
│ ▢ 4    │                 │   page 2 of 9    │               │ ────────────── │
│        │                 │                  │               │ Vendor: ACME   │
│ pages  │                 └──────────────────┘               │ Total: 1,240.00│
│ (opt.) │                                                    │ …              │
├────────┴────────────────────────────────────────────────────┴────────────────┤
│  ◀ 2 / 9 ▶     − 100% +   Fit width ▾   ⟳     Find (Ctrl+F)                    │
└──────────────────────────────────────────────────────────────────────────────┘
```

- **Top bar.** It holds:
  - Back.
  - A breadcrumb (workspace / collection / folder).
  - The file name, with badges: **Draft** (a newer unpublished file than readers see), **Archived**, or **Version 3 of 7** when viewing an earlier version.
  - Previous and next.
  - The actions.
- **Previous and next** step through the items of the list as it was showing: the same view, folder, filter, search and sort. The list hands the viewer the ordered IDs it has loaded. After a reload, or when the viewer was opened from a link, the arrows are hidden rather than guessed.
- **Info panel** (`i`) reuses the item sheet's tabs:
  - **Details** (editable as in the sheet);
  - **Discussion**;
  - **Versions**.

  It is closed by default and remembered per browser. Below 1024 px it opens as an overlay sheet.
- **The `⋯` menu** holds:
  - Copy link (the deep link, including the page);
  - Open details;
  - Replace file;
  - Run workflow;
  - Archive and Delete, which behave as in the sheet.
- **Version view.** When viewing an earlier version, a banner reads "You are viewing version 3 (uploaded by Ana, 12 May). **Restore this version** · **View current**". Edit is disabled.
- **States.** Each one fills the canvas and never leaves it blank:
  - loading: a skeleton;
  - the item has no file yet;
  - the version was reclaimed (410): "This version's bytes were reclaimed";
  - the file is broken or protected (PDF password prompt, client-side only);
  - no handler for the type: the type icon, file name, size and a large **Download** button;
  - offline or failed: a retry.

### PDF handler (view)

- Continuous vertical scroll with lazy page rendering.
- Optional page thumbnails rail (`t`).
- A page box you can type into.
- Zoom: fit width (default), fit page, 50–400 %, Ctrl/⌘ + wheel, and pinch.
- Rotate the view (does not change the file).
- **Find** (Ctrl/⌘+F) over the PDF's text layer, with a hit count and next/previous.
- Text selection and copy.
- Links inside the PDF open in a new tab with `noopener`, after showing the target host.
- **Scanned PDFs** have no text layer. Find says "This PDF has no text layer". [Phase 3](#phases) searches PaperGo's OCR text instead and jumps to the matching pages.
- `&page=n` deep links. The current page updates the URL with `replace`, so Back closes the viewer rather than stepping through pages.

### Image handler (view)

- JPEG, PNG and WebP are shown at full resolution from the original file.
- Fit by default.
- Double-click or `1` toggles 100 %.
- Wheel or pinch zooms; drag pans.
- Rotate the view.
- On touch screens, swiping left or right goes to the previous or next item when the image is at fit size.
- **TIFF:** browsers can't decode it.
  - Phase 1 shows the 800 px preview with the note "Showing a preview. Download for full resolution."
  - Phase 3 adds server-rendered pages, which also gives multi-page TIFFs every page.

### Markdown handler (view and edit) — the sample

**View.**
- Rendered GitHub-flavoured Markdown: headings, lists, task lists, tables, code blocks and links.
- A readable measure (about 72 characters) in the app's typography.
- An outline of the headings on wide screens, for jumping around.
- Raw HTML in the file is shown as text, never rendered.
- Remote images are not loaded until you choose **Show remote images** for that file. This stops tracking pixels and mixed content.
- Links open in a new tab with `noopener`.

**Edit** (the **Edit** button, `e`, or `&edit=1`).

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ ←  Team / Runbooks    on-call.md  • Unsaved changes        [Cancel] [Save ⌘S]│
├──────────────────────────────────────┬───────────────────────────────────────┤
│ H  B  I  Link  • List  ☐ Task  Code  │                                       │
│ # On-call                            │  On-call                              │
│                                      │  ═══════                              │
│ 1. Check the **alerts** board        │  1. Check the alerts board            │
│ 2. ...                               │  2. ...                               │
│        source (Monaco)               │        live preview                   │
└──────────────────────────────────────┴───────────────────────────────────────┘
```

- **Layout.** Split source and preview at 1024 px and wider; **Write** and **Preview** tabs on narrower screens. On touch devices the source pane is a plain textarea, since Monaco is poor on phones.
- **Toolbar.** A small Markdown toolbar with the shortcuts Ctrl/⌘ + B, I and K. The editor is Monaco with its Markdown language. It is already a dependency and already lazy-loaded for workflows.
- **Save** (button or Ctrl/⌘+S) uploads the text with `PUT /content` and the item's current `If-Match`. That makes a new file version, with the same filename. Feedback reads "Saving…" and then "Saved · version 5". In a library with publishing, the result is a draft: the header shows **Draft** and the `⋯` menu offers **Publish**, the same action as the sheet's Publish tab.
- **Each save is a `file.updated` event**, so content workflows run on every save: text reading, preview, search, and AI classification if configured. That is why there is no autosave. The note under the Save button says so where workflows react to file changes.
- **Unsaved changes are protected:**
  - Leaving the editor, closing the viewer, stepping to another item or closing the tab asks first.
  - The buffer is also kept in this browser's local storage per item and version. After a crash, reopening offers "Restore unsaved changes from 14:02?".
- **Conflicts.** If the file changed since you opened it (412), a dialog shows who changed it and when, and offers three choices:
  - **Load their version**: your text is copied to the clipboard first;
  - **Save mine anyway**: re-read the version and save over theirs, which keeps their file in history if versioning is on;
  - **Save mine as a new file**: creates `on-call (copy).md` next to it.
- **Permission.** Edit appears only for people who may write the item, with `document.write`. Read-only tokens and readers never see it. Archived items stay editable, as today.
- **Limits.** Files over 2 MiB open read-only, with "Too large to edit in the browser. Download it, or use WebDAV." The text must be UTF-8.

**Cards and the File tab.** Text files have no rendered preview. A card shows the first lines of the file as a text excerpt: up to 4 KiB, fetched with a `Range` request, with fading at the bottom. The excerpt is cached like previews.

**New Markdown document.**
- **New → Markdown document** asks for a name and creates `<name>.md`, containing `# <name>`, with the library's normal defaults. It then opens the editor.
- A library with a required field that has no default asks for that value first, as **New item** already does.

### Plain-text handler (view and edit)

- View: a monospace font, line numbers, wrap on or off, and find.
- Edit: the same editor, save rules and conflict handling as Markdown, without the preview pane.
- Accepted extensions: `.txt` and `.log` (decision D3).

### Keyboard

| Key | In the list | In the viewer | In the editor |
| --- | --- | --- | --- |
| `o`, double-click | Open the file | — | — |
| Esc | Close the sheet | Close the viewer | Leave edit, asking first if there are unsaved changes |
| ← / → or `j` / `k` | — | Previous or next item | — |
| PgUp / PgDn, Home / End | — | Page up or down, first or last page | — |
| `+` / `-` / `0` | — | Zoom in, out, fit | — |
| Ctrl/⌘+F | — | Find in the file | Monaco find |
| `e` | — | Edit (editable types) | — |
| Ctrl/⌘+S | — | — | Save |
| `i` | — | Info panel | Info panel |
| `?` | Shortcut help | Shortcut help | Shortcut help |

The list's keyboard hint and the `?` help gain these entries.

### Mobile and touch

- The viewer is always full screen.
- The top bar shrinks to Back, the name and `⋯`. Page and zoom controls move to a bottom bar that hides while you scroll.
- Pinch zooms. Swiping changes items for images only; PDF and text scroll vertically.
- Editing uses the Write and Preview tabs over a textarea, with Save fixed to the top bar.

### Accessibility

- The viewer is a labelled modal dialog (`aria-modal`, titled with the file name). Focus moves into it and returns to the row or card that opened it.
- Every toolbar control has a name and a visible focus ring.
- The page number is announced politely when it changes.
- PDF text is selectable and readable by screen readers through PDF.js's text layer. Rendered Markdown is semantic HTML.
- Zoom works from the keyboard. The layer honours `prefers-reduced-motion`.

## Architecture

### Extension-First check

| Piece | Can it be an extension? | Result |
| --- | --- | --- |
| PDF and image viewers | **Yes.** They read `GET /content` (Range) and `/content/versions`. | Web-only handler modules. No core change. |
| Markdown and text editors | **Yes, once the type can be stored.** They write through `PUT /content` with `If-Match`. | Web-only handler modules. |
| Storing `.md` and `.txt` | **No.** The accepted types are hard-coded in storage, DMS, the database `CHECK`, the web upload list and the filter labels. | **Socket S1: a file type registry** (needs approval). |
| Text reading and search for text files | **No.** `doctext` is a `switch` that ends in "unsupported", so content workflows would fail on every text file and raise failed-run notifications. | Part of S1: a reader per type. |
| Not attempting previews of text files | The preview workflow would run on every text file only to record `unavailable`. | Part of S1: the preview role skips types without a renderer. |
| Knowing a row's file type without an extra request | Rows carry no file information today. | **Socket S2: a file summary on item rows** (needs approval). |
| Choosing a handler in the web app | There is no client registry. | **Socket S3: a client-side handler registry.** |
| The viewer itself, routes and Open buttons | This is the app shell. | Built as part of S3. |

### S1 — file type registry (server, core socket)

A small registry in `internal/storage` replaces the hard-coded set:

```go
type FileType struct {
    Media      blobs.MediaType // "text/markdown"
    Label      string          // "Markdown document"
    Extensions []string        // ".md", ".markdown"
    // Detect sees the first 8 KiB and the filename. Binary types ignore the
    // filename: their bytes alone decide, as today.
    Detect func(head []byte, filename string) bool
    // Validate, when set, checks the whole stream (UTF-8 for text types).
    Validate func() io.Writer // returns a validating sink; error at Close
}
func RegisterFileType(FileType)
```

- Core registers PDF, TIFF, JPEG, PNG and WebP with exactly today's signatures.
- A new extension package, `internal/textfiles`, registers `text/markdown` and `text/plain`. It then also registers:
  - a `doctext` reader for them: one page of text, under the existing limits;
  - "no preview" with the preview role, so the workflow skips these files instead of recording `unavailable`.
- **Detection policy for text.**
  - Binary formats keep the rule that bytes decide.
  - A text file is accepted only when its bytes are valid UTF-8 with no NUL. A UTF-8 BOM is allowed, and a 0-byte file is accepted for WebDAV's empty first write. The extension then picks the dialect.
  - A renamed binary is therefore still refused.
  - Text is always served as `text/plain` or `text/markdown` with `nosniff` and an attachment disposition, so it can never run as HTML.
- **Every path follows the registry:** `PutDocument`, which grows a filename parameter and an 8 KiB head; `dms.createBlob`; staged files; multipart create; WebDAV `PUT`; package import; and backup restore. Saving `notes.md` from Windows Explorer over WebDAV then works too.
- **Database (decision D1).** `blobs.content_type` stops being a fixed `IN (…)` list, becoming `CHECK (length(content_type) BETWEEN 3 AND 127 AND content_type LIKE '%/%')`. The registry is the source of truth. `blobs.MediaType` keeps named constants for the core types; extensions declare their own.
  - No migration is needed while no release is published. Today only the `models-laya-*` release exists.
  - If a release ships first, this becomes migration `0001`: a table rebuild on SQLite, and `DROP`/`ADD CONSTRAINT` on PostgreSQL.
- **Discovery.** `GET /auth/config` features gain `file_types: [{media_type, label, extensions}]`. From this the web derives the upload `accept` list, the file-type filter labels and the type icons, replacing the three hard-coded copies.
- **Docs and contract.** Update the upload sections of `README.md`, `docs/documents.md` and `docs/previews.md`, and the OpenAPI descriptions that list the five types.

### S2 — file summary on item rows (API, core socket)

Library rows in collection listings, smart folders and search hits gain:

```json
"file": {"content_type": "text/markdown", "filename": "on-call.md", "size": 2140, "version": 5}
```

This describes the caller's visible file, under the same visibility rule as `$file_type`. It drives:
- type icons in tables;
- whether to show **Edit**;
- the text excerpt instead of a preview;
- the viewer's first paint without a round trip.

Without S2, the viewer reads `/content/versions?$top=1` first, and cards learn that a file has no preview only after asking.

### S3 — client handler registry (web)

`web/src/features/files/handlers.ts`:

```ts
export type FileRef = { itemId: string; blobId?: string; contentType: string; filename: string; size: number };
export type FileHandler = {
  id: string;                       // 'pdf', 'image', 'markdown', 'text'
  accepts(file: FileRef): boolean;
  View: React.LazyExoticComponent<React.ComponentType<ViewerProps>>;
  Edit?: React.LazyExoticComponent<React.ComponentType<EditorProps>>;
  maxEditBytes?: number;
  Excerpt?: React.ComponentType<{ file: FileRef }>; // card fallback when no preview exists
};
export function registerFileHandler(handler: FileHandler): void;
export function handlerFor(file: FileRef): FileHandler | undefined;
```

- **Built-ins** live in `features/files/handlers/{pdf,image,markdown,text}`. Each is its own chunk: PDF.js, the Markdown renderer and Monaco load only when that type opens.
- **`ViewerProps`** carry the file, page state and zoom state, plus a `fetchContent(range?)` that adds the auth header. Bearer mode cannot use plain `src` URLs, so handlers never build URLs themselves.
- **`EditorProps`** add `save(text): Promise<SaveResult>`. It implements `If-Match`, the 412 dialog, drafts and the local-storage buffer once, for every editable handler.
- **The viewer shell** lives in `features/files/viewer.tsx` and owns everything except the canvas:
  - the URL state (`open`, `page`, `blob`, `edit`);
  - the top bar;
  - previous and next;
  - the Info panel;
  - the states.

  `collection-page.tsx` renders it when `search.open === 'file'`.
- **Dependencies** (recorded in `docs/dependencies.md` with licences):
  - `pdfjs-dist` (Apache-2.0), with `isEvalSupported: false`. It loads ranges through `httpHeaders` and is kept current, because it parses untrusted files.
  - `react-markdown` with `remark-gfm` (MIT). It renders to React elements with no `dangerouslySetInnerHTML`, and raw HTML is not rendered.
  - Monaco is reused.

### Security summary

- Nothing from a stored file runs as script: Markdown is rendered without raw HTML, and PDF.js runs without eval.
- `nosniff` and attachment disposition stay on `/content`. The viewer fetches bytes with the caller's credentials and never embeds a cross-origin frame.
- Remote images in Markdown load only when you ask; links get `noopener noreferrer`.
- Edit uses the same write checks as **Replace file**, and every save is audited as a file version.
- A Content-Security-Policy for the SPA would be good hardening. It is not required by this design and is tracked separately.

## Phases

| Phase | Delivers | Needs |
| --- | --- | --- |
| **1. Viewer** | S3 registry and viewer shell, **PDF** and **image** handlers, all the ways in, previous and next, Info panel, deep links, mobile, keyboard. TIFF shows its preview. | No core change. Can start now. |
| **2. Markdown and text** | S1 file type registry and `internal/textfiles`. **Markdown** view and edit, **plain text** view and edit, New → Markdown document, text excerpts on cards, conflict dialog, unsaved-changes guard and recovery. S2 if approved. | D1–D4 |
| **3. Depth** | Server page renditions (`GET /v1/items/{id}/pages/{n}?width=`, an extension of `documentpreview`): every page of a multi-page TIFF, and a light fallback for very large PDFs on low-end phones. OCR-backed find for scanned PDFs, from `/text`. Version compare for text files. | Phase 1 |
| **4. Later** | **Office files** via S1 (OOXML detection) plus **Open in desktop app** over WebDAV (`ms-word:ofe|u|…`) for WebDAV-enabled libraries. Anchoring comments to a PDF page or region, the comments plan's open need. CSV as a table view. | Separate approval each |

**Acceptance for phases 1 and 2**, tested on SQLite and PostgreSQL where the server is involved:

- **Opening.**
  - Every way in opens the right file at the right page.
  - Back and Esc return to the same list scroll position.
  - A deep link survives a reload.
- **Permissions.**
  - Readers never see **Edit**.
  - Draft readers see the draft file and ordinary readers the published one, in the viewer as at `/content`.
- **Markdown storage.**
  - A `.md` upload, a WebDAV save and a package round-trip are all accepted.
  - A renamed binary, invalid UTF-8 and NUL bytes are refused with 415.
  - Text files raise no failed or wasted preview and text runs.
  - Markdown text is searchable.
- **Markdown editing.**
  - Save makes a file version.
  - A stale save shows the conflict dialog with all three outcomes.
  - In a publishing library, a save is a draft.
  - Leaving with unsaved edits asks first, and the buffer recovers after a reload.
- **Safety.**
  - A Markdown file with `<script>`, `<img onerror>` and `javascript:` links renders inert.
  - A PDF with a JavaScript action does nothing.
- **Size.** The list page's bundle does not grow by more than the shell; PDF.js, the Markdown renderer and Monaco are separate chunks.

## Open decisions

| # | Question | Recommendation |
| --- | --- | --- |
| D1 | **S1: may core gain a file type registry, with `blobs.content_type` becoming an open, shape-checked value?** The alternative adds `text/markdown` and `text/plain` to the fixed `CHECK` and the constants, so each future type is a schema change. | Registry and shape check. That is the point of pluggable handlers, and phase 4 (Office) needs it. |
| D2 | **S2: may item rows carry a `file` summary?** | Yes. It saves a request per viewer open and per text card, and gives tables type icons. |
| D3 | **Which text extensions?** | `.md`/`.markdown` as Markdown; `.txt`/`.log` as plain text. Leave out `.csv`, `.json` and `.yaml` until they get their own handlers. |
| D4 | **What does a click on a library row or card do?** Details first (today), or the file first (Google Drive, SharePoint)? | Keep details first, and add Open everywhere plus double-click and `o`. If people want file-first, make it a personal preference later rather than a collection setting. |
| D5 | **Editing limit and editor.** 2 MiB, Monaco on desktop, textarea on touch? | Yes. It reuses a dependency we already ship. |
| D6 | **Remote images in Markdown:** blocked until asked (proposed), always shown, or never? | Blocked until asked, per file. |
