//go:build analytics_duckdb && cgo && race

package duckdb

// Race fixtures account for instrumented Go/runtime allocations separately from
// the fixed native query input. This changes only the explicitly requested test
// budget; production admission, headroom and child RSS checks remain unchanged.
const nativeFixtureMemoryLimit = "512MiB"
