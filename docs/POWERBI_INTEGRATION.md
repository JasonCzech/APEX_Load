# Power BI Integration

APEX Load can publish a completed run's results to a Microsoft Power BI **push dataset** so release managers and executives can track performance trends in the BI platform they already use. Publishing is **manual and per-run**: an editor opens a completed run's report page and clicks **Publish to Power BI**.

What gets published:

- one **run summary row** per run (verdict, SLA rollup, throughput, percentiles), and
- one **transaction row** per action and per group (stats + per-transaction SLA outcome).

Full time-series data is *not* pushed; the run row carries a deep link (`apexload_url`) back to the APEX Load report for engineers who need per-interval detail.

## Prerequisites (platform team)

1. **Azure AD app registration** (service principal) with a client secret.
2. Power BI tenant setting **“Allow service principals to use Power BI APIs”** enabled (Admin portal → Tenant settings → Developer settings), scoped to a security group containing the principal.
3. The service principal added as **Member** (or Admin) of the target Power BI **workspace**.
4. No API permissions/consent are required beyond workspace membership for push-dataset calls; the token is requested with the `https://analysis.windows.net/powerbi/api/.default` scope.

## APEX Load configuration (system admin)

Settings → Integrations → **Power BI**:

| Field | Notes |
|---|---|
| Enabled | Gates the publish action on run reports. |
| Azure AD tenant ID | Directory (tenant) GUID. |
| Client ID | Application (client) ID of the service principal. |
| Client secret environment variable | Name of a controller env var (e.g. `APEXLOAD_POWERBI_CLIENT_SECRET`) holding the secret. The secret value is never stored or returned by the API. |
| Workspace ID | GUID of the workspace (from its URL). |
| Dataset name | Default `APEX Load Performance Runs`. Created automatically on first publish. |
| Authority URL / API base URL | Defaults target the public cloud (`https://login.microsoftonline.com`, `https://api.powerbi.com/v1.0/myorg`). Point these at sovereign or internal endpoints when applicable — both hosts are added to the outbound allow-list and may resolve to private address space; all other egress remains SSRF-guarded. HTTPS is required in production (`APEXLOAD_POWERBI_ALLOW_INSECURE_HTTP=true` is the explicit lab-only escape hatch). |
| Dashboard base URL (optional) | Absolute base for the `apexload_url` deep links pushed into the dataset (e.g. `https://apexload.example.com`). Recommended: when unset, links fall back to the publish request's own host (strictly validated), which may be wrong behind path-rewriting proxies. |

Use **Test connection** after saving to verify token acquisition and workspace access.

## Dataset schema (v1)

Two tables, related on `run_id` (relationship is created with the dataset, cross-filtering both directions).

### `runs` — one row per published run

| Column | Type | Source |
|---|---|---|
| `run_id` | String | Test ID |
| `publish_id` | String | Unique per publish; use for dedupe |
| `test_name` | String | |
| `project_id`, `release_id`, `application_id` | String | Run provenance |
| `status` | String | Final run status (COMPLETED/FAILED/…) |
| `verdict` | String | Report verdict (Pass / Conditional Pass / Fail) |
| `started_at`, `completed_at` | DateTime | Run wall-clock bounds |
| `duration_seconds` | Double | |
| `avg_tps` | Double | total transactions / duration |
| `total_vusers`, `peak_active_vusers` | Int64 | |
| `total_transactions`, `total_errors` | Int64 | |
| `error_pct` | Double | |
| `avg_ms`, `min_ms`, `max_ms` | Double | Response times |
| `p50_ms`, `p90_ms`, `p95_ms`, `p99_ms` | Double | Percentiles |
| `peak_tps`, `peak_target_tps` | Double | |
| `sla_evaluated`, `sla_passed`, `sla_warned`, `sla_failed` | Int64 | SLA rollup counts |
| `apexload_url` | String | Deep link to the APEX Load run report |
| `published_at` | DateTime | Publish timestamp |

### `transactions` — one row per action + one per group, per run

| Column | Type | Notes |
|---|---|---|
| `run_id`, `publish_id`, `published_at` | | Join/dedupe keys |
| `kind` | String | `action` or `group` |
| `name` | String | Transaction/group name |
| `count`, `errors` | Int64 | |
| `error_pct` | Double | |
| `avg_ms`, `min_ms`, `max_ms`, `p50_ms`, `p90_ms`, `p95_ms`, `p99_ms` | Double | |
| `peak_tps` | Double | |
| `has_sla` | Bool | Actions only |
| `sla_ms` | Double | Configured p95 SLA |
| `sla_result` | String | `pass` / `warn` / `fail` / `na` |

## Re-publish and dedupe

Power BI push datasets cannot delete individual rows, so re-publishing a run **appends** a second copy of its rows (a new `publish_id`). APEX Load blocks accidental duplicates — a publish is refused with a conflict when a previous attempt already pushed rows for the run into the same dataset (whether it completed or failed partway), unless the user confirms **force**. A `partial` publish leaves transaction rows without a matching runs row; because the table relationship joins on `run_id`, those orphaned rows would double-count in transaction-level visuals after a re-publish — which is why **reports must filter both tables to the latest `publish_id` per run**, not just the `runs` table.

The Power BI push API has no idempotency key, so APEX Load does not retry ambiguous row mutations. A timeout, throttle response, or server error stops the publish as `partial`; only an explicit authentication rejection is retried after refreshing the token. This prevents a client retry from duplicating rows within one `publish_id`. The service can still apply a chunk before returning an ambiguous error, so an operator-forced re-publish may append rows under a new `publish_id`; the latest-publish filter above remains required.

Sample DAX filter measure:

```dax
Is Latest Publish =
VAR latest =
    CALCULATE ( MAX ( runs[published_at] ), ALLEXCEPT ( runs, runs[run_id] ) )
RETURN IF ( SELECTEDVALUE ( runs[published_at] ) = latest, 1, 0 )
```

Apply `Is Latest Publish = 1` as a report/page filter (equivalent measure on `transactions` using `publish_id`).

## Limits and operations

- Push API limits: 10,000 rows per POST (APEX Load chunks automatically), 1M rows/hour, 5M rows per table. Manual per-run publishes stay far below these.
- 429/5xx responses are retried with `Retry-After` honored; a failure mid-publish is recorded as `partial` and surfaced on the run report — recover by force re-publishing.
- The run row is pushed **last**, so a run never appears in trend visuals until all of its transaction rows landed.
- **Schema drift**: if the dataset predates a schema change (or its tables were altered), publishing fails with a drift error. Drift detection is dataset-scoped — it compares against the newest publish into that dataset across all tests. Settings → Power BI → **Recreate dataset** deletes and recreates it with the current schema — this erases all previously pushed rows in Power BI (APEX Load's publish history is retained). The recreated dataset has a new ID, so previously published runs can be re-published into it without `force`.
- The published `verdict` honors an analyst's verdict override saved via report annotations (executive profile wins, then engineer, then audit), matching what the rendered reports display.
- Every publish is recorded in the `powerbi_publishes` table and in the audit log (`powerbi.publish`).

## Recommended report layout (built by the BI team)

**Page 1 — Release Scorecard.** KPI cards (latest verdict, SLA pass %, error rate, p95, peak TPS); a verdict strip of runs over time colored pass/conditional/fail; slicers for project, release, date range.

**Page 2 — Release Trends.** Line charts across runs: p90/p95/p99 response times, peak TPS, error rate, SLA pass % (`sla_passed / sla_evaluated`). Use `release_id` as a categorical axis to compare release-to-release.

**Page 3 — Transaction Drill-down.** Matrix of `transactions[name]` × runs with `p95_ms` conditional formatting (heatmap) to spot which transaction regressed in which release; a top-regressions table ranked by p95 delta vs the previous run; drill-through with `apexload_url` linking back to the full APEX Load report.

**Page 4 (optional) — SLA Compliance.** Matrix of `sla_result` per transaction per run; compliance % trend over time.
