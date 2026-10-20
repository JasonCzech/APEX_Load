# Applied validation commands

Install only after the preceding slices are committed and the guarded source hashes match. Generate API/OpenAPI/TypeScript before the full dashboard cycle; do not run embedding Go tests/builds while assets are still being generated. Run generate-and-check.sh after asset generation (independent browser workflows may still be running), capture source/assets hashes, then run-backend.sh. Re-run affected checks after findings and preserve failed cohorts separately.

The dashboard cycle executes typecheck, lint, all unit tests, build and both complete workflow auth profiles. Native semantic/race tests use their explicit 256 MiB memory/128 MiB spill fixture. The final full Go invocation permits the normal Go test cache; focused/race/native checks use -count=1. The private HAR overlay only imports the actual ZIP, verifies its hash and retains aggregate results, without replay or saved converted content.

Commands are retained alongside their scripts in the final evidence bundle.

The initial complete dev-admin cohort found five selector-only failures; preserve its original test source and artifacts. Repeat the five cases in both profiles with run-browser-repeat.sh and require every remaining complete-profile workflow to pass. The selector change does not alter application assets. Re-run changed-file lint/typecheck after that test edit.
