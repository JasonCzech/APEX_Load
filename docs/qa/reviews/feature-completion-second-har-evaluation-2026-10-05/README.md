# Second HAR: offline evaluation

The user-supplied `iPad_LrgBan_ViewServices_6Oct26Rel_V0.1.har` is a useful HTTP-import compatibility sample. The existing shared HAR importer retained all 435 requests with static assets explicitly included. Offline conversion produced no blocking diagnostics; this does not establish successful replay or validated correlations. No application traffic, managed run, browser or API upload was performed.

## Capture and conversion

| Observation | Result |
|---|---|
| Original input | 88,221,596 bytes; HAR 1.2 |
| Input SHA-256 | `4a145fb5f253fe0bea37b5d45b77c6e10a3b3375dfd483992b2d2d5ad90a05e4` |
| Requests retained | 435 / 435, zero filtered or skipped |
| Response-content provenance | 425 complete text, 3 complete binary, 7 unavailable |
| Base64-marked source responses | 407 |
| Generated status assertions | 435; authoring output, not executed assertions |
| Automatic correlations / extractors | 0 / 0 |
| Diagnostics | 1,307 informational, 869 warnings, 0 blockers |
| Source response errors | No 4xx, 5xx or status-zero entries |
| Offline probe process | PASS, 10.882 seconds total elapsed; source file and in-memory buffer unchanged |

All requests use HTTPS and contain iPad user-agent evidence. Most captured responses are JSON (394), with HTML, JavaScript, CSS, image and other content also present. These facts identify the captured request workload; they do not establish device rendering or successful user journeys.

## Evidence limits

Every request and response reports the literal HTTP version `unknown`. The importer records unavailable-version quality and preserves its inherited execution policy; the capture cannot qualify HTTP/2 behavior.

Connection timing is invalid negative data on 420 requests. Send and wait timing is unavailable on 72 requests each. These values remain source quality gaps, not measured zero durations. DNS, SSL and blocked phases also contain negative values; their validity was not independently qualified by this evaluation. Timing from this capture must not be treated as a complete transport breakdown.

There are 404 page records and 404 distinct referenced page identities, but every page lacks `onContentLoad` and `onLoad`. No native resource-type or initiator fields or WebSocket entries were observed. Page references alone do not prove 404 navigations and cannot supply web vitals, native browser waterfalls, interaction latency or dependency causality.

Zero detected automatic correlations is a bounded importer result, not evidence that the flow is stateless. A replay draft still needs review of session values, dynamic identifiers and request dependencies before replay validation.

The capture contains credentials and cookies: 433 requests have cookies and 144 have authorization headers. The original capture, endpoints, header values, request/response bodies, private observations and generated executable script were not copied into this bundle or Git. Only counts, fixed classifications, file identity and helper source are retained.

## Reproducibility and scope

The [aggregate shape](evidence/aggregate-shape.json), [quality inspection](evidence/aggregate-quality.json), [conversion receipt](evidence/offline-probe-1.json) and [aggregate-only raw output](evidence/offline-probe-1.log) preserve this evaluation. The [Go helper](helpers/offline-probe.go.txt) uses the existing shared HAR parser and converter with a 100 MiB input bound, pins the original input hash and installs rejecting default HTTP transports. It recorded zero HTTP attempts. The [runner](helpers/offline-probe-runner.py.txt) imposes a 90-second process deadline and records eleven directly inspected source/module hashes before and after; all remained unchanged. Its 10.882-second elapsed time includes Go compilation/startup, file reads and hashing, observation parsing, conversion and the source recheck; isolated conversion duration was not measured. That inventory is not a full transitive dependency inventory or a network-sandbox attestation.

The converter and its synthetic regression tests were previously qualified in the [HAR import compatibility slice](../../../archive/qa-reviews/BROWSER_2026-10.md#feature-completion-har-import-compatibility-2026-10-05). This evaluation makes no source-code change, raises no API/capture limit, copies no private capture content into a fixture, and does not qualify API persistence, native HAR export, browser-runtime replay, source-clock alignment or the broader feature program.
