# Import publication primitive qualification

This slice adds a dedicated durable fence for `library.import_claims.v1`, an exact raw-byte SHA-256 paired with row presence, bounded SQL reads, and dedicated claims CAS. Existing API claim mutation and publication reads use the qualified primitive; blank-script-ID repair acquires the same fence before source work. Generic settings behavior remains unchanged. Expected-empty CAS inserts only a missing row; taking the fence does not create a placeholder claims row.

The publication fence is acquired before project/source locks. Real PostgreSQL tests observe the specific advisory-key wait for absent and existing settings, then independently probe project/source rows with `NOWAIT` before releasing the fence. SQLite tests use two independent stores and preserve single-winner CAS behavior. Raw-byte bounds use SQLite BLOB length and PostgreSQL octet length. Both byte and entry limits remain 64 MiB and 10,000. Invalid UTF-8 is refused before decoding identities, and failed repair preserves original source rows and serialized bytes.

The existing losing, unmutated direct-import rollback contract remains: revalidate receipt ownership before releasing claims, record terminal no-op claims, and preserve completed peer assets. Related failure/count fixtures now intercept the dedicated seam so qualification still exercises real durable failures. The existing lost-unmutated regression body is unchanged from the foundation HEAD.

Public scenario authoring support and dependency-aware scenario admission remain separate work. The foundation's public capability guards are unchanged. No commit, push, or build-cache cleanup has occurred in this slice.

## Evidence

Source inventory v1 (`evidence/publication-primitive-source-inventory-v1.json`) freezes 11 changed Go files and 14 unchanged dependencies. Gate index v4 (`evidence/publication-gate-index-v4.json`) records exact receipt/log hashes, independent verification of API test counts, and remaining composition limits. Historical indexes and the original red regression remain alongside the final evidence. The execution helper is retained. Root's race driver output (`evidence/publication-affected-api-import-race-1.driver.stdout.txt.gz`) is preserved as deterministic gzip; its packaging record (`evidence/publication-driver-lossless-compression-v1.json`) verifies the exact original bytes and hash after decompression. The original staging whitespace failure is retained in the gate index.

- The red bounds regression failed for both excessive entry count and excessive bytes, proving the previous blank-ID rewrite accepted data beyond the existing publication limits.
- Focused normal storage tests passed, including exact row presence and bytes, two-store concurrency, corrupt-identity refusal, output expansion bounds, and complete source/reference rollback.
- An earlier race run passed with two repeats on the same production implementation. The final frozen-source storage race repeat including those additions passed twice (80.979 s), with unchanged source bookends.
- Actual PostgreSQL 16.15 passed exact byte/presence tests and all six absent/existing snapshot/CAS/repair lock-order cases. All waiters joined and owned schemas were removed by the fixture.
- Affected API/import/rollback qualification passed 113 top-level tests normally (106.174 s) and the same set twice under the race detector (226 top-level passes, 356.346 s). Both runs had zero skips or failures. The existing lost-unmutated oracle passed once normally and twice under race; every frozen source and dependency hash remained unchanged.

The independent source review (`evidence/publication-parent-independent-review-v1.json`) and repeat scoped review (`evidence/publication-final-scoped-review-v2.json`) found no additional actionable issues. Documentation and diff checks are recorded in the final gate index. Semantic composition with the authoring-fidelity changes in `library_import_claims.go` and the composed full Go merge gate remain pending. A separate native WebKit INP failure is being fixed outside this slice; these scoped checks do not establish full-program acceptance.
