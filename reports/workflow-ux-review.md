# Workflow use and automatic processing

Workflows are reusable product features, including the built-in search and document tools. Users need to answer two separate questions: “Can I run this now?” and “Will this run automatically for future changes?” The interface and API expose those decisions independently.

## Usage model

| Decision | Configuration | User-facing behavior |
| --- | --- | --- |
| Allow people to invoke a workflow | `manual_enabled`, a manual entry point, published state | Run once is available subject to selection, inputs, setup and permissions |
| Subscribe to future changes | `automatic_enabled`, active trigger rules, published state | Automatic runs summarizes the events and collection |
| Prepare a custom definition | Draft state | Editable; accepts no new runs |
| Retire a definition | Archived state | Accepts no new runs; accepted work and history remain |
| Replace a product provider | Publish and enable a role workflow for one collection | Pauses the other automatic provider there; manual use remains separate |

Automatic content processing belongs to a library or list. An optimizer needed in Photos must not affect Contracts. Content-event rules require an explicit collection; there is no inherited workspace content setting. Workspace-level services such as the AI batch coordinator retain genuine workspace schedules.

The workspace catalog can prepare shared manual tools. Collection settings link to the catalog with that library or list selected. One shared selector makes the scope visible while comparing features. Default search, preview and content services are represented by explicit collection configurations. Paused or deleted defaults stay as configured.

## Main journeys

### Optimize one photo without future automation

1. Select the photo's library in Workflows.
2. Prepare Optimize image storage, allowing manual runs and leaving automatic runs off.
3. Select the photo and choose Run workflow → Run once.
4. Review the launch explanation and queue the operation.
5. Follow the run and any approval task. Automatic settings stay unchanged.

The server rejects unsupported file types and explains unavailable image detection or storage before enqueueing. It checks selected-item write access and the version author's execution permissions. A terminal skip reason is presented as No changes needed.

### Optimize future uploads in one library

Enable automatic runs on that library's optimizer. Enable upload events, replacement events, or both independently. The summary names the active rules and collection. Other libraries keep their own settings. Disabling automatic processing does not revoke manual access.

### Customize a built-in safely

Duplicate to customize creates an editable draft with automatic processing off. It does not change the source workflow or its automatic provider. Publishing and explicitly enabling the copy is the deliberate replacement action. The role's output contract keeps following workflows connected to the feature.

## Availability and execution

The item menu consumes server-computed actions, including disabled choices with reasons. The same admission path powers the launch preflight and the final request. This prevents independent frontend eligibility rules from disagreeing with the server. Required inputs are collected before launch, and a changed expected version is rejected so the user can review the new definition.

Manual invocation targets a workflow and immutable version directly. Automatic role selection cannot silently suppress a targeted manual request. Accepted work retains its version even if the workflow is edited, paused, archived or deleted afterward. Dispatch rechecks the caller's item access and records visible failure when access has been revoked.

The confirmation screen identifies the execution author. Workflows execute as that version author; the initiating person remains recorded as the actor. Known setup checks are an early aid, not a guarantee that later activities cannot fail.

## Explaining automatic behavior

A read-only check evaluates an item against upload, replacement and metadata-change scenarios. It uses the actual event matcher, active rules, collection/provider scope and condition. The result explains automatic processing being off or the scenario failing to match. It emits no domain events and runs no activities.

## Feedback and history

Run history distinguishes manual, automatic and scheduled origins, offers status and workflow filters, and pages results. Manual system runs remain discoverable by default and use ordinary retention. High-volume automatic system runs are opt-in in general history. Item readers receive permission-scoped summaries; workspace managers can inspect steps and results.

Labels distinguish queued work, running work, waits for input or approval, completion, failure and successful no-change results. A queued response does not claim the file was changed.

## Validation scope

Backend regression coverage exercises independent manual admission, pinned accepted versions, targeted manual system tools, draft duplication, collection-specific automatic rules and disabled rules. API checks exercise availability and automatic explanation without executing work. Frontend tests cover launch preflight, selected collections, optimistic concurrency, input forms and item actions. The schema, generated Ent model, OpenAPI contract, documentation and sample packages share the same usage model. Frontend verification uses DOM tests and a production build; a browser surface was unavailable for visual inspection.

Direct workflow composition remains an engine concern beyond these usage controls; the feature does not introduce a separate subworkflow engine. The interactive concept in `visuals/workflow-usage.html` illustrates independent manual and automatic controls, while production behavior is defined by the application and API described here.
