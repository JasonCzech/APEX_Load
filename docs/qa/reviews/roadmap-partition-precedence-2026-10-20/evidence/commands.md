# Native partition precedence commands

The installed-source gate runs `go test -tags analytics_duckdb -count=1 -timeout=10m -json ./pkg/analysis/...`, the complete `docs/qa` package and `python3 scripts/check-docs.py`. The native test exercises contained preview, normalization and query with both mapping versions, physical numeric values, constant and column-selected names, v2 dimensions and unchanged source hashes.

Draft tests use a Go overlay adding only the new native test. The deliberately changed control additionally replaces native preview with an otherwise identical source file using `hive_partitioning=true`; both v1 and v2 must fail. It is not a prior-product bug. Original/control/test hashes and raw logs are retained.

No production code, embedded Help content or frontend behavior changes in this qualification slice. Preserve the preceding committed assets and their manifest. This does not qualify precision disclosure, schema drift or unavailable platform/reference cohorts.
