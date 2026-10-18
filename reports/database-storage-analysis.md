# PaperGo sample database storage analysis

Analyzed 2026-10-18: `papergo_sample.db`. The sample was opened read-only;
experiments ran on temporary copies. Its SHA-256 remained unchanged. No
application, schema, configuration or sample-data changes were made.

## Conclusion

The sample is **3,784,704 bytes (3.61 MiB)**. Most of the size is explainable:
the empty schema costs 1.43 MiB, the database contains 57 calendar items in
addition to three file-backed items, and workflow execution creates several
records for each change. There are also concrete opportunities to reduce
storage and one unbounded retention path to fix.

This is not evidence of a 2 MiB cost per uploaded file. A single snapshot cannot
establish a long-term growth rate.

## What is actually in the sample

| Measurement | Result |
| --- | ---: |
| Main database | 3,784,704 bytes / 924 pages |
| Page size | 4,096 bytes |
| Empty recreation of the sample's complete schema | 1,503,232 bytes / 367 pages |
| Tables / indexes / triggers | 78 / 256 / 120 |
| Items | 60 |
| Calendar items (`ical_https`) | 57 |
| File records (`blobs`) / distinct associated items | 3 / 3 |
| PaperGo workflow runs | 104 |
| DBOS workflow executions, including maintenance | 164 |
| Transactional step results / DBOS operation results | 419 / 428 |
| Domain events / audit events / live notifications | 395 / 141 / 523 |
| Publication snapshots | 90 |
| User-visible item history entries | 0 |
| Free pages available for truncation | 0 |
| `PRAGMA quick_check` | `ok` |

The live notification timestamps cover about 55 minutes: 2026-10-17 23:20 to
2026-10-18 00:15 in Europe/Madrid. This describes the recorded activity window,
not continuous observation of a running server.

The three file records declare 1,050,204 bytes of original file content, stored
outside SQLite. Two have the same SHA-256 and size (511,170 bytes each).
Physical blob files were not supplied, so their actual allocation was not
measured. Text and preview metadata each have two rows; the reusable result
cache is empty. Neither cached AI answers nor user-visible version history
explains this database's size.

## Where the database space goes

Values include each table's indexes. Rows below are distinct categories, but
do not cover every table.

| Table | Allocated KiB | Rows |
| --- | ---: | ---: |
| `runner_step_results` | 496 | 419 |
| `operation_outputs` | 356 | 428 |
| `domain_events` | 244 | 395 |
| `resources` | 184 | 66 |
| `workflow_input` | 168 | 164 |
| `field_values` | 160 | 177 |
| `workflow_runs` | 144 | 104 |
| `item_surfaces` | 140 | 60 |
| `audit_events` | 132 | 141 |
| `workflow_status` | 128 | 164 |
| `live_changes` | 92 | 523 |
| `publications` | 80 | 90 |

The six workflow execution tables (`runner_step_results`, `operation_outputs`,
`workflow_input`, `workflow_output`, `workflow_status`, `workflow_runs`)
together occupy **1,336 KiB, or 36.1% of the database**.

Across the database, indexes occupy 1,568 KiB (42.4%). Indexes are a substantial
fixed and variable cost, but this sample does not establish that any particular
query index can safely be removed.

### 1. Step-result table layout wastes overflow-page space

`runner_step_results` uses `WITHOUT ROWID` with a composite key and a variable
JSON output. It contains 136,537 bytes of actual output. Including keys and
record encoding, SQLite reports 173,379 payload bytes occupying 507,904 bytes.
There are **95 overflow pages** and 331,944 unused bytes within its pages.

The `load` step accounts for 95 outputs above 1,000 bytes. This table layout
places those comparatively small records onto overflow pages, leaving much
of each 4 KiB page unused. SQLite's [WITHOUT ROWID guidance](https://www.sqlite.org/withoutrowid.html)
specifically recommends ordinary rowid tables for larger rows.

Experiments with identical records:

| Experiment | Result |
| --- | ---: |
| Fresh `WITHOUT ROWID` table | 503,808 bytes |
| Fresh ordinary rowid table, including its composite primary-key index | 262,144 bytes |
| Full sample copy with ordinary rowid step table, then `VACUUM` | 3,489,792 bytes |
| Whole-database reduction in that copy | **294,912 bytes / 288 KiB / 7.8%** |

This is a measured storage improvement, not an application correctness or
performance test. A product change should preserve the unique key and validate
exactly-once replay and concurrent writes. Edit the one schema file in place;
do not introduce an additional migration or an upgrade path.

### 2. Each transactional output is persisted twice

All **419** `runner_step_results` rows have a matching DBOS
`operation_outputs` row. Base64-decoding the latter reproduces the former's
output exactly:

- Raw outputs: 136,537 bytes.
- Encoded duplicates: 182,372 bytes, approximately 34% larger.

The transactional marker is necessary: domain writes and the marker commit
together, while DBOS records its step output separately. Removing the marker
without redesign would compromise exactly-once writes after a crash.

A focused design improvement is to store the large result once in the
transactional marker and give DBOS a small stable reference. Recovery must
resolve that reference, and retention must keep both records consistent.

There is a second source of repetition: 104 `load` step outputs hold only five
distinct payloads. They repeat 121,404 bytes of output whose distinct values
total 3,696 bytes. `internal/workflow/run.go` includes the full workflow
definition in every loaded result. Consider resolving a retained immutable
workflow-version reference instead, preserving the run's exact definition and
author semantics. Savings from these proposals overlap and should not be added
together without another measurement.

### 3. Maintenance workflow executions have no retention path

The sample contains:

- 55 successful `papergo.tick` executions.
- Four successful `papergo.search_embed` executions.
- One successful `papergo.system-retention` execution.

`internal/runner/runner.go` schedules a tick every minute, embeddings every
15 minutes, system retention hourly and general retention daily. This produces
approximately **1,561 maintenance executions per continuously running day**,
even without uploads.

`Runner.Purge` filters DBOS workflows to `papergo.run` only.
`PurgeSystemRuns` also selects application runs, through PaperGo's
`workflow_runs` table. Neither removes these maintenance executions. The sample
confirms their accumulation; the code establishes the missing cleanup path.
`RUN_RETENTION` alone does not bound them.

Add bounded cleanup of terminal executions for these explicit maintenance
workflow names, including their DBOS payloads. Keep pending/running executions
and allow enough retention for scheduler deduplication and diagnostics. Avoid
a broad DBOS purge that could bypass PaperGo's application-run cleanup.

### 4. Calendar changes explain much of the workflow fan-out

The sample contains 60 `item.created` events and 30 `item.updated` events.
Search indexing accounts for **91 of the 104 application runs**. Each search
run records multiple steps and emits both workflow and role completion events.
Those completion event types account for 182 domain-event rows.

Calendar items account for **84 of the 90 publication snapshots**. The sample
contains no retained item history, but publication snapshots and audit events
have separate retention rules. The schema prevents deleting audit events and
prevents deleting publication snapshots for live items.

History settings therefore cannot bound all historical records. Any future
audit/publication retention policy must explicitly change those product rules
and guards. For calendar sync, preserve the existing unchanged-content check
and test that an unchanged refresh creates no new revisions, publications or
search runs. This snapshot alone does not prove that check is malfunctioning.

## Why `VACUUM` and WAL size need different treatment

The sample has `auto_vacuum=NONE`, **zero freelist pages**, and about 1.79 MiB
of spare bytes inside allocated pages. Spare bytes inside pages are different
from free pages at the database level. Many empty table/index roots are required
by the schema, and the step table has sparse overflow pages.

Running ordinary `VACUUM` on a full copy left the file at **3,784,704 bytes**:
**zero net savings**. It is not the first remedy for this sample.

No `papergo_sample.db-wal` was supplied, so the reported live WAL was not
inspected. A WAL near 4 MB is consistent with the default automatic checkpoint
threshold: 1,000 frames at 4,120 bytes plus the header is 4,120,032 bytes.
Checkpoints normally reuse that allocation. See [SQLite WAL behavior](https://www.sqlite.org/wal.html).

For a smaller retained journal, a reasonable starting point to benchmark is
`wal_autocheckpoint=256` and `journal_size_limit=1048576` on **every writer
connection**, through the shared DSN used by the API, runner and SQLite search
store. These are proposed connection settings, not existing PaperGo environment
variables. More frequent checkpoints add I/O. The
[journal size limit](https://www.sqlite.org/pragma.html#pragma_journal_size_limit)
controls retained size after reset; it is not a hard ceiling during large
transactions or blocked checkpoints. Keep WAL and FULL durability.

`PRAGMA wal_checkpoint(TRUNCATE)` during a quiet maintenance period can reclaim
the journal immediately, subject to active-reader/writer locks. It does not
shrink the main database or fix accumulating records. Do not delete the WAL by
hand.

## Recommended order of work

1. **Bound maintenance executions.** Fix the clear unbounded growth path and
   verify that old terminal maintenance runs disappear while active work stays.
2. **Change the step-result table layout.** The sample demonstrates a 288 KiB
   whole-database improvement on a copy. Validate replay before shipping it.
3. **Reduce duplicated durable payloads.** Use small immutable references for
   DBOS step results and repeated workflow definitions, retaining crash safety.
4. **Tune retention and cadence.** `RUN_RETENTION` defaults to 720h; successful
   system built-ins are retained for up to 24h and cleaned hourly. A shorter
   `RUN_RETENTION`, such as 168h, reduces finished-run and dispatched-event
   retention, but also changes reusable answer-cache lifetime. General cleanup
   is daily, so a short TTL is not immediate cleanup. Consider separate shorter
   retention for successful high-volume system work while keeping failures
   longer for investigation. Active/waiting runs must remain protected.
5. **Control retained WAL allocation if needed.** Benchmark the connection
   settings above and monitor incomplete checkpoints and long read transactions.
6. **Measure sustained growth.** Track main/WAL/search/blob bytes separately,
   freelist pages, per-table allocation, runs by name/status, and expired rows.
   Expect bounded operational tables to plateau after their retention windows;
   content, audit and publication records still grow with retained activity.

The sample alone supports the measured layout savings. The long-term gains
from retention and deduplication need an idle soak test and representative
calendar/import/upload workloads rather than extrapolation from two uploads.
