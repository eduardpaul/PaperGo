# List integrations

Integrations are configuration on ordinary lists. They do not introduce another resource kind or change a list's existing items, content types, folders, permissions or publication policy. The first provider is **HTTPS iCalendar URL** (`ical_https`). Libraries do not accept this provider.

Open **List settings → Integrations → Add integration**, choose a name and supply a public HTTPS iCalendar URL. Google Calendar's secret iCal address works without OAuth or an API project. Adding the integration attaches the shared Event content type and queues the first refresh. Use an existing calendar view grouped by **Start**, or create one in **Views**. Recurring instances are expanded in the visible month; opening an occurrence opens its ordinary item. All-day events stay on their calendar date; timed events follow the viewer's preferred time zone.

A list can have up to 50 integrations. The same URL can be used in different lists; a duplicate in the same list is rejected. Each integration has independent event identities, including when two feeds reuse UIDs. Local items and local metadata such as keywords are independent of refreshes.

Reading status requires list read access. Configuration and Refresh now require list Manage and `list.write`; no workspace-owner or external calendar-owner check is needed. Routine responses omit private configuration, provider state, credentials and feed URLs. Subscription URLs must use HTTPS on port 443. Every connection and redirect rejects private, loopback, link-local and other non-public destinations; HTTP redirects, proxies, unbounded responses and more than five redirects are refused.

Configuring integrations with a REST token requires `list.read` and `list.write`. Automatically attaching the Event content type does not require `contentType.read`; choosing a catalog type directly through the content-type API still does.

## Refresh and reconciliation

Adding the first integration creates the workspace's **Refresh list integrations** built-in workflow. Every 15 minutes in UTC, its `integration.refresh` activity queues durable operations for active integrations. Each operation rechecks the configuring user's current list permissions and stored REST credential, when applicable. Revoked credentials do not authorize automatic work. A disabled workspace workflow stays disabled when another integration is added. Ordinary workflows can use the activity with `integration_id` for one managed subscription, or omit it with workspace Manage access.

Refresh now queues a tracked operation for that integration only. Its progress is visible under Background work. Downloads happen outside the database writer. Conditional HTTP validators avoid repeated transfers. Expiring source leases serialize fetching, reconciliation and configuration changes; conflicting configuration changes return 409. A crash resumes pending operations after the lease expires.

Refresh credentials are revalidated in the transaction that applies the downloaded snapshot or records a not-modified response. Revocation or expiry during download fails the operation and retains the previous successful snapshot and HTTP validators.

A complete valid snapshot updates source-managed titles, descriptions, locations, start/end times, all-day dates and recurrence. Those fields and the Event content type are protected through the ordinary API, bulk editing, history restoration and cross-list moves. Local metadata stays editable. Pause retains ownership of the managed fields. Remove integration retains existing items, recurrence and history as editable local content. Replacing a URL preserves identities; select **This is a different feed** to detach existing items and start new identities.

Events absent from a complete snapshot, or cancelled by the provider, go to the recycle bin. Returning events restore with the same identity and local metadata. Recurring series retain their IANA time zone, daylight-saving behavior, excluded dates, additional dates and moved/cancelled exceptions. Removed exceptions are reconciled. Export uses item-based UIDs, so matching original UIDs from separate subscriptions remain distinct.

Fetch, parse and validation failures retain the entire previous snapshot. Reconciliation is one transaction, including absent-event removal; a custom required field or validation rule cannot leave half an import visible. A valid empty feed recycles that subscription's active items. Feeds over 10 MiB or 2000 VEVENT components fail instead of being truncated. Unsupported zones, duplicate or orphan identities, EXRULE, period RDATE, RANGE exceptions and sub-day recurrence rules fail before reconciliation. Recurrence supports daily, weekly, monthly and yearly rules. Calendar range queries are bounded to 93 days, 10000 visible series and 10000 occurrences; exceeding a limit returns an error.

Replication reflects the provider's published date window and cache. Refresh cannot force a provider to publish newer data. Invitations, RSVP, alarms, reminders, attachments, conferencing and tasks are not imported.

## Generic storage

The API accepts a provider-owned `configuration` object, such as `{"url":"https://…"}` for iCalendar.

`list_integrations` stores provider kind, encrypted JSON configuration, a configuration identity hash, opaque provider state, concurrency/lease information, status and configuring credentials. Provider state is hidden from routine responses. iCalendar's URL and HTTP validators are JSON properties owned by its implementation; they are not provider-specific database columns.

`integration_items` binds a provider's external identity to an ordinary item and records the field keys it manages. Detaching an integration clears its ownership pointer and leaves local content intact. `integration_revision_data` stores immutable provider-kind/JSON data against an ordinary item revision. This keeps recurrence and exceptions on the same head/published permission boundary as item values. Core resource, revision and surface schemas contain no provider-specific columns. Future providers can use these stores without adding columns for their own payloads.

Configuration is AES-GCM encrypted and bound to its destination list. The server creates a private 32-byte deployment key at `<DATABASE_PATH>.integrations.key`; a database with configured integrations requires its original key. Deployment backup archives include and restore this key alongside the database and blobs. Portable core packages retain integration names and kinds, omit secrets and ownership, and report warnings to reconfigure integrations at the destination. Imported items remain local and keep their revision data.

## API

| Action | Endpoint |
| --- | --- |
| List status and public configuration | `GET /v1/resources/{id}/integrations` |
| Add `{kind:"ical_https",name,configuration:{url}}` | `POST /v1/resources/{id}/integrations` |
| Change `{name?,paused?,configuration?:{url},reset?}` with `If-Match` | `PATCH /v1/resources/{id}/integrations/{integrationID}` |
| Remove, retaining local items, with `If-Match` | `DELETE /v1/resources/{id}/integrations/{integrationID}` |
| Queue one refresh | `POST /v1/resources/{id}/integrations/{integrationID}/refresh` → 202 operation |
| Read item ownership and managed-field keys | `GET /v1/items/{id}/integration` |
| Query `{from,to,query?,surface?}` | `POST /v1/resources/{id}/calendar/occurrences` |
| Export visible Event content | `GET /v1/resources/{id}/calendar/export?surface=auto` |

The complete request and response contract is in [OpenAPI](../api/openapi.json).
