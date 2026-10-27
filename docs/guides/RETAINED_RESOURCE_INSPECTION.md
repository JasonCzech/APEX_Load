# Retained resource inspection

This guide describes bounded inspection of retained archive resource observations. Document lifecycle measurements have a separate [browser document evidence guide](BROWSER_DOCUMENT_EVIDENCE.md).

## Open a completed run

In a completed run's Diagnostics view, open Browser evidence and select **Inspect retained archive resources**. Opening the inspector starts an authorized archive read. Viewing the document metrics or the report preview alone does not start this scan.

The inspector reads the selected canonical run in the active project. Viewers can read authorized archives. A mismatched run/project response is refused. Closing the inspector or changing the selected run, project or archive retires pending reads and resets its controls.

A report's resource preview can contain only a subset of retained observations. The inspector pages through the retained source independently, including when the report has no preview. It does not contact the target system.

## Filter and page

The initial page contains at most 50 observations. Select 25, 50 or 100 resources per page. **Next resource page** follows the source's opaque continuation. **First page / retry** starts the same query again. Filter, sort and page-size controls remain drafts until **Apply resource filters** submits them. Applying those controls starts at the first page and discards the previous continuation. **First page / retry** reruns the currently applied query, leaving draft changes unapplied.

Capturing action, group, region, resource type, native request state, failure classification, browser engine and device profile use exact matches. Expand **Exact identity and cohort filters** for opaque source generation, request, redirect, document, initiating action and observation action identities, plus exact origin. A capturing action's name is separate from its initiating and observation action identities.

Sort by capture order, legacy duration, native request epoch start or native encoded response-body bytes. Missing values stay last in either direction. Equal values use a stable capture locator. Counts refer to retained entry observations. A zero filter match differs from an unavailable projection; unavailable counts remain unavailable.

The table has its own horizontal scroll region on narrow screens. Focus that region to scroll with the keyboard. Each row's **Inspect retained resource** disclosure shows its capture locator and whatever native request evidence was retained.

## Interpret a row

The capture locator identifies the transaction, resource ordinal and selected network-entry branch. The action capture timestamp is independent from native request epoch start and document-relative offsets. Timestamp precision is explicit. A legacy Parquet millisecond timestamp does not acquire nanosecond precision.

Observed zero duration, zero bytes, status zero and service-worker false retain their values. Missing phases, native identities, byte kinds, cache provenance or initiator provenance stay unavailable. Native phase intervals can overlap; their sum is not server processing time. An observed zero-length phase is a point, not a missing interval. UTC timestamps alone do not establish synchronized clocks across hosts.

Legacy captures can contain HTTP document resources. Native browser request details appear only when retained with a supported valid version. Unsupported or malformed optional native evidence is labeled while the valid legacy row remains inspectable. The inspection projection excludes request/response bodies, headers and debug payloads.

A retained observation count is not a complete browser network census or a capture-coverage denominator. The inspector gives no health, SLA, causality or performance verdict.

## Source integrity and continuation

**Manifest and artifact integrity verified** means the opened source matches the authorized archive's manifest and artifact proofs. **Legacy scan fingerprint** identifies the exact bounded source scan without attesting producer integrity. Missing historical project metadata does not override the durable run's project ownership.

Each continuation is bound to the same run, project, source revision, query, sort and page size. It expires after 15 minutes. Source replacement, archive deletion, changed authorization or a different query can invalidate it. Retry from the first page rather than editing or reusing a continuation in another scope.

The controller bounds source size, record count, decoder retention, response size and query duration. An unsupported Parquet layout, malformed source, expired query or exceeded limit produces an explicit error; it does not silently truncate analytical results. The request deadline also covers the contextual project resolver. Existing contextless global authentication calls are not bounded by this reader deadline; HTTP response writes use the server's separate write deadline. The decoder's retained-memory accounting is a logical allocation bound, not a process-RSS attestation.

## CLI

Read one bounded page as JSON:

```bash
apexctl analysis browser-resources --id RUN_ID --project PROJECT_ID --limit 50
apexctl analysis browser-resources --id RUN_ID --project PROJECT_ID --action-name Checkout --sort-by duration_ms --sort-dir desc
```

This command requires the canonical managed run ID. It accepts `--api`, `--api-key` and `--bearer-token`, or the corresponding `APEXLOAD_API`, `APEXLOAD_API_KEY` and `APEXLOAD_BEARER_TOKEN` variables. Help omits configured credential values.

Pass the returned `next_cursor` as `--cursor` with the same query to read another page. The CLI also supports page sizes 1 through 100. Exact filter flags use hyphens, such as `--request-id` and `--source-generation`. Select encoded body ordering with `--sort-by response_encoded_body`. Unknown, repeated or explicitly empty filters are refused before controller I/O. Output preserves unavailable fields and makes no coverage or health claim.

## Scope

This archive reader does not implement a live resource census, source clock synchronization, complete capture coverage, dependency inference, baseline intelligence or browser density qualification. HAR downloads, including the HAR holding a row, are described in [the HAR guide](BROWSER_HAR_WORKING_OWNERSHIP.md#downloads-and-exports). Native acquisition and archive/UI transport tests establish different facts and must retain separate evidence.
