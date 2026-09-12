# Validation record — Import From Source

Date: 2026-09-12. Local shared working tree; unrelated work was already present. This audit changed dashboard components and tests, with no Go implementation changes.

## Automated checks

The required `cd dashboard && npm run test:cycle` was run, using isolated workflow ports and output directories to avoid other local validation jobs.

| Check | Result |
| --- | --- |
| TypeScript type check | Passed |
| ESLint | Passed |
| Vitest | 386 files, 4,175 tests passed |
| Dashboard / embedded asset build | Passed |
| Full browser workflows, dev-admin | 104 passed; 1 profile-specific authorization test skipped |
| Full browser workflows, local-auth | 25 passed |
| Focused importer browser tests, dev-admin | 3 passed |
| Focused importer browser tests, local-auth | 3 passed |
| Whitespace/error check (`git diff --check`) | Passed |

The first cycle stopped on an unchanged credential-scanning test exceeding its 5-second timeout while multiple validation jobs were active. The next complete unit run passed all tests. Initial browser runs exposed test setup errors: a source-card locator matched two legitimate HAR-related search results, and the authenticated setup used a separate API client without the browser session. The tests now select the exact source card and seed through the authenticated browser context, refreshing the workspace hierarchy afterward. An overlapping edit briefly duplicated that context declaration; it was removed before the final run.

The final browser command exited successfully: 129 tests passed across both profiles. The one dev-admin skip requires a restricted local-auth persona and passed in that profile.

Final browser command (from `dashboard`):

```sh
APEXLOAD_E2E_PORT=18124 APEXLOAD_E2E_AUTH_PORT=18125 APEXLOAD_E2E_OUTPUT_ROOT=/tmp/apex-import-final-workflows npm run test:workflows
```

## Added regression coverage

- Shared workflow orientation at source, review, and saved stages, plus the review-only variant.
- File picker Change/Remove behavior, preserving selection on cancel, keyboard Space behavior, and error recovery.
- Oracle input changes invalidate findings; empty input cannot run; pending controls lock; old project responses are discarded; errors are announced.
- Browser navigation across 15 common-guide source entries, search/no-match/clear, accessible sample inputs, JMeter action ordering, and optional HAR comparison.
- Real-controller Oracle review invalidation and HAR validation without persistence in both authentication profiles.

The existing workflows additionally exercise actual OpenAPI and Bruno saves, malformed upload recovery, LoadRunner review/batch behavior, and other authoring paths. Passing tests establish the covered contracts; they do not establish lossless conversion or runtime equivalence for all source formats.

## Manual browser verification

Used the running dashboard in the Codex browser. Captured and inspected the catalog and all 17 source entries; validated a repository OpenAPI fixture; reviewed a synthetic Oracle source and changed it to confirm the findings cleared. Checked JMeter at desktop size and 640 × 900 with no horizontal page overflow. Screenshots and saved DOM evidence accompany the [audit](README.md).

No scripts were saved to the user's project as part of the manual audit. Persistence tests used disposable controllers/databases. No live third-party load or external runner enablement was performed.
