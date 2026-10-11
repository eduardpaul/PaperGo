# Missing, partial and drifted features by priority

Current code/API analysis: 2026-10-10; PaperGo includes inherited preferences, folder metadata, HTTP batches, administrative backup/restore and aligned concurrency/pagination; PaperDotNet `96df412`. 66 open comparisons: 45 missing, 12 partial, 9 drifted. Covered stories and accepted recursive deletion are excluded.

[Full per-module comparison and source evidence](C:/Users/eduapaul/Documents/PaperGo/reports/code-api-parity.md). Priorities are proposed implementation ordering; local sign-in and multi-tenancy depend on the intended product boundary.

## P1 (13)

- **P1 · ID-01 · Missing:** A user needs an external identity service for the production sign-in story.
- **P1 · ID-04 · Partial:** User profiles and account anonymization are implemented; password management remains with the external identity provider.
- **P1 · LST-32 · Missing:** An everyday user's initial landing/storage locations are missing.
- **P1 · DOC-07 · Missing:** Scanned documents cannot become searchable text through PaperGo.
- **P1 · DOC-08 · Missing:** Preview and processing retry/status stories are missing.
- **P1 · DOC-10 · Missing:** Shared intake and personal triage cannot be reproduced through the product API.
- **P1 · SRC-02 · Missing:** Words occurring only inside PDF/image contents cannot be found.
- **P1 · WF-06 · Missing:** A launch form does not cover collecting a human decision during an active process.
- **P1 · WF-10 · Drift:** The same automation can succeed/fail under different permissions. StartedBy is not proof that .NET actions run with caller ACLs.
- **P1 · COL-01 · Missing:** A team cannot discuss an item or notify a mentioned colleague.
- **P1 · NTF-01 · Missing:** Approval requests, mentions and reminders cannot reach a personal in-app inbox.
- **P1 · TSK-01 · Missing:** No task template, task-specific validation/assignment lifecycle or task views are registered. Person fields alone do not implement task behavior.
- **P1 · TSK-04 · Missing:** Cannot port a personal task dashboard or document follow-up.

## P2 (43)

- **P2 · TEN-01 · Drift:** Multi-organization hosting requires an explicit scope decision; workspace ACLs alone do not provide tenant isolation.
- **P2 · TEN-02 · Missing:** Separate organizations cannot be provisioned and resolved by host in one service.
- **P2 · ID-02 · Missing:** Token consumption covers a different role from acting as the application's authorization server.
- **P2 · ID-07 · Missing:** Third-party OAuth client onboarding is unavailable.
- **P2 · LST-29 · Drift:** Rights and duplicate-add behavior align. Go requires a delete ETag while .NET removes by edge ID without one. Relationship events are tracked as LST-44.
- **P2 · DOC-03 · Drift:** Spoofed extensions/media types and arbitrary binary files have different acceptance behavior.
- **P2 · DOC-09 · Missing:** Users must edit documents outside the app; server page manipulation is unavailable.
- **P2 · DOC-11 · Partial:** Per-file language hints exist; library OCR defaults/inheritance and multilingual processing are absent.
- **P2 · DOC-12 · Missing:** A human review of a generated replacement cannot be implemented with the same lifecycle.
- **P2 · SRC-01 · Partial:** Metadata search exists; one organization-wide search response across all source types does not.
- **P2 · SRC-03 · Drift:** A query such as invoice OR receipt means different things and result order is not relevance.
- **P2 · SRC-04 · Missing:** Metadata grouping does not replace ranked document/page search navigation.
- **P2 · SRC-06 · Missing:** Administrators cannot run the same repair/rebuild workflow.
- **P2 · JOB-02 · Partial:** No general operation-handler registry or equivalent document/search/provisioning operation families and expiry cleanup.
- **P2 · JOB-03 · Missing:** Clients must poll and cannot receive equivalent live updates.
- **P2 · JOB-04 · Partial:** Custom synchronous cancellation/transformation and general subscriber extension stories are absent.
- **P2 · WF-03 · Partial:** Cron is available; date-per-item deduplication, module trigger families and webhook ingress are missing.
- **P2 · WF-04 · Missing:** Cannot express 'only when status changes from X to Y' with the same trigger contract.
- **P2 · WF-05 · Partial:** Timed waiting exists; approval/payment/external-job callbacks cannot resume equivalent waits.
- **P2 · WF-07 · Missing:** Cannot declaratively create receipt lines for every extracted line with the same semantics.
- **P2 · WF-08 · Missing:** Computed mappings and script-based multi-item business logic are absent.
- **P2 · WF-09 · Missing:** A duplicate trigger cannot be configured to skip or replace an existing business process.
- **P2 · WF-11 · Partial:** Generic item writes exist, but common ready-made business actions are missing.
- **P2 · WF-12 · Partial:** The configuration mechanism is covered; almost all built-in process content is absent.
- **P2 · AI-01 · Missing:** AI-assisted classification/extraction stories are absent; this module has no direct REST routes but is implemented.
- **P2 · COL-02 · Missing:** No user-facing timeline combining changes and discussion.
- **P2 · NTF-03 · Missing:** Internal workflow events do not cover user follow/digest stories.
- **P2 · NTF-04 · Missing:** External integrations cannot subscribe to changes.
- **P2 · NTF-05 · Missing:** Notification steps cannot be ported.
- **P2 · TSK-02 · Missing:** Checklist behavior needs a dedicated implementation.
- **P2 · TSK-03 · Missing:** Generic edges cannot enforce the task graph story.
- **P2 · TSK-05 · Missing:** A schedule trigger alone does not reproduce a repeating task lifecycle.
- **P2 · CAL-01 · Missing:** Cannot reproduce agenda/calendar stories with raw date filtering.
- **P2 · CAL-02 · Missing:** Recurring calendar edits are absent.
- **P2 · CAL-03 · Missing:** Calendar client exchange and private subscriptions are absent.
- **P2 · CAL-05 · Missing:** Upcoming-event alerts are absent.
- **P2 · NTE-01 · Missing:** No built-in note template or automatic hashtag-to-keyword behavior.
- **P2 · NTE-02 · Missing:** Linked knowledge-base navigation does not port.
- **P2 · PRV-01 · Partial:** No complete application package for workspaces, identity, workflows and contributed module settings, with parameterized dry-run/reapplication.
- **P2 · PRV-02 · Missing:** Cannot transfer a complete set of items, file content and module state.
- **P2 · MCP-01 · Missing:** Assistant/tool clients cannot use the same integration story.
- **P2 · AUD-01 · Partial:** No unified organization log with entity/actor/time filters, changed-property/trace fields and newest-first merged paging.
- **P2 · LST-48 · Drift:** A 256-character item title accepted upstream fails in Go. Title limits remain different even though custom text-field limits align.

## P3 (10)

- **P3 · ID-08 · Missing:** Existing proxy SSO cannot authenticate by trusted user/group headers.
- **P3 · SRC-05 · Missing:** Meaning-based retrieval is missing; .NET support is configuration-dependent.
- **P3 · AI-02 · Missing:** Cannot port batch jobs or cost-control behavior.
- **P3 · NTF-02 · Missing:** Cannot control when and how alerts arrive.
- **P3 · CAL-04 · Missing:** Cannot keep external calendars synchronized.
- **P3 · EXT-01 · Missing:** Administrators cannot activate/configure packaged extensions.
- **P3 · EXT-02 · Partial:** No equivalent contributed field/API/activity/job/trigger/MCP SDK and registration lifecycle. Template contribution covers only a subset.
- **P3 · LST-45 · Drift:** A valid 101-person/choice/lookup/term array is rejected; even 101 identical inputs exceed the Go cap. Type categories themselves align (LST-40).
- **P3 · LST-47 · Drift:** The first page shows oldest entries and numbering/current markers differ after off-mode writes.
- **P3 · LST-49 · Drift:** Deep hierarchies cannot be represented at the same depth. Boundary scenarios must account for workspace/collection levels in Go.
