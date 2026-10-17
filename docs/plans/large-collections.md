# Very large lists and libraries

How PaperGo behaves, and how usable it is, when one list holds 10,000 items and one library 10,000 documents, with the
500,000-item numbers measured on the way there. Data goes in through the public API (`cmd/loadtest`), API timings come
from `loadtest measure`, and the web UI is walked with Playwright (`cmd/loadtest/ui.mjs`). Open work at the end is
ordered by how much it helps people.

## Setup

- One server binary, SQLite on local disk, default settings, one user who is organization admin and workspace owner.
- Workspace "Load test" with three collections:
  - **Suppliers** list, 2,000 items (target of a lookup column).
  - **Invoices** list, 10,000 items: Name, Amount (number), Status (choice, 4 values), Due date, Owner (person),
    Supplier (lookup to Suppliers). One item in 20 is tagged `priority`, one in 1,000 `audit-2026`. Views: "All
    items" (default table), "Open invoices" (filter status = Open, sort by due date, total), "By status" (board) and
    "Due calendar" (calendar).
  - **Archive** library, 10,000 documents in 9 top folders (`2000 January`…) with company subfolders, 500 documents
    per subfolder, Document type (choice) and Received (date) columns; the first document of each folder has a small
    PDF.
- Reproduce:

  ```bash
  go run ./cmd/loadtest seed -url http://127.0.0.1:8080 -token TOKEN -items 10000 -state large.json
  go run ./cmd/loadtest measure -url http://127.0.0.1:8080 -token TOKEN -state large.json -out api.json
  node cmd/loadtest/ui.mjs http://127.0.0.1:8080 TOKEN large.json out/   # needs the playwright package
  ```

## What broke, and was fixed

**Writes slowed down with the size of the database.** After every batch of domain events the dispatcher collected
old revisions with `(?='' OR item_id=?)`, which SQLite cannot use an index for, so every pass scanned all revisions:
9.3 s per pass at 8,000 items. Creating one item took 2–8 s, uploads timed out, and the event backlog drained at 13
events/s, so search, workflows and live updates lagged minutes behind.

Fixed by collecting only the items of the batch (`CollectItemRevisions`, a plain `item_id=?` lookup) and adding the
missing indexes on `resources.head_revision_id`, `resources.published_revision_id`, `resources(parent_id, kind_id,
scope_id)` and `item_surfaces.revision_id`. Purge still sweeps everything. After the fix, at 10,000 and at 500,000
items alike: create an item ≈ 20 ms, update ≈ 24 ms, upload ≈ 100 ms, and the backlog stays empty.

## Ingest and storage

- Bulk creation through the API with 8 workers: about 100 items/s at the start, 68 items/s at 500,000 items; 10,000
  items took 98 s, 10,000 documents (with folders) 95 s, on a quieter machine than the later comparison below.
- Ingest is CPU-bound in the one writer: each new item writes a revision, its draft and published surfaces with their
  field values, a publication and two updates of the item row. The profile first credited the audit hook with half
  the time, but that included the writes below it; the hook itself read old values, about 7 %.
- About 16 KB of database per item with six columns, mostly the `field_values` rows of both surfaces and their indexes
  (9.2 KB since lists without publishing keep one surface, item 13 below).

## API at 10,000 items (and 500,000)

Median of several runs, warm cache. "Total" means `include_total`.

| Request | 10,000 | 500,000 |
| --- | ---: | ---: |
| First page of children / query | 7–11 ms | 9–10 ms |
| Keyset paging, per page (100 pages walked) | 8 ms | – |
| Sort by name, number, date, person | 9–10 ms | 29–51 ms |
| Filter status = Open, sort by due date | – | 97 ms |
| Search a common word ("Acme") | 28 ms | 81 ms |
| Search a rare word | 4 ms | 13 ms |
| **First page with total** | **360 ms** | **19.8 s** |
| **Filter + sort + total ("Open invoices")** | **420 ms** | **503 after 30 s** |
| **Group counts (board)** | **401 ms** | **503 after 30 s** |
| Rare filter (amount ≥ 9,990) with total | 457 ms | – |
| Calendar month (due in March 2025) | 168 ms (279 rows) | – |
| Workspace keyword vocabulary | 914 ms | – |
| Item read, revisions, file versions, download | 1–4 ms | – |
| Lookup list first page | 5 ms | – |
| Library: root, subfolder (500 documents) | 3–6 ms | – |
| Library: all documents with total | 364 ms | – |
| Library: filter + sort + total | 430 ms | – |
| Make a column filterable (index build operation) | 4.3 s | – |
| Delete a subfolder with 500 documents / restore it | 177 ms / 653 ms | – |
| Delete a top folder with ~1,100 documents | 695 ms | – |
| Recycle bin first page | 17 ms | – |
| Bulk field update of more than 50 ids | 422 `in requires 1..50 values` | – |

Pages, sorting, filtering and search stay fast at any size: they read a page through an index. Everything that
**counts** grows with the collection, about 36 µs per item, because the count evaluates the permission check and the
surface choice for every matching row. At 500,000 items a plain total takes 20 s and a filtered total or the board's
group counts hit the 30 s request timeout.

## The web UI at 10,000 items

Playwright, Chromium, 1400 × 900. Times include rendering and, where noted, a fixed wait.

| Step | Time | What people see |
| --- | ---: | --- |
| Home, workspace overview | 110–145 ms | |
| Open the list | 223 ms | 50 rows, footer "50+ entries" |
| Load more × 10 | 6.2 s | 550 rows in the DOM; 45 name-lookup `$batch` requests for 10 pages |
| Sort by Amount (descending) | 1.8 s | Correct order; footer "50 of 10001 entries" |
| Type "Acm" in search | 2.1 s (1.5 s wait) | **0 rows** |
| Type "Acme" | 464 ms | 50 rows, "50+ entries" |
| Saved view "Open invoices" | 755 ms | "50 of 2490 entries" |
| Board "By status" | 880 ms | Columns count **13 / 9 / 12 / 16**: the 50 loaded rows, not 10,000 |
| Calendar "Due calendar" | 852 ms | **2 items** in the visible month (the API returns 279 for a month) |
| Select all, Edit a field | 345 ms | "Edit 50 selected items": the loaded rows only |
| Bulk edit 100 selected | 685 ms | **Fails**: "in requires 1..50 values" |
| Open an item | 172 ms | Supplier picker offers **51 of 2,000** suppliers, no search |
| Column picker: show Supplier | 387 ms | Works; names resolve |
| Library root, folder, subfolder | 160–660 ms | Folders first, breadcrumbs, "50+ entries" |
| Move dialog | 264 ms | All 9 top folders, paged |
| File tab: versions | 202 ms | |
| Recycle bin | 1.1 s (1 s wait) | **50 entries, no way to see more** |
| Workspace keywords page | 1.5 s | 50 resources for "priority" (of 500) |
| Global search "Acme" | 3 s (3 s wait) | **Empty** until a workspace is chosen |

No page errors and no server errors.

## Usability

**What works.** Opening, paging, sorting, filtering and searching feel instant at 10,000 items and stay under 100 ms
at 500,000. Folders keep libraries navigable: each level shows a short list, folders first, and the Move dialog pages
folders however large the library is. Item panels, versions, downloads and permissions do not depend on the size of
the collection. Deleting and restoring a folder with hundreds of documents takes well under a second.

**Where people lose track.**

1. **Board and calendar show the loaded page, not the data.** The "By status" board counts 50 items in total and the
   calendar shows 2 invoices due in a month that has 279. People read these as the truth. The API already answers
   both: `query/groups` gives the counts (400 ms) and a month-range filter gives the calendar (170 ms).
2. **"Select all" selects 50, and bulk edit breaks above 50.** After Load more, selecting 100 rows and editing a field
   fails with an API message. There is no way to change "all 2,490 open invoices" at once.
3. **Lookup pickers are a plain select with the first 51 items.** Choosing a supplier from 2,000 is impossible unless
   it happens to be among the first. Searchable pickers are needed for lookups (and people, terms) of any size.
4. **Search needs whole words.** Search as you type shows nothing until a word is complete ("Acm" → 0, "Acme" → 50).
   In a library, searching at the root finds nothing because search only looks at the open folder's direct children.
5. **Totals are missing or slow.** The default view says "50+ entries"; views with a total show "50 of 2490" but that
   count costs 0.4 s at 10,000 and times out at 500,000. People want an idea of size ("about 10,000") quickly and an
   exact number only when it is cheap.
6. **The recycle bin and the keyword page stop at 50.** Neither pages, so after deleting a large folder only the first 50
   entries can be restored one by one, and the keyword page lists 50 of 500 tagged items.
7. **Load more gets slower as the page grows.** Every Load more re-resolves the names of all loaded rows (45 `$batch`
   requests for 10 pages) and the table renders every row (550 rows after 10 loads, 6 s). Scrolling a few hundred rows
   is fine; thousands are not.
8. **Global search starts empty.** `/search?q=Acme` shows nothing until a workspace is picked, even with only one
   workspace.
9. **The keyword vocabulary takes almost a second** at 10,000 items, so keyword suggestions lag while typing.

## Done since the test

1. **Board and calendar from the API.** Board columns count with `query/groups` (Open 2,490, Paid 2,487, Overdue
   2,487, Disputed 2,537: all 10,001 items) and load 20 cards per column with Load more. The calendar asks for the
   visible weeks only (date fields by day, moments widened by the largest time zone offsets), shows "279 items this
   month", folds days with more than four items, and lists undated items separately.
2. **Searchable pickers.** Lookup fields search the looked-up list by name as people type (`$name contains`, now
   case-insensitive in the API, 20 at a time, "More items match" when there are more); "1555" finds Umbrella Software
   1555 among 2,000 suppliers. The current value shows by name wherever it sits. Person fields use the same picker.
3. **Bulk edit on all matching items.** After selecting every loaded row, "Select all 2,490 items" selects everything
   the view (or folder and search) matches, and Edit a field sends that query to `bulk-update`. Selected ids go in
   groups of 50 under one `or`, at most 1,550 per request. Updating 2,490 items took 2 minutes: the operation runs one
   item per transaction (item 12 below).

4. **Search by word start, below the open folder.** Search matches its last word as a prefix (`"acm"*`): typing
   "Acm" finds 625 invoices in 10 ms. A search runs from the open folder through all its subfolders (`ancestor_id`),
   or over the whole list or library at the top; each hit shows the folder it is in, and "Search the whole library"
   widens it. Opening a folder from the results shows that folder.
5. **Cheap totals.** Queries decide permissions once per ACL scope (a collection has few) instead of looking up
   grants for every row. At 10,000 items a total now takes 25 ms (was 360 ms), a filtered total 92 ms (was 420 ms) and
   board counts 96 ms (was 401 ms); that is about 1.2 s, 4.6 s and 4.8 s at 500,000 items, where they timed out
   before. The table no longer waits for a count: the first page shows "50+ entries", then "50 of 10,001 entries"
   from a separate count that stops at 100,000 (`total_limit`, "100,000+" beyond). A search with a total had to be
   kept from probing the surface index once per match for every row: 11 s became 29 ms.

6. **A recycle bin that pages, one entry per deletion.** The recycle bin lists the newest deletions first, 50 at a
   time with Load more, and only what was deleted directly (`top_level=true`): a deleted folder is one entry saying
   how much it took with it ("with 1,010 entries inside") instead of 1,011 entries. A partial index on deleted rows
   keeps it fast however large the collection. The keyword page already paged ("More resources"); the test had only
   counted its first page.
7. **Names read once per id.** Lookup and term names are cached per id and read together for the ids a screen asks
   for at once, so Load more reads only names it has not seen: 10 loads read 445 names in 28 requests, each once
   (before, every load read all names again).
8. **Long tables render the rows near the screen.** Above 200 rows the table renders about 40–60 rows around the
   visible part (TanStack Virtual, against the page scroll); 550 loaded rows keep 56 in the page instead of 550.
   Arrow keys, Home and End scroll to rows that are not rendered yet, and rows carry `aria-rowindex` for screen
   readers. Ten Load more clicks took 4.5 s, 130–470 ms each (6.2 s before).

9. **Search starts where people are.** The search page searches as you type in the workspace last opened (or the only
   one), offers "All workspaces" (one request each, five hits per workspace with a link to the rest), says what and
   where each hit is ("Item in Archive"), and pages with More results.
10. **Keyword list in 35 ms (was 914 ms).** The vocabulary query starts from the keyword rows instead of walking every resource
    in the workspace (a forced join order and an unindexed workspace condition).
11. **Faster ingest.** A bulk request reads the collection's write permission, field definitions, schema and row once
    instead of per item; the audit hook skips reading old values of rows created in the same transaction; a folder's
    depth stops at its collection. `BenchmarkBulkCreate` (six indexed columns): 92 → 140 items/s with half the
    allocations. Through HTTP with the event dispatcher running, seeding 10,000 items took 91 s instead of 122 s and
    10,000 documents in folders 114 s instead of 124 s (same machine, back to back).
12. **Faster bulk updates.** A bulk update edits up to 200 items per transaction within a quarter of a second, each
    under its own savepoint: updating 2,490 invoices took 32 s instead of 121 s.

13. **One surface per item without publishing.** Lists and libraries without publishing publish every write, so their
    draft and published content were always the same, stored and indexed twice. They now keep only the published
    surface, which every read of head content uses; turning publishing on copies it to a head surface, turning it off
    publishes the heads and drops the head surfaces. At 10,000 items: 9.2 KB per item instead of 15.4 KB (−40 %),
    seeding the list in 69 s instead of 91 s and the library in 97 s instead of 114 s, `BenchmarkBulkCreate`
    140 → about 170 items/s.
14. **Board counts in 5 ms at any size.** Triggers keep a count per collection, surface and single-choice value
    (`group_counts`). Board columns of a whole collection read it when the person reads every item the same way (no
    item with permissions that hide it from them); a filtered board, or a person who cannot see some items, still
    counts rows. At 10,000 items 5 ms instead of 96 ms; at 500,000 the same 5 ms instead of about 5 s.
15. **Live updates no longer slow bulk work.** An open list refreshed everything for each changed item; during a bulk
    edit that is about 100 refreshes a second. It now refreshes at most once a second: editing 2,502 items with the list
    open took 47 s instead of 116 s (25 s with no page open).

## Open work

Nothing structural remains from this test. Smaller things worth doing:

- Imports into folders still check the folder's permission for every item (a deleted folder must stop the batch).
- Turning publishing on for a very large list copies every surface in one transaction; at 500,000 items that would
  hold the writer for minutes and should become a background operation like index builds.
