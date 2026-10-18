# CI and development test plan

Review date: 2026-10-16. Source review includes commit `540d61d` and its native CI
fixes. GitHub timing evidence below is from `a025e97`, before those fixes. This
document tracks the optimization work.

Implementation started in `perf/ci-dev-cycle` from `fc163f6`. The first change
adds cached/scoped local checks with shared Bash/PowerShell behavior, validates
frontend source/output hashes before reuse, shares one embedded UI artifact
across verification Go jobs, captures race timing/event artifacts (including
discovery failures), and cancels superseded branch/PR verification. Existing CI
test coverage and the three race shards remain in place. Test matrix reduction, fixture optimization,
cache/shard redesign, specialized trigger routing and upgrade-feature removal
are still planned.

First-change validation: 27 script tests and 26 sample tests passed; frontend
typecheck/lint and all 123 tests passed. The selected pagination test check took
55.24s on its first run (including frontend build and compilation), then **0.66s**
on the unchanged repeat with the test result cached. Embedded-web and HTTP tests
passed in a 22.43s scoped check. A temporary Go module verified race execution of
tests, subtests, fuzz seeds and examples with timing artifacts. Actionlint checked
both modified workflows. These are local measurements; the GitHub matrix,
PowerShell execution and the complete `--full` check have not been run for this
change.

## Recommendation

Make the normal development check incremental and cached. Run the ordinary Go
suite and a small concurrency suite on pull requests. Run the complete race suite
on main, with exhaustive combinations, native acceptance, platform packaging,
soaks and active fuzzing in dedicated lanes. Run specialized lanes on relevant
pull requests as well as on a schedule and before release.

Keep the three race shards initially. Fix the test costs and duplicated setup
before deciding whether two shards, or one, are sufficient. Increasing shard
count or timeouts would conceal the main problems.

## Evidence

The [latest completed verification run examined](https://github.com/eduardpaul/PaperGo/actions/runs/38870345484)
took about 10 minutes elapsed and consumed **58.6 aggregate job minutes** across
12 jobs. Aggregate minutes are summed job durations, not billed minutes or wall
time. The separate dependency scan adds another job.

| Job | Total duration | Significant work |
| --- | ---: | --- |
| Race shard 1 | 8:22 | 7:36 in the race step; DMS 253s, runner 316s |
| Race shard 2 | 10:07 | 9:25 in the race step; DMS 460s, runner 279s |
| Race shard 3 | 5:05 | 4:31 in the race step; DMS 144s, runner 160s |
| Development container | 5:55 | 5:49 building and exercising the environment |
| Verification | 4:19 | Frontend build 19s, frontend checks 34s, Ent 28s, vet 70s, application build 70s |
| Staticcheck | 2:44 | Frontend build 19s, tool installation 24s, analysis 92s |
| Docker build and startup | 3:16 | 2:57 building and testing the image |

The race steps spend **75–102 seconds before test selection is printed**. Each
shard first invokes `go test -race -list ... ./...`, then invokes `go test` again
against every package. Listing includes build/link/discovery overhead; it is not
test execution. Go, npm and native caches all reported hits in these jobs.

The name-based split produces 189/189/188 names but very different runtimes.
The largest individual tests in those logs are:

| Test | Race duration | Finding |
| --- | ---: | --- |
| `TestSeekPagesMatchSortedEligibleRows` | 229.24s | 2 actors × 3 surfaces × 4 queries × 7 sorts × 2 directions = 672 combinations, each comparing pagination with a reference query |
| `TestTaxonomyReviewSampleGuards` | 77.86s | Six scenarios each launch a complete runner fixture |
| `TestDecisionDurableCrashRecovery` | 42.61s | Three genuine subprocess crash/recovery scenarios |
| Native `TestConformance` | 33.97s | Shared search contract with fresh stores per scenario |
| `TestBrowseCandidateAndScanPathsAgree` | 27.25s | Another query-path comparison matrix |

A bounded local measurement used the existing SDKs and built frontend, Go
1.26.8, Linux with four logical CPUs, and uncached ordinary tests for four
packages. All four passed: DMS **47.90s**, HTTP API **15.48s**, runner **57.20s**,
zvec **88.03s**; combined wall time **106.37s**, including compilation and package
scheduling. The pagination matrix alone took **12.52s**. These are a local
baseline, not a prediction for GitHub's Go 1.27.2 runners.

Frontend: **33 files / 123 tests passed**, 33.40s Vitest time, 36.50s command wall
time with two workers. Vitest attributed about a third to environment setup and
reported 33 isolated worker startups. This warrants a small configuration
experiment, but is secondary to the backend race costs.

Source inventory: 202 Go test files, 568 top-level Test/Fuzz/Example declarations,
**zero `testing.Short()` checks and zero `t.Parallel()` calls**. Packages already
run concurrently through Go; tests within each package run serially.

The latest run failed on missing embedded frontend assets in the zvec job,
Windows compiler setup, and packaged macOS backup. It did **not** time out.
The current native CI fixes address those areas and must be verified separately;
they do not establish that suite runtime has improved. The
[preceding run](https://github.com/eduardpaul/PaperGo/actions/runs/38726465748)
consumed 64.6 job minutes and took about 12 minutes, including a failed race shard.

## What to remove, reduce, or retain

| Work | Decision | Replacement / coverage retained |
| --- | --- | --- |
| `npm ci` on every local check | Remove from the edit/check loop | Install in setup and when the lockfile changes; fail clearly if setup is stale. Continue using `npm ci` on clean CI workers. |
| Local `go test -count=1 ./...` as the default | Replace | Cached selected-package tests by default; explicit full and race commands retain forced execution. `-count=1` disables test-result caching, not compilation caching. |
| Full frontend build on backend-only edits | Avoid when inputs are unchanged | Reuse a verified current `web/dist`; still build real assets after frontend/build input changes and for integration/packaging checks. |
| Independent frontend builds in nine current verification jobs, plus vulnerability scanning | Consolidate | Build and typecheck once per workflow revision, upload `web/dist`, download only in consumers that embed it. Keep Docker's own build as a packaging check. Measure artifact dependency overhead. |
| Entire Go suite under race detection on every PR | Remove from the ordinary PR lane | Ordinary correctness suite plus explicit concurrency tests; complete race execution on main and nightly, and on relevant PRs. |
| Duplicated amd64 native/backup/admin race execution | Remove duplicate ownership | Dedicated native lane owns zvec tests; common suite owns ordinary backup/admin coverage. Keep native-backend backup/copy coverage where it differs. |
| Full service/API/runner parity matrix on both Linux architectures | Reduce | Full backend contract on amd64; arm64 native conformance and one indexing/query/backup smoke. Broader arm64 coverage nightly and before release. |
| 2,000 × 768 zvec soak and timed fuzz campaigns on every PR | Move | Nightly/manual and relevant native changes; retain deterministic fuzz seeds on every PR. |
| Devcontainer rebuild on every app change | Make conditional | Trigger on devcontainer/setup/toolchain/native inputs and scheduled runs. Retain its non-root, permissions, debugger and proxy assertions. |
| Docker rebuild on unrelated edits | Make conditional | Trigger on application, frontend, native, schema and packaging inputs; skip prose-only and unrelated sample changes. Add persistent BuildKit cache. |
| Previous-release schema upgrade planning and preservation tests | Delete with their feature code | Follow `AGENTS.md`: one current schema, fresh initialization, no upgrade paths. Remove `internal/releaseplan`, upgrade-specific portions of `internal/database/release*`, their callers and bundle-history machinery/docs in one coherent change. Retain fresh startup, schema validation, version/checksum and package tests. This is a policy correction, not the largest speed saving. |
| Similar business cases at service, HTTP and runner layers | Review assertion by assertion | Keep exhaustive domain rules at their owning layer; HTTP tests own parsing/status/auth/ETags, runner tests own dispatch/durability. Remove only assertions proven redundant after mapping them. |

Do not delete ACL isolation, exact numeric transport, transactional rollback,
crash recovery, backup/restore, native ABI, actual OCR recognition or search-store
conformance to meet a time target. Backup/restore and current content import/export
are current app features; they are not database upgrade compatibility tests.
The existing `Benchmark*` tests are not run by the current normal test command,
so deleting benchmarks would not speed it up.

## Proposed commands and CI lanes

Replace the current all-in-one default in `scripts/check.sh`, keep
`scripts/check.ps1` equivalent, and update the default VS Code task and
`docs/development.md` together. Proposed interface:

| Command | Purpose |
| --- | --- |
| `check --packages ./internal/dms` | Cached ordinary tests for explicitly selected packages and relevant inexpensive checks; no dependency reinstall |
| `check --web` | Frontend lint/typecheck and selected tests; watch mode available for repeated edits |
| `check --full` | Entire ordinary suite, Python checks, formatting, vet, Staticcheck, generation/checksums and real frontend/application build |
| `check --race` | Complete race lane, deliberately uncached |
| `check --native` | Explicit real-model/native acceptance; missing required assets fail instead of silently skipping |

The default can select changed packages only after reverse-dependency handling
is implemented. Until then, require an explicit scope or run the ordinary suite;
do not quietly test just the edited directory. Shared `internal/model`, Ent,
schema and module/native changes require broader validation. An unchanged
package whose dependency changed must still run.

| Trigger | Required work |
| --- | --- |
| Ordinary PR | Frontend lint/tests/typecheck/build as applicable; Python scripts; formatting; vet and Staticcheck; full ordinary Go correctness suite with the bounded query matrix; small explicit concurrency suite; amd64 native contract where relevant |
| Relevant specialized PR | Add Docker, devcontainer, native-model acceptance, platform packaging, or broad race coverage based on the affected inputs; workflow/shared dependency changes broaden selection |
| Push to main | Ordinary checks plus complete Go race suite; relevant packaging/native lanes |
| Nightly/manual | Full query matrix, all platform/native lanes, real-model acceptance, soak, timed fuzzing and full race suite if not already validated for the same revision/configuration |
| Release | Full validation for the exact tagged revision plus packaging/startup on every supported target; scheduled success on another commit is insufficient |

Use a small always-present aggregate required check to validate selected jobs and
report success when non-app work legitimately skips app jobs. Do not use top-level
path filters that leave required checks pending. Add branch/PR concurrency with
`cancel-in-progress: true` for superseded verification and dependency runs; keep
release publication serialized without cancellation.

Run reachable Go vulnerability checks on dependency/toolchain changes and the
weekly schedule, with exact-revision validation before release. App-only changes
can make existing vulnerable code reachable, so retain a lightweight dependency
inventory check or run the reachable scan on main as well. Keep security scans
visible and independently reported.

## Implementation order

### 1. Establish timing and fix the local loop

- Emit `go test -json` and a compact per-package/per-test duration report in CI;
  upload it even on failure. Separate preparation, compilation/discovery and
  execution in the report. Record cache keys/hits, toolchain and native manifest.
- Add explicit scoped checks, remove routine installs and forced reruns, and
  avoid Ent generation unless its inputs changed locally. Keep generation drift
  validation in CI. A full check remains explicitly available.
- Reuse assets based on their inputs, not just the existence of `dist/index.html`.
  Preserve the real embed contract; do not introduce placeholder frontend assets.
- Add cancellation for superseded CI runs.

Acceptance: a second unchanged scoped check reuses successful results; schema,
shared package and frontend edits select the necessary checks. Target warm local
feedback under 60s, with small backend changes under 30s. Measure cold setup
separately.

### 2. Reduce the largest tests

- Turn `TestSeekPagesMatchSortedEligibleRows` into named cases. Keep every sort,
  both directions, nulls/ties, actor visibility, surfaces and cursor boundaries in
  a bounded deterministic PR set. Run the complete 672-case product in the
  exhaustive lane. Retain explicit security-sensitive combinations even when
  reducing the product; do not replace it with random sampling.
- Apply the same approach to `TestBrowseCandidateAndScanPathsAgree`. Do not
  remove the independent reference query; it catches incorrect optimized SQL.
- Keep all six taxonomy guard scenarios, but test their domain guard logic at
  its owning layer. Use fewer full runner launches only where lower-layer
  assertions cover the same behavior. Keep representative end-to-end approval
  and rejection plus actual process-crash cases.
- Replace fixed waits with observable barriers or controllable clocks where the
  behavior permits it. `TestConcurrencySkipAndReplace` currently takes 10.70s
  locally, including deliberate five-second workflow delays. It should block
  until the second trigger is delivered, then release; shortening sleeps alone
  would restore timing races. Keep real durable-delay/restart coverage elsewhere.

Acceptance: bounded pagination coverage targets under 20s under race detection on
the same CI runner, exhaustive behavior still runs in its dedicated lane, and
guard/recovery assertions remain mapped to an executing test.

### 3. Optimize database fixtures, then add safe parallelism

`internal/testutil/database.go` repeatedly validates migration files, applies the
whole schema, initializes identity, creates 20 users, creates a role and assigns
it. `database.Open` also uses WAL, `synchronous(FULL)`, `wal_autocheckpoint=256` and `journal_size_limit=1048576`. Runner fixtures launch
the dispatcher and seed several fields even when the scenario needs less.

- Profile fixture setup separately before changing it. Cache immutable schema
  bytes/validation within the test process and use smaller scenario fixtures.
- If setup dominates, create one current-schema seeded template per test process
  and clone it to an independent path for each test. Use SQLite's backup/VACUUM
  facilities or a fully checkpointed, closed database; never copy a live main
  file without its WAL. Do not share mutable databases between tests.
- Retain dedicated fresh-schema and identity-initialization tests. Keep durability
  and crash tests on production database settings. Any relaxed fixture pragmas
  must be isolated and measured; do not change production defaults for test speed.
- Move `surfaceBatch`, `operationBatch` and `candidateDriveLimit` test overrides
  from package globals into per-service configuration before parallelizing DMS
  tests. Audit native ONNX/PDFium globals, environment mutation and runner
  lifecycle as well. Start with isolated pure/domain tests and bounded parallelism.

Acceptance: unchanged assertions and isolation; no race findings or flakes in
repeat runs of the affected concurrency scenarios. Publish before/after fixture
costs. Do not add `t.Parallel()` across the suite indiscriminately.

### 4. Remove repeated CI preparation and rebalance race jobs

- Build frontend assets once, then distribute them to embedding consumers.
  Keep frontend lint/tests independent of native SDK installation where possible.
- Split immutable module downloads from build caches. Give ordinary and race
  jobs distinct build-cache families, including OS/architecture, Go version,
  native fingerprint and build mode, with revision keys and compatible restore
  prefixes. Nominate a writer per family. The current identical setup-go keys
  across jobs can retain an incomplete cache indefinitely; that explanation is a
  hypothesis to verify, not proof from a cache-hit message.
- Cache pinned Staticcheck/govulncheck executables by platform and tool version.
  Add persistent Docker/devcontainer build caches; in-step cache mounts alone
  do not provide cross-run persistence on disposable workers.
- Replace alphabetical round-robin sharding with duration-aware package/test
  assignments. Give small packages one owner and split only DMS/runner if needed.
  Discover from a shared inventory rather than rebuilding every package merely
  to list it in every shard. Include fuzz seeds and examples exactly once.
- Audit redundant vet execution: `go test` runs a vet subset. When an explicit
  full `go vet` check is required and has passed, test lanes can use `-vet=off`.
- After measurements, reduce shard count only if the slowest full race job fits
  the budget. Compare wall time and aggregate runner minutes together.

Acceptance: max/min shard execution ratio below 1.25 on comparable successful
runs, no missing tests, and fewer aggregate runner minutes. Cache optimization
must improve warm builds without concealing native dependency changes.

### 5. Route expensive lanes and complete cleanup

- Apply the trigger matrix above, consolidate duplicated native coverage, and
  remove the upgrade feature/tests/contracts/docs coherently.
- The Laya native decision tests currently skip without
  `DECISION_MODELS_PATH`; the workflow does not provision it or set
  `PAPERGO_REQUIRE_DECISION`. Add a dedicated real-model lane that requires
  assets and reports actual execution. A faster green run must not hide this gap.
- Trial frontend worker reuse or another lighter pool on a branch, with cleanup
  and module/global isolation verified. Existing tests mock modules and globals,
  so disabling isolation globally is not an assumed safe improvement. Keep the
  experiment only if repeated runs improve runtime and preserve independence.
- Update both local scripts, editor tasks and development docs; delete replaced
  shard tooling/tests when its replacement is complete. No compatibility modes,
  extra migrations, backfills or upgrade tests.

Acceptance: warm ordinary PR feedback targets **five minutes or less** and local
scoped feedback **one minute or less**. Compare at least five representative runs,
including cold-cache and relevant native changes. Track skipped acceptance tests,
flakes, p50/p95 wall time and aggregate job minutes. Time targets are goals, not
speedups established by this review.

## Execution boundaries and references

Only the four-package ordinary Go measurement and frontend tests were run for
this review. The entire local check, full race suite, Docker and platform matrix
were not rerun. GitHub failures and timing claims refer to the linked completed
runs, not to unverified behavior after `540d61d`.

Implementation references: [Go test caching and flags](https://pkg.go.dev/cmd/go#hdr-Test_packages),
[GitHub cache immutability](https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching).
Go's documentation confirms that `-count=1` bypasses test-result caching and
`-parallel` applies to tests that call `t.Parallel`; increasing that flag alone
will not parallelize the existing test functions. GitHub's cache contents cannot
be updated under an existing key, hence the proposed build-cache rotation.
