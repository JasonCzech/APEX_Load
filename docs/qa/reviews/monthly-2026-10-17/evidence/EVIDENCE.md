# Monthly review evidence

These files support the review at `9c00643e15bab68e8f1e78a81ec32b3de8dd9cdc`. A failing reproduction is an assertion of the intended behavior that the reviewed implementation violates. The control tests exercise existing supported behavior and passed.

Go reproductions are retained with `.go.txt` extensions. To repeat one, temporarily restore the appropriate file to its original package as a `_test.go` file, run its focused test, and remove it afterward. They depend on that package's existing test helpers. Do not copy all evidence files at once: the original and v2 API/storage fixtures have overlapping test names. Use the v2 fixtures for this snapshot.

| Evidence | Original package or purpose |
| --- | --- |
| `monthly_review_rendezvous_adoption_v2_test.go.txt` | `pkg/api`, active adopted rendezvous lifetime |
| `monthly_review_draft_race_v2_test.go.txt` | `pkg/storage`, exact draft-save revision |
| `monthly_review_plan_stop_v2_test.go.txt` | `pkg/api`, stop intent across controller replacement; boundary model described in the report |
| `monthly_review_partial_reimport_v2_test.go.txt` | `pkg/api`, partial batch retry after an injected storage outage |
| `orchestration-storage-v2-results.txt` | Commands, failure excerpts, and validation limits for the four tests above |
| `monthly_review_dns_test.go.txt` | `pkg/protocol/http`, reachable address fallback |
| `monthly_review_wait_test.go.txt` | `pkg/protocol/appium`, eventual element text |
| `monthly_review_generated_test.go.txt` | `pkg/engine`, importer-to-interpolation identifier equality |
| `monthly_review_summary_test.go.txt` | `pkg/analysis/duckdb`, overlapping and truncated summary windows |
| `monthly_review_histogram_test.go.txt` | `pkg/analysis/duckdb`, the same cases for histogram windows |
| `summary-rerun-output.txt`, `histogram-rerun-output.txt` | Failing native DuckDB reproductions on the refreshed snapshot |
| `analytics-controls.txt` | Passing selected existing summary/histogram tests |
| `deployment-controls.txt` | Passing selected current-values Helm, KEDA, and HA render tests |
| `grid-v2.patch`, `catalog-save-v2.patch` | Focused frontend reproduction patches from an isolated harness |
| `helm-legacy/*.txt` | Actual historical-values Helm render failures and successful control after adding the new default sections |

Native analytics commands use `go test -tags analytics_duckdb ./pkg/analysis/duckdb -run '^TestMonthlyReview…$' -count=1`. The review's disposable Go cache was `/tmp/apexload-monthly-review-gocache`; it was cleaned after validation. Other source and dependencies were preserved.

For the Helm reproduction, copy the current chart to a temporary directory, replace its `values.yaml` with the file from `f757e964ba455ffb603028b6310bd09af418853d`, and use the non-placeholder overrides from `archiveStagingHelmArgs` in the existing deployment tests. This follows the repository's own reused-values simulation. Add the current `networkPolicy`, `http3`, and `archiveStaging` default sections in turn: the recorded failures show each missing section; rendering succeeds after all three are supplied.

The MCP result was a transport-level inspection plus a simulated strict endpoint, not a live-server run. Frontend harness versions and the Stop-plan fixture's limits are documented in the main review.
