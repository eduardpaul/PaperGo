# File handlers (plan)

Status: **phases 1 to 4 implemented, except comments anchored to a PDF
page or region**, which need a core extension point that awaits a decision.
Implemented: the viewer and its PDF, image, TIFF, Markdown, text, Word and
CSV handlers, every way to open a file, the file summary on rows, any file
stored, Markdown and plain text edited and compared, page renditions, find
in scanned pages, Office text for search, Open in Word, Excel and
PowerPoint, and the organization's trusted hosts, as
[Viewing files](../documents.md#viewing-files),
[File types](../documents.md#file-types),
[Page renditions](../previews.md#page-renditions) and
[Trusted hosts](../trusted-hosts.md) describe. The product owner settled
every other decision ([below](#decisions)).

What phase 1 learned:
- PDF.js 6 uses JavaScript features that browsers shipped only recently, so the viewer loads its legacy build, which carries their polyfills.
- A click on a row opens the details sheet, which covers the list. So a row or card with a file waits a quarter of a second for a second click before it opens details.
- Close and Esc go back in the browser history, so they return to wherever Open was chosen: the sheet, the list or search. A link opened afresh closes to the list.
- Previous and next also work after a reload: the list reloads the same rows.
- Viewer URL changes (page, step, info tab) keep scroll positions; the router would otherwise restore stale ones into the PDF.
- There is no `?` shortcut help in the app yet. The keys show in tooltips, the right-click menu and the list's key hint.

What phases 2 and 2b learned:
- Monaco types through the browser's EditContext where it is supported, so its input is a plain element, not a text area. Page shortcuts recognize typing by the editor itself, or single keys such as `d` and `g` would act while someone types.
- The editor keeps its own text. Feeding every change back through React reset the cursor whenever keys arrived faster than renders, so outside text (a restored draft, their version after a conflict) replaces it only on an explicit revision.
- A save that finishes while someone keeps typing keeps the newer text as a draft.
- Office and operating-system housekeeping files would have become items once any file was accepted, so WebDAV keeps them in memory apart from the library.
- In the editor, `i` and other single keys type; Esc leaves edit unless one of Monaco's own popups is open.

What phase 3 learned:
- Go's TIFF decoder reads only the first page. Pointing a view of the file's header at each page's directory decodes every page without copying the file, so previews, text reading and page renditions share one TIFF reader.
- Page renditions render on request rather than as stored operations: a scan is read page by page as people scroll, and a small in-memory cache and fixed widths make repeat views free. Nothing new is stored, so nothing needs cleaning up.
- Scanned pages have no text layer, so find cannot highlight in them. It names the pages whose read text matches instead and goes to them; a file whose text was never read says so.
- A handler shown outside the viewer, as in a review, would take the page's keys (Ctrl+F among them), so handlers take keys only from inside the box that holds them there.

What phase 4 learned:
- docx-preview empties the containers it draws into, so the Word frame keeps its own styles apart from them. The frame is sandboxed without scripts and same-origin, so the app draws into it and handles its link clicks under the trusted-hosts rule, and the browser's own find works inside it.
- Office text reading needed no core change: `internal/officefiles` registers doctext readers, as `textfiles` does, and the search workflow lists the types; a type without a reader is skipped with 415.
- Open in Word needs only the library's WebDAV address and the folder names above the item, so it is a web feature; Office's own lock files were already kept out of the library.

The goal has three parts:
- **PaperGo stores any file.**
- A person browsing a library can **open the whole file** inside PaperGo, not just its 800-pixel first-page thumbnail.
- Opening it is easy from wherever they are.

Each file type that PaperGo can show is served by a **file handler**: a plug-in that views the type and, for some types, edits it. Markdown is the sample handler that does both. PDF gets a real viewer and images get zoom; Word documents are the next handler to add. What a handler may load from the internet, such as images in Markdown or links in PDFs, follows one organization-wide list of **trusted hosts**. Integrations follow the same list.

## Decisions

| # | Question | Decision |
| --- | --- | --- |
| D1 | Which files may be stored? | **Any file.** No hard-coded list. PaperGo identifies the type for display and processing, and refuses nothing for its type. |
| D2 | Item rows carry a `file` summary? | **Yes.** |
| D3 | Text extensions | `.md`/`.markdown` open as Markdown and `.txt`/`.log` as plain text, for now. More handlers follow, starting with `.docx`. |
| D4 | Click on a row or card | **Opens details, as today.** From there and from every other place the file must be one obvious click away: see [Ways to open a file](#ways-to-open-a-file). |
| D5 | Editing limit | A configuration setting, `FILE_EDIT_MAX_BYTES` (default 2 MiB). Raising it is allowed. |
| D6 | Remote content | One **organization-wide list of trusted hosts**. Content from them opens without asking. Changing the list changes every feature that reaches outside PaperGo, integrations included. See [Trusted hosts](#trusted-hosts). |
| O1 | Trusted hosts at first | **`*`: every public host is trusted.** Administrators narrow the list by removing `*` and adding the hosts they want. |
| O2 | Viewing Word files | **In the browser, with an npm package:** [`docx-preview`](https://www.npmjs.com/package/docx-preview) (Apache-2.0) in phase 4. |

## Product view

### Who it is for

| Person | Job | What they need today and can't get |
| --- | --- | --- |
| Reader | "What does page 7 of this contract say?" | To read every page without downloading the file and finding it in a downloads folder. |
| Reviewer | "Check the signature block, then the next invoice." | To zoom in, then step to the next item in the same view without closing anything. |
| Writer | "Keep the team's runbook as Markdown next to the PDFs." | To store `.md` files at all (they're refused today), and to edit one in place with a preview. |
| Office user | "Our contracts are Word files." | To store `.docx`, `.xlsx` and every other file the team uses. Today PaperGo refuses them. |
| Mobile user | "Show me the delivery note." | A full-screen, pinch-zoomable view on a phone. |
| Link sharer | "Look at page 3." | A link that opens the file at that page. |
| Administrator | "Which outside sites may PaperGo talk to or show?" | One list, in one place, that every feature obeys. |

### What exists today

| Building block | State | Consequence |
| --- | --- | --- |
| Accepted file types | Hard-coded to PDF, TIFF, JPEG, PNG and WebP, detected from the first 12 bytes (`internal/storage/documents.go`). The same set is a `CHECK` on `blobs.content_type`, the `blobs.MediaType` constants, the web upload `accept` list and the file-type filter labels. | Every other file returns 415, including WebDAV saves from Explorer, Finder and Office, and package imports. |
| Processing | Preview (`internal/preview`), text reading (`internal/doctext`), OCR, image optimisation and Photos to PDF each assume one of those five types. | Once anything can be stored, each processor must say which types it handles and skip the rest. Otherwise workflows fail. |
| Preview | `GET /v1/items/{id}/preview`: a WebP of page 1, longest side at most 800 px. It shows in gallery and board cards and in the item sheet's **File** tab. | You can never see page 2. There is no zoom, and clicking the preview does nothing. |
| Original file | `GET /v1/items/{id}/content`: always `attachment` and `nosniff`, with Range support. The web app only ever saves it to disk. | Opening a file means downloading it. |
| Editing a file | `PUT /v1/items/{id}/content` with `If-Match` makes a new file version. The **Replace file** button uploads one. | The concurrency model an editor needs already exists. |
| Viewer routes | None. An item is always `?item=<id>` on the list route and opens in the side sheet. Search, smart folders and relationship links all link that way. | A viewer can reuse `?item=` and every existing link gains it. |
| Outbound hosts | iCalendar integrations accept any public HTTPS host, with no allow list. Links in URL fields open anything. There is no organization-level host policy. | [Trusted hosts](#trusted-hosts) is new, and integrations change to follow it. |
| Web extension points | None. Layouts, field editors and sheet tabs are `if` chains. | Handlers need a small client-side registry. |
| Rendering libraries | Monaco is a dependency, lazily loaded for the workflow editor. There is no Markdown renderer and no PDF renderer. | New dependencies: a Markdown renderer and PDF.js. |
| Security headers | `nosniff` and `no-store` are set. There is no Content-Security-Policy. | Storing HTML and SVG makes a sandbox header on file downloads necessary. Rendered Markdown must be safe by construction. |

### Principles

1. **Store anything, show what we can.** Storage never judges a file by its
   type. Handlers and processors declare the types they understand. Every
   other file is still a first-class item: metadata, versions, search by
   name, workflows, WebDAV and download.
2. **Preview is a glance, the viewer is the file.** Thumbnails stay where they
   are. One consistent **Open** action leads from each of them to the
   full-screen viewer.
3. **Details first, the file one click away.** Clicking a row still opens the details sheet. The file is always visible there, and from every other place it is one labelled click away.
4. **The viewer is a place you can link to.** It lives in the URL, survives a
   reload, works in a new tab and closes with Back.
5. **Saving is a file version.** An editor save is exactly a **Replace file**,
   with the same history, publishing, workflows, audit and conflict rules.
   There are no hidden autosave versions.
6. **One trust list.** Whether PaperGo may fetch or show something from
   another host is decided by one organization setting, not per feature.
7. **A handler is a plug-in.** Core knows file types only through the
   registries below. Viewers and editors live in their own lazily loaded
   bundles, built on the public API.

### Out of scope

- Real-time co-editing, presence and in-browser editing of PDFs, images or
  Office files. The editor is single-user, with optimistic concurrency.
- Annotations, and comments anchored to a page or region ([phase 4](#phases)).
- Converting between formats, such as Markdown to PDF.
- Malware scanning of uploads. This matters more once anything can be stored, so it is a separate proposal; `MAX_UPLOAD_BYTES` still applies.

## The experience

### Words

| Term | Meaning in the UI |
| --- | --- |
| **Preview** | The thumbnail on cards and in the sheet. Unchanged. |
| **Open** | Opens the viewer: the whole file, full screen. It is shown only when a handler exists for the type. |
| **Edit** | Available for editable types (Markdown and plain text for now). It turns the viewer into an editor. |
| **Download** | Saves the original file. It is always available, and it is the primary action when no handler exists. |
| **Save** | Makes a new file version. In libraries with publishing, a save is a draft. |
| **Trusted host** | An outside site PaperGo may fetch from and show without asking. |

### Ways to open a file

D4 keeps a click on a row or card opening the details sheet. So this section
designs the paths to the file: always visible, always the same icon and
label, and always a real link (`<a href>`), so Ctrl/⌘-click and middle-click
open the viewer in a new tab.

**One control, three faces.** The **file action** is a button whose label and icon come from the handler:
- **Open** (expand icon), for a viewable type;
- **Download** (arrow-down icon), for a type with no handler;
- later, **Open in Word** and the like, for Office files in WebDAV-enabled libraries.

The same component is used in every place below, so the file looks and behaves the same everywhere.

#### 1. The item sheet: the file bar

The sheet opens on **Details**, so the file can't live only on the **File**
tab. Every library item's sheet gets a **file bar** between the title and
the tabs, visible on every tab:

```text
┌ Sheet ──────────────────────────────────────────────────────┐
│ ACME-0042                                           ⋯   ✕   │
│ ┌──────┐  ACME-0042.pdf                                     │
│ │ thumb│  PDF · 1.2 MB · version 5 · Draft                  │
│ │      │  [⤢ Open]  [✎ Edit]  [⤓]                           │
│ └──────┘                                                    │
│  Details   File   Versions   Links   Workflows   Discussion │
│ ─────────────────────────────────────────────────────────── │
```

- **Thumbnail.** 64 px, from the existing preview. For a type with no preview it shows the type icon. A text file shows its first lines. Clicking the thumbnail opens the file; the cursor is a zoom-in cursor.
- **Primary button.** **Open**, or **Download** when no handler exists.
- **Edit** appears for editable types when the person may write. **Download** is an icon button beside the primary button.
- **The `⋯` menu** gains Replace file and Copy file link.
- **An item with no file yet** shows "No file yet" and an **Upload file** button. That is today's Replace action, given a name that fits.
- **Mobile.** The sheet is full screen, and the file bar stays at the top. A sticky bottom bar would sit over the Details tab's Save footer.

The **File** tab keeps the large preview and the version list. Clicking the preview opens the viewer, and each version row gets **Open** for that version.

#### 2. Table rows

```text
│ ☐ │ 📄 ACME-0042.pdf                    [⤢] │ ACME Ltd │ 1,240.00 │
│ ☐ │ 📝 on-call.md                        [⤢] │ —        │ —        │
│ ☐ │ 📦 export.zip                        [⤓] │ —        │ —        │
```

- **The name cell** shows the file-type icon (from the `file` summary, D2) and, at its right edge, the file action as an icon button.
- **When the button shows:**
  - while the row is hovered or focused, and on the open row;
  - always on touch screens.

  Elsewhere it stays hidden, so the table stays calm.
- **Its tooltip** names the action and the key: "Open (O)".
- **Double-click** a row to open the file. A single click waits a moment for a second click before it opens details, because the sheet would otherwise cover the row.
- **Keys:** `o` opens the file, `Enter` opens details (unchanged) and `d` downloads.
- **The list's keyboard hint** at the bottom reads "↑↓ move · Enter details · O open file · X select".

#### 3. Gallery and board cards

```text
┌──────────────────────┐
│                      │
│      thumbnail       │
│               [⤢]    │  ← appears on hover or focus; always shown on touch
├──────────────────────┤
│ ACME-0042            │  ← click anywhere outside the button: details
│ ACME Ltd · 1,240.00  │
└──────────────────────┘
```

- The card is the details button, as today.
- The file action sits on the thumbnail's bottom-right corner, and is keyboard-reachable as the card's second tab stop.
- Text files show an excerpt instead of a thumbnail. Files with no handler show a large type icon.

#### 4. Right-click and `⋯` menus

The first group of the item menu becomes:

| Item | Shown when |
| --- | --- |
| Open file (O) | A handler exists |
| Edit (E) | Editable type and write access |
| Download (D) | The item has a file |
| Details (Enter) | Always |

The menu is the same for one item in tables, cards, search results and smart folders.

#### 5. Everywhere else an item appears

| Place | File path |
| --- | --- |
| Global search results | Each document hit gets the file action at its right edge. `Enter` opens details; `o` opens the file. A hit found in a PDF's text opens the viewer at that page once [phase 3](#phases) knows the page. |
| Smart folders | The same rows and cards as collections, with the same controls. |
| Relationship explorer and Links tab | Each linked document shows the file action beside its name. |
| Notifications and mentions | Unchanged: they open the item with its Discussion tab. The file bar above the tabs is one click from the file. |
| Upload finished toast | "Uploaded on-call.md · **Open**". When several files were uploaded, it offers "Show". |
| New → Markdown document | Opens straight into the editor. |
| Workflow review tasks | The staged-file comparison uses the same handlers, in place of today's iframe ([phase 3](#phases)). |
| Command palette | When an item is open: "Open file", "Edit file" and "Download file". |
| Copied links | **Copy file link** copies `…?item=<id>&open=file`, and the page when there is one. Pasting it opens the viewer directly. |

#### Discoverability

- The first time someone opens a library sheet, the **Open** button gets a one-time coach mark: "Open the whole file here. Shortcut: O". It is dismissed for good and remembered per browser.
- The right-click menu shows **O**, **D** and **Enter** beside its items.

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
- **Previous and next** step through the items of the list as it was showing: the same view, folder, filter, search and sort. The viewer steps through the rows the list has loaded; a reload loads the same rows. Where the list does not know the order, as in board, calendar and navigation views or from search, the arrows are hidden rather than guessed. An item with no handler shows the download card (see the states below), so stepping never stops.
- **Info panel** (`i`) reuses the item sheet's tabs:
  - **Details** (editable as in the sheet);
  - **Discussion**;
  - **Versions**.

  It is closed by default and remembered per browser. Below 1024 px it opens as an overlay sheet.
- **The `⋯` menu** holds:
  - Copy file link;
  - Open details;
  - Replace file;
  - Run workflow;
  - Archive and Delete, which behave as in the sheet.
- **Version view.** When viewing an earlier version, a banner reads "You are viewing version 3 (uploaded by Ana, 12 May). **Restore this version** · **View current**". Edit is disabled.
- **States.** Each one fills the canvas and never leaves it blank:
  - loading: a skeleton;
  - the item has no file yet: **Upload file**;
  - the version was reclaimed (410): "This version's bytes were reclaimed";
  - the file is broken or protected (PDF password prompt, client-side only);
  - no handler for the type: the type icon, file name, type, size and a large **Download** button;
  - offline or failed: a retry.

### PDF handler (view)

- Continuous vertical scroll with lazy page rendering.
- Optional page thumbnails rail (`t`).
- A page box you can type into.
- Zoom: fit width (default), fit page, 50–400 %, Ctrl/⌘ + wheel, and pinch.
- Rotate the view (does not change the file).
- **Find** (Ctrl/⌘+F) over the PDF's text layer, with a hit count and next/previous.
- Text selection and copy.
- Links inside the PDF follow the [trusted hosts](#trusted-hosts) rule.
- **Scanned PDFs** have no text layer. Find says "This PDF has no text layer". [Phase 3](#phases) searches PaperGo's OCR text instead and jumps to the matching pages.
- `&page=n` deep links. The current page updates the URL with `replace`, so Back closes the viewer rather than stepping through pages.

### Image handler (view)

- JPEG, PNG, WebP and GIF are shown at full resolution from the original file.
- Fit by default.
- Double-click or `1` toggles 100 %.
- Wheel or pinch zooms; drag pans.
- Rotate the view.
- On touch screens, swiping left or right goes to the previous or next item when the image is at fit size.
- **SVG is never rendered.** It can carry script, so it downloads, like HTML.
- **TIFF:** browsers can't decode it.
  - Phase 1 shows the 800 px preview with the note "Showing a preview. Download for full resolution."
  - Phase 3 adds server-rendered pages, which also gives multi-page TIFFs every page.

### Markdown handler (view and edit) — the sample

**View.**
- Rendered GitHub-flavoured Markdown: headings, lists, task lists, tables, code blocks and links.
- A readable measure (about 72 characters) in the app's typography.
- An outline of the headings on wide screens, for jumping around.
- Raw HTML in the file is shown as text, never rendered.
- Images and links follow the [trusted hosts](#trusted-hosts) rule.

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
- **Limits.** Files larger than `FILE_EDIT_MAX_BYTES` (default 2 MiB, configurable) open read-only, with "Too large to edit in the browser (limit 2 MiB). Download it, or use WebDAV." The text must be UTF-8; otherwise it opens read-only with "This file isn't UTF-8 text".

**Cards and the sheet thumbnail.** Text files have no rendered preview. A card shows the first lines of the file as a text excerpt: up to 4 KiB, fetched with a `Range` request, with fading at the bottom. The excerpt is cached like previews.

**New Markdown document.**
- **New → Markdown document** asks for a name and creates `<name>.md`, containing `# <name>`, with the library's normal defaults. It then opens the editor.
- A library with a required field that has no default asks for that value first, as **New item** already does.

### Plain-text handler (view and edit)

- View: a monospace font, line numbers, wrap on or off, and find.
- Edit: the same editor, save rules and conflict handling as Markdown, without the preview pane.
- It handles `.txt` and `.log`.

### Every other file

- A file with no handler is an ordinary item. It has metadata, versions, permissions, publishing, workflows, WebDAV, duplicate detection and search by name and fields.
- Its file action is **Download**. Its card and thumbnail show the type icon, the label (for example "Word document") and the size.
- Type icons come in families: document, spreadsheet, presentation, image, PDF, text, archive, audio, video, code and generic.

### Trusted hosts

**Administration → Security → Trusted hosts** holds the organization's single list of outside hosts that PaperGo may fetch from and show without asking.

```text
┌ Trusted hosts ───────────────────────────────────────────────────────────────┐
│ PaperGo shows and fetches content from these hosts without asking.           │
│ Other hosts are blocked or need confirmation, depending on the feature.      │
│                                                                              │
│  ⚠ * trusts every public host. Remove it to trust only the hosts listed.     │
│                                                                              │
│  *                                       Used by 3 integrations      [Remove]│
│  *.sharepoint.com                        —                           [Remove]│
│  docs.example.com                        —                           [Remove]│
│                                                                              │
│  [ host, *.domain or *                               ]  [Add]                │
│                                                                              │
│  Where this applies: Markdown images and links · links in PDFs · URL fields  │
│  · calendar integrations · future handlers and integrations                  │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Entries.**
- An entry is one of:
  - a host name (`docs.example.com`);
  - a wildcard for subdomains (`*.example.com`, which does not match `example.com` itself);
  - `*`, which trusts every public host.
- Entries are lowercase and stored in their ASCII (punycode) form. IP addresses and ports are not allowed.
- The list holds at most 200 entries.
- **A new organization starts with `*`**, so everything works at first. An administrator restricts PaperGo by removing `*` and adding the hosts the organization uses.
- While `*` is in the list, the page shows the warning in the mock-up, and no feature offers **Trust host**: there is nothing left to trust.
- The other entries can be added before `*` is removed, so narrowing the list never breaks anything in between.

**Each entry shows where it is used**, for example "Used by 3 integrations". Removing an entry that is in use asks first and names what will stop.

**Where it applies.** Changing the list takes effect everywhere at once:

| Feature | Host on the list | Host not on the list |
| --- | --- | --- |
| Images and media in Markdown (and future handlers) | Load with the document | A placeholder: "Image from example.org not shown · **Show once**". Administrators also get **Trust example.org**. |
| Links in Markdown, PDFs and URL fields | Open in a new tab (`noopener noreferrer`) | A confirmation: "You're leaving PaperGo for **example.org**". The confirmation shows the full URL. |
| iCalendar integrations (and future integrations that fetch) | Allowed | **Refused** when added or edited: "example.org isn't a trusted host. Ask an administrator to trust it." Administrators can trust it from that message. An existing integration whose host is removed stops refreshing and shows "Host no longer trusted". Its items stay, and it resumes when the host is trusted again. |

**What it never does.**
- It never opens private networks. The existing rule that server-side fetches go only to public addresses over HTTPS stays as a floor beneath the list.
- It never covers what the operator configures: AI endpoints, OIDC, the MCP URL and inbound CORS stay in deployment configuration.

**Who.**
- Reading the list requires an active account, because the web app needs it to render.
- Changing it requires `organization.manage`.
- Changes are versioned with `If-Match` like other organization settings and audited as `settings.trusted_hosts`. Open browsers pick them up the next time the window regains focus.

### Keyboard

| Key | In the list | In the viewer | In the editor |
| --- | --- | --- | --- |
| `o`, double-click | Open the file | — | — |
| `Enter` | Open details | — | — |
| `d` | Download | Download | — |
| Esc | Close the sheet | Close the viewer | Leave edit, asking first if there are unsaved changes |
| ← / → or `j` / `k` | — | Previous or next item | — |
| PgUp / PgDn, Home / End | — | Page up or down, first or last page | — |
| `+` / `-` / `0` | — | Zoom in, out, fit | — |
| Ctrl/⌘+F | — | Find in the file | Monaco find |
| `e` | Edit (editable types) | Edit | — |
| Ctrl/⌘+S | — | — | Save |
| `i` | — | Info panel | Info panel |
| `?` | Shortcut help | Shortcut help | Shortcut help |

### Mobile and touch

- The viewer is always full screen.
- The top bar shrinks to Back, the name and `⋯`. Page and zoom controls move to a bottom bar that hides while you scroll.
- Pinch zooms. Swiping changes items for images only; PDF and text scroll vertically.
- Editing uses the Write and Preview tabs over a textarea, with Save fixed to the top bar.
- File actions on rows and cards are always visible on touch screens, because there is no hover.

### Accessibility

- The viewer is a labelled modal dialog (`aria-modal`, titled with the file name). Focus moves into it and returns to the row or card that opened it.
- Every toolbar control has a name and a visible focus ring. Row and card file actions are real links with names such as "Open ACME-0042.pdf".
- The page number is announced politely when it changes.
- PDF text is selectable and readable by screen readers through PDF.js's text layer. Rendered Markdown is semantic HTML.
- Zoom works from the keyboard. The layer honours `prefers-reduced-motion`.

## Architecture

### Extension-First check

| Piece | Where it lives | Why |
| --- | --- | --- |
| Accepting any file and identifying its type | **Core:** S1 file type identification. | Decided in D1. Storage, DMS, WebDAV, import and the schema all gate on type today. |
| Processors (preview, text, OCR, optimise, compose) saying which types they take | **Core:** S1 processor capabilities. | Otherwise every new type makes workflows fail. |
| Text reading for `.md` and `.txt` | **Extension:** `internal/textfiles` registers a `doctext` reader. | It plugs into S1. |
| File summary on rows | **Core:** S2. | Decided in D2. |
| Trusted hosts | **Core:** S4, an organization setting with one Go check. | Decided in D6: integrations and every handler must share it. |
| Client handler registry, viewer, file action and file bar | **Web core:** S3. | The app shell. |
| PDF, image, Markdown and text handlers | **Web extensions** on S3. | They use only the public API. |
| Word and later handlers | **Web extensions** on S3, plus, if needed, a server converter registered through S1. | Phase 4. |

### S1 — any file, identified (server)

**Storage accepts every file.**
- `storage.PutDocument` no longer rejects anything. It reads the first 8 KiB and the filename and asks the **type registry** for the media type.
- `storage.ErrUnsupportedFile` is deleted, along with the 415 `unsupported_file_type` on uploads, the `SupportedDocumentType` gates in `dms` (`content.go`, `staged.go`) and their WebDAV mapping.
- `MAX_UPLOAD_BYTES` still applies.

```go
// internal/storage
type FileType struct {
    Media      blobs.MediaType // "text/markdown"
    Label      string          // "Markdown document"
    Family     string          // "text", "document", "spreadsheet", "image", ...
    Extensions []string        // ".md", ".markdown"
    // Signature, when set, recognises the type from its first bytes alone.
    Signature func(head []byte) bool
    // Text types require the head to be UTF-8 without NUL bytes.
    Text bool
}
func RegisterFileType(FileType)
func Identify(head []byte, filename string) blobs.MediaType
```

**How `Identify` works.**
1. A registered **signature** wins: a PDF named `notes.txt` is a PDF.
2. Otherwise the **extension** decides. A text type also requires its head to be UTF-8 without NUL bytes; if it isn't, the file is `application/octet-stream`.
3. Otherwise the file is `application/octet-stream`.

The built-in table is deterministic and maintained in code. Go's `mime.TypeByExtension` reads the operating system's tables and would differ between Linux, Windows and containers. The table covers:
- the five current types;
- GIF;
- Markdown and plain text;
- CSV and JSON;
- the Office and OpenDocument formats;
- RTF;
- ZIP;
- email (`.eml`, `.msg`);
- common audio and video;
- HTML and SVG (stored, never rendered).

The type a file gets is stored per file version, so a handler added later, such as `.docx`, works on files uploaded before it existed.

**Database.**
- `blobs.content_type` becomes `CHECK (length(content_type) BETWEEN 3 AND 127 AND content_type LIKE '%/%')`, in both masters.
- `blobs.MediaType` keeps named constants for the types core refers to.
- No migration is needed while no PaperGo release is published. If a release ships first, this becomes migration `0001`: a table rebuild on SQLite, and `DROP`/`ADD CONSTRAINT` on PostgreSQL.

**Serving files safely.** Anything can now be stored, including HTML, SVG and executables. So every byte route — `/content`, staged files, artifacts and WebDAV `GET` — sends:
- `Content-Disposition: attachment`;
- `X-Content-Type-Options: nosniff`;
- `Content-Security-Policy: sandbox; default-src 'none'`.

The last header is new; it makes a file opened directly in a browser inert. The viewer fetches bytes with `fetch`, so none of these headers get in its way.

**Processor capabilities.**
- Each processor declares the types it handles:
  - preview: PDF, TIFF, JPEG, PNG and WebP;
  - text reading: those, plus whatever `doctext` readers are registered;
  - OCR and image optimisation: images;
  - Photos to PDF: images.
- A request for another type is refused up front with `415 unsupported_file_type`, before any operation is queued.
- The built-in workflows check the file's type first and end as **skipped**, not failed, so no failed-run notifications follow.
- The capabilities are published in `GET /auth/config` features as `file_types: [{media_type, label, family, extensions, preview, text}]`.
- The web app derives the type labels, icons and the `$file_type` filter choices from that list. The upload `accept` attribute is removed.

**Text extension.** `internal/textfiles` registers a `doctext` reader for `text/markdown` and `text/plain`. It returns one page of text under the existing limits, so these files become searchable by content.

**WebDAV scratch files.**
- Once anything is accepted, Explorer, Finder and Office would create items for their housekeeping files:
  - `~$*.docx` lock files;
  - `.~lock.*#`;
  - `Thumbs.db`, `desktop.ini` and `.DS_Store`;
  - `._*` AppleDouble files.
- WebDAV keeps these names in a per-library, in-memory scratch area instead: at most 1 MiB each, with a one-hour lifetime, like its locks.
- They never become items or fire workflows, and Office's save-and-rename flow keeps working.

**Docs and contract.** Update the upload sections of `README.md` and `docs/documents.md`, and the processor sections of `docs/previews.md`, `docs/ocr.md` and the workflows docs. Remove the five-type wording from the OpenAPI descriptions.

### S2 — file summary on item rows (API)

Library rows in collection listings, smart folders, search hits and `GET /v1/resources/{id}` gain:

```json
"file": {"content_type": "text/markdown", "filename": "on-call.md", "size": 2140, "version": 5}
```

- It describes the caller's visible file, under the same visibility rule as `$file_type`, and is `null` before the first upload.
- It drives the type icons, the choice between Open and Download, whether **Edit** shows, and text excerpts in place of previews.
- It lets the viewer's first paint happen without a round trip.

### S3 — client handler registry (web)

`web/src/features/files/handlers.ts`:

```ts
export type FileRef = { itemId: string; blobId?: string; contentType: string; filename: string; size: number };
export type FileHandler = {
  id: string;                       // 'pdf', 'image', 'markdown', 'text'; later 'docx'
  accepts(file: FileRef): boolean;
  View: React.LazyExoticComponent<React.ComponentType<ViewerProps>>;
  Edit?: React.LazyExoticComponent<React.ComponentType<EditorProps>>;
  Excerpt?: React.ComponentType<{ file: FileRef }>; // card thumbnail when no preview exists
};
export function registerFileHandler(handler: FileHandler): void;
export function handlerFor(file: FileRef): FileHandler | undefined;
```

- **Built-ins** live in `features/files/handlers/{pdf,image,markdown,text}`. Each is its own chunk: PDF.js, the Markdown renderer and Monaco load only when that type opens.
- **`ViewerProps`** give handlers:
  - the file;
  - page state and zoom state;
  - `fetchContent(range?)`, which adds the auth header. Bearer mode cannot use plain `src` URLs, so handlers never build URLs themselves;
  - `trust(url)`, the S4 check, used by every handler for images, media and links.
- **`EditorProps`** add `save(text): Promise<SaveResult>`. It implements `If-Match`, the 412 dialog, drafts and the local-storage buffer once, for every editable handler. The edit limit comes from `FILE_EDIT_MAX_BYTES` via `/auth/config`.
- **Shared components** in `features/files/`:
  - `FileAction` (Open, Download or Edit, always a link);
  - `FileBar` (the sheet strip);
  - `FileThumb` (preview, excerpt or type icon);
  - `TrustedLink` (a link that follows S4);
  - `viewer.tsx` (the viewer shell).
- **The viewer shell** owns everything except the canvas: the URL state (`open`, `page`, `blob`, `edit`), the top bar, previous and next, the Info panel and the states. `collection-page.tsx` and the smart-folder page render it when `search.open === 'file'`.
- **Dependencies** (recorded in `docs/dependencies.md` with licences):
  - `pdfjs-dist` (Apache-2.0), with `isEvalSupported: false`. It loads ranges through `httpHeaders` and is kept current, because it parses untrusted files.
  - `react-markdown` with `remark-gfm` (MIT). It renders to React elements with no `dangerouslySetInnerHTML`, and raw HTML is not rendered.
  - Monaco is reused.

### S4 — trusted hosts (server and web)

- **Storage.** It is stored as organization setting `trusted_hosts` in the existing settings table, beside the comment settings.
- **API.**
  - `GET /v1/organization/settings/trusted-hosts` is readable by any active user.
  - `PUT` on the same path requires `organization.manage` and `If-Match`.
- **Go check.** `internal/hosts` holds the entry rules and `hosts.Trusted(entries, host)`; `dms` reads the setting and hands fetches a check. Every server feature that fetches calls it. Today that is `internal/calendar/fetch.go`, beside its existing public-address check, on every connection and redirect.
  - Integrations check it when they are added or edited, and again on each refresh.
  - A refresh whose host is no longer trusted fetches nothing and records the error "Host no longer trusted" in the integration's existing `error` column. Its items stay untouched, and the next refresh after the host is trusted again succeeds. The integration page shows that error with a **Trust host** action for administrators.
- **Usage counts.** The admin page shows, per entry, how many integrations use it, from `GET .../trusted-hosts/usage`: the host of each integration's decrypted configuration. A second provider adds its own host reader there.
- **Web.** `useTrustedHosts()` reads the list when the app loads and again when the window regains focus. `TrustedLink`, the handlers' `trust(url)` and the URL-field display all use it. The "Leaving PaperGo" dialog is one shared component.

### Security summary

- **Stored files never run.**
  - Any file can be stored, but none is ever served inline or sniffable.
  - Byte routes add a sandbox CSP.
  - Markdown is rendered without raw HTML.
  - PDF.js runs without eval.
  - SVG and HTML have no viewer.
- **Outbound and remote content.**
  - Outbound fetches stay public-address-only, HTTPS-only and bounded.
  - Their hosts must also be trusted.
  - Remote content in documents loads only from trusted hosts or on an explicit "Show once".
- **Edits and the browser.**
  - Edit uses the same write checks as **Replace file**, and every save is audited as a file version.
  - A Content-Security-Policy for the web app itself would be good hardening, and is tracked separately.

## Phases

| Phase | Delivers | Needs |
| --- | --- | --- |
| **1. Viewer and ways in** | S2 file summary. S3 registry, viewer shell, `FileAction`, `FileBar` and `FileThumb`. **PDF** and **image** handlers. Every way in from [Ways to open a file](#ways-to-open-a-file). Previous and next, Info panel, deep links, mobile and keyboard. TIFF shows its preview. | — |
| **2. Any file, Markdown and text** | S1: any file stored, `Identify`, the sandbox header on byte routes, processor capabilities and skipped built-ins, WebDAV scratch files, `internal/textfiles`. **Markdown** view and edit, **plain text** view and edit, New → Markdown document, text excerpts, conflict dialog, unsaved-changes guard and recovery, `FILE_EDIT_MAX_BYTES`. | Phase 1 |
| **2b. Trusted hosts** | S4: the setting, admin page, `internal/hosts`, and the rules for integrations, links, URL fields and Markdown images. | Can be built alongside phase 2. It must ship no later than the Markdown handler, which uses it. |
| **3. Depth** | Server page renditions (`GET /v1/items/{id}/pages/{n}?width=`, an extension of `documentpreview`): every page of a multi-page TIFF, and a light fallback for very large PDFs on low-end phones. OCR-backed find for scanned PDFs. Hits found in a PDF's text open at their page. Workflow review uses handlers. Version compare for text files. | Phase 2 |
| **4. Word and beyond** | A **`.docx` view handler** rendered in the browser with `docx-preview` (see [Word files](#word-files)), so it needs no server conversion. **Open in Word** for WebDAV-enabled libraries (`ms-word:ofe|u|…`), with the same pattern for Excel and PowerPoint. CSV as a table view. Comments anchored to a PDF page or region. | Phase 2 |

**Acceptance for phases 1, 2 and 2b**, tested on SQLite and PostgreSQL where the server is involved:

- **Opening.**
  - Every way in shows the right action: Open for viewable types, Download otherwise.
  - Ctrl/⌘-click opens the viewer in a new tab, at the right file and page.
  - Back and Esc return to the same list scroll position, and a deep link survives a reload.
- **Permissions.**
  - Readers never see **Edit**.
  - Draft readers see the draft file and ordinary readers the published one, in the viewer, the file bar and the S2 summary alike.
- **Any file.**
  - A `.docx`, a `.zip`, an `.exe`, an `.html` and an `.svg` all upload through REST, multipart, WebDAV and package import, each with the right media type.
  - Each is served as an attachment with the sandbox CSP.
  - A PDF renamed `.txt` is identified as a PDF.
  - No built-in workflow fails on these files: they end as skipped.
  - The file-type filter lists the known types.
- **WebDAV.** Saving a `.docx` from Word over WebDAV creates exactly one item and one new version per save, and no `~$` items.
- **Markdown.**
  - It is searchable.
  - Save makes a file version, and a stale save shows the conflict dialog with all three outcomes.
  - In a publishing library a save is a draft.
  - Leaving with unsaved edits asks first, and the buffer recovers after a reload.
  - Files over `FILE_EDIT_MAX_BYTES` open read-only.
- **Trusted hosts.**
  - With the default `*`, images load, links open and any public iCalendar URL is accepted.
  - With `*` removed, an image from a trusted host loads; one from an untrusted host shows the placeholder.
  - Untrusted links ask first.
  - An iCalendar URL on an untrusted host is refused.
  - Removing a host stops the integrations using it at their next refresh, and trusting it again resumes them.
  - Private addresses stay refused even when trusted.
- **Safety.**
  - A Markdown file with `<script>`, `<img onerror>` and `javascript:` links renders inert.
  - A PDF with a JavaScript action does nothing.
- **Size.** The list page's bundle grows by no more than the shell and the file controls; PDF.js, the Markdown renderer and Monaco are separate chunks.

## Word files

Phase 4 views `.docx` in the browser with
[`docx-preview`](https://www.npmjs.com/package/docx-preview) (Apache-2.0,
0.4.x). It converts the document's XML into HTML in the browser and keeps
its page layout, fonts, tables, headers and footers well enough to read and
review. It is a view handler on S3 like the others:

- **Loading.** It loads lazily, with its ZIP reader, only when a Word file opens, and reads the bytes through `fetchContent`.
- **Isolation.** It renders inside a sandboxed `<iframe srcdoc>` with no `allow-scripts`, so nothing in a document can run or reach the app.
- **Links and images.** They follow trusted hosts. Images embedded in the file always show; they are part of the file.
- **Fidelity.** The layout is approximate: fields, tracked changes, comments and complex floating objects may differ from Word. The viewer says so in its `⋯` menu ("Shown in a simplified layout"), next to **Download**.
- **Editing.** It stays in Word: **Open in Word** for WebDAV-enabled libraries saves back as a new file version.
- **Alternative.** [`mammoth`](https://www.npmjs.com/package/mammoth) (BSD-2-Clause) converts to clean, reflowed HTML without page layout. It could become a "Reading view" toggle if the layout view proves too heavy on phones.
- **Search.** Word text becomes searchable through a server-side `doctext` reader for OOXML, a small Go reader of `word/document.xml`, registered by the same phase.
