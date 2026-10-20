# Mapping-preserving reassociation — 2026-10-20

This [NA-A slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-a--deterministic-import-semantics) adds a negotiated association contract for retained identity, summary and histogram mappings. It preserves the existing legacy PUT response and revision/CAS owner. The new PATCH method is rejected by older route tables before they can rewrite metadata; the dashboard requires the exact capability and validates the returned mapping, revision and ownership before adoption. It never falls back to PUT after a rejected PATCH.

The [minimum-controller and rolling-upgrade barrier](../../../NATIVE_ANALYTICS.md) requires every serving controller and writer to support this contract before these datasets are reassociated. Capability reads alone do not certify the entire pool. Older clients using PUT against older controllers remain unsafe; previously discarded metadata requires re-import. Precision-consent workflows and advanced mapping authoring gates are separate open work.

## Validation and review

- Full Go regression passed 168 packages, 17,686 top-level tests and 17,386 subtests, including the private HAR import through a test-only overlay.
- The attached ZIP retains its original hash and aggregate result: HTTP 200, 803 requests, 476 actions and zero DSL issues. No target replay or converted-content persistence.
- Native mapping-preservation tests and race checks, route authorization/CAS refusals, generated API/TypeScript/access contracts, analytics inventory and documentation QA passed.
- Dashboard typecheck, lint, all 718 unit-test files (8,022 tests), build and both complete workflow profiles were exercised. The first dev-admin run had 321 passes, six declared skips and five new selector failures; all five passed after the accessible-role locator fix in both profiles. The complete local-auth run passed with the fix. In-app Help is covered in both profiles. Original failures, repeat results and exact source/asset/controller hashes remain in [the evidence bundle](SHA256SUMS).
- Original-component regressions, unavailable-resource fixture results and subsequent passing drafts remain separate from the final installed-source evidence.

The native fixture uses real upload, normalization, immutable output metadata and SQL publication under a declared 256 MiB memory / 128 MiB spill profile. The first default-spill reservation was correctly refused on this host and remains retained; this is not default-resource or reference-host qualification. Browser fixtures exercise real workspace navigation/authentication with controlled peer responses; the native tests verify actual persisted mappings and outputs.

Final applied-source and staged review found no remaining actionable findings after correcting missing-metadata fallback, strengthening ownership/authorization refusals and verifying negotiated responses without a legacy-route fallback.
