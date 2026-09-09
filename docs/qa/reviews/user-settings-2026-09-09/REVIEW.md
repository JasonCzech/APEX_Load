# Personal Settings review — September 9, 2026

## Delivered behavior

Settings remains visible in the sidebar and keyboard navigation for non-admin users, including the development User preview. The personal page uses the existing administrative Settings grid, section navigation, panel toggle, form fields, colors, and responsive layout.

- Appearance: the existing six theme choices (including System), plus device-aware reduced motion.
- Tables & editor: a remembered 10/25-row default for standard tables and the existing synchronized editor word-wrap preference. Per-table overrides remain available; specialized/server-paged tables retain their controls.
- Notifications: the existing test-outcome notification preference in the native desktop app; browser users see an accurate availability explanation.
- Account: read-only name, email, and sign-in provider, with organizational profile-management guidance.

Preferences apply immediately and are browser/device-local, rather than account-synchronized. Administrative Preferences uses the same content component. The administrative Settings component mounts only after effective access establishes system/domain/project management permission; ordinary users and User preview receive the personal component, including for direct administrative destination URLs.

## Review

Reviewed the personal page at 1440×1000 and 923×987 in Dark and Light themes, including navigation expansion, collapse, section changes, readable labels/help, and shared administrative Preferences. Screenshots are alongside this report. Restored Dark theme, Admin preview, and the original viewport after inspection.

Fixed a missing administrative Preferences region label found by regression tests. Added explicit focus handling when selecting a section closes narrow navigation. The page performs no administrative settings requests or identity mutations.

## Validation

Completed `npm run test:cycle` and reran the local-auth profile after correcting its new account assertion from “User” to the seeded “Local Viewer” display name.

- Type checking, lint, and embedded dashboard build: passed.
- Unit suite: 3,962 tests in 362 files passed; the final personal-page review changes also passed their focused rerun.
- Dev-admin browser profile: all 76 workflows passed, including existing accessibility and five-theme/two-width checks.
- Local-auth browser profile: all 9 workflows passed after the fixture assertion correction. The new viewer test verifies theme, reduced motion, table-row default and word-wrap persistence; read-only account identity; direct administrative destination fallback; no system-settings fetch; and HTTP 403 for an administrative settings write.
- `git diff --check`: passed.

Browser workflows used disposable controllers and databases. No live account or test-run records were modified.
