# Project live tables

Project live tables share runtime data between concurrent runs in one project and retain that data after a run finishes. Open **Repository → Live tables** in the intended project. Existing **Shared test data** pools keep their snapshot behavior; creating a project table does not convert or modify a pool.

| Choice | Data lifetime |
| --- | --- |
| Existing read-only pool binding | Each run receives its own immutable snapshot. |
| Run-scoped live binding | Groups in one run share a table; its explicit run-end policy controls finalization. |
| Project-scoped live binding | Admitted runs share the project's current table instance; finishing a run detaches it. |

The [runtime guide](LIVE_DATA_TABLES_RUNTIME.md) describes table operations and leases. The [generated API reference](swagger.yaml) supplies the request and response contracts. [Native script services](NATIVE_SCRIPT_DATA_SERVICES.md) describes Java and host JavaScript access through an admitted alias.

## Create and bind a table

1. Select **Create project live table**, name the table and declare its columns.
2. Choose **Native work queue** for records with stable IDs, versions and optional leases, or **Shared columns** for positional cells. A missing cell differs from a present empty string. Project schemas are fixed after creation.
3. Choose **Start empty**, or select a seed pool in the same project. The seed is read once. Subsequent edits to that pool do not change the live table, and recovery does not reread the pool.
4. Set the row and byte limits, then choose **Create table**. Project tables require durable storage and a configured wrapping key for encrypted recovery. The controller refuses creation or admission when these prerequisites or its bounded capacity are unavailable.
5. In the scenario's **Data** step, choose live access and **Project** table scope, then select the table and an alias. Choose explicit script actions, read-next, pop, or a native-queue iteration lease. Configure the empty-row policy and operation timeout as needed.

A project binding stores the project table reference. Its profile, schema, durability and lifecycle come from the definition. Run seed and run-end policies do not apply. Launch checks the current project permissions, live-table capability and active instance again. A saved binding is not permission to attach later to an archived or quiescing table.

For `next` operations, omitting `cursor` uses the table-wide shared sequence. An explicitly named cursor is private to the run/group/alias attachment and retires with that attachment; another run does not inherit its position.

The table details show the immutable instance ID, configuration and data revisions, capacity, leases and attached runs. Several runs can use the same instance. Finishing or deleting one run revokes and settles that run's attachment without erasing another run's work or writing rows back to the original pool.

## Inspect and export deliberately

Table metadata refreshes periodically. Row values are fetched only when you select **Preview up to 100 rows** or **Export table**. Both operations are audited and require current project read permission. Service accounts additionally need the explicit `read` scope; unrelated result-reading or broad write scopes do not substitute for it.

A preview is tied to the displayed instance and exact data revision. Large cells may reduce a page below 100 rows; **Next rows** and **Previous rows** follow the actual returned page boundaries. If another writer advances the revision during inspection, refresh and preview again. **Hide row values** also suppresses an outstanding preview response.

Export downloads bounded JSON for one consistent instance/revision. It preserves absent cells, empty strings, record identities and decimal-string versions. Its maximum encoded response is 16 MiB. Concurrent writes may cause an export conflict; it does not silently combine pages from different revisions. Raw previews and exports share bounded admission, so a busy response can be retried later.

Archived instances remain available for authorized inspection during retention. A reset's former instance can also be inspected through its retained immutable instance ID in the API. Retention expiry or project deletion removes that availability.

## Append or conditionally edit rows

Data management permission is required for **Append row** and **Apply row change**. Service accounts additionally need explicit `scripts:write` scope. Select the cells to include: appending leaves an unchecked cell absent, while updating leaves an unchecked cell unchanged. A checked blank input writes an empty string.

Use **Edit row** after a deliberate preview. An update, selected-cell clear or integer increment always supplies the reviewed table revision. Native queues also require the record ID and version and reject edits to leased rows. Shared-column edits use the reviewed one-based position. Integer deltas, revisions and versions use decimal strings, preserving values beyond JavaScript's exact integer range.

On a conflict, the draft stays visible. Refresh and preview the current row, review it, then choose **Use reviewed row version and keep my draft** before applying again. A metadata refresh alone never changes the revision on which an existing edit was based. The API can return `condition_failed` with a committed receipt but no data change; this also requires review before a new command.

## Reconcile an uncertain command

Each operator command has a canonical UUID idempotency key, issuance time, actor, expected instance and exact payload. The state change, encrypted receipt and value-free audit outcome commit together. An audit failure prevents the command from being acknowledged or published.

If the connection fails after submission, keep the page open and select **Retry same command**. The dashboard retains the original key, time and body and prevents a new command while reconciliation is pending. Authentication, capacity and other retry failures also preserve this identity: an error received after submission does not prove that the command failed to commit. Retry becomes available when current data-management permission permits it. An exact retry returns the original outcome even when its original expected revision is now stale. Reusing that key with different content conflicts.

The table shows its retry window, which defaults to ten minutes. Exact operator receipt recovery remains available within that window after reset or archive: it reads the retained encrypted outcome without reopening the old instance for writes. The same actor must still have current project and data-management permission. New commands cannot address the retired instance. A receipt does not grant lasting access, and expiry returns an explicit error. After expiry, the dashboard retains the pending identity and disables retry. Fetch a current preview, inspect retained evidence as needed, then explicitly acknowledge that the earlier command may have completed before choosing **Clear pending command after review**. The acknowledgment must refer to the current preview; a changed or hidden preview requires review again. Clearing the pending command also clears its edit draft and sends no new command. The prior outcome cannot be recovered after expiry, so reconcile the observed state before deliberately preparing a new command.

The dashboard's pending command is held only in the current page's memory; it is not persisted across closing or reloading the page. API clients should retain the exact request identity and body through the advertised retry window without logging row values as routine operational metadata.

## Govern the lifecycle

**Table lifecycle** requires project-administrator permission, a reason, confirmation, and current configuration, instance and data revisions.

| Action | Effect |
| --- | --- |
| Quiesce | Refuses new run attachments; admitted work can finish. The table becomes quiesced when active attachments detach. |
| Resume | Allows new attachments and new operator writes again. |
| Reset | Creates an empty instance with a fresh immutable ID and the same schema and limits. It does not reread the seed pool. |
| Archive | Stops new use and retains the closed instance for authorized inspection during retention. |

Reset and archive require quiescence, no leases and no remaining run attachments, including detached attachments retained for retry cleanup. The page explains these blockers. Retained operator receipts do not force a lifecycle delay because their exact outcomes can be recovered independently. Quiesced tables reject new operator edits; an already committed command can still be reconciled.

Reset never makes an old capability valid for the new instance. Failed recovery makes a table unavailable rather than silently starting an empty replacement. The existing shared-data pool editor remains the place to maintain reusable snapshot input rows.
