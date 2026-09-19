# NEXT31 LoadRunner import evaluation

Evaluated `NEXT31_iPhone14_SS_BC_UPGRADE.zip` on 2026-09-19 using the archive
converter. The original archive and its parameter data are not checked into the
repository; regression tests use synthetic data and a local HTTP server.

## Findings and repairs

1. **False malformed-URL gates.** All 61 requests received
   `credential_url_malformed` because `metadata.original_url` retained
   LoadRunner's `{server_url}` syntax while executable request fields used APEX
   templates. Credential validation interpreted the source token as an invalid
   literal hostname. URL provenance now uses the same template conversion and
   known-parameter binding as request fields. Source review retains the C source
   and line mappings. Recorded resource URL entries receive the same conversion.
   Invalid escapes and literal spaces still produce malformed-URL findings.
2. **False missing-transaction evidence.** `[Transactions]` quotes names with
   spaces. Those quotes were being retained, creating three extra declarations
   that did not match the imported runtime calls. Quoted keys now decode and
   deduplicate against `[TransactionsOrder]` correctly.
3. **URL-prefix destination corruption.** Base URL removal accepted any string
   prefix, including a longer hostname, another port, or a userinfo prefix.
   Removal now requires a URL component boundary, preserving those distinct
   destinations. This adjacent defect is covered with synthetic cases.

## Result on the supplied archive

| Check | Result |
| --- | --- |
| Active source calls | 303 converted; zero skipped |
| HTTP requests | 61, retained across 71 top-level actions |
| Transaction starts | 31 |
| Extractors | 60 |
| Parameters | 51 expected and converted |
| Data sources | 5 |
| Malformed-URL execution blockers | 61 before; zero after |
| DSL validation issues after conversion | Zero |
| Converter promotion status after fixes | `promotable` |
| Request provenance | All 61 target paths resolve to the matching request name, source file, and source line |

For the reported `actions[23].params.requests` group, child 0 maps to
`web_url` at `Action.c:345`; child 1 maps to `web_submit_form` at
`Action.c:366`. Both now pass URL credential validation.

Re-import the archive with the updated controller to obtain these corrections.
Previously saved imports are not rewritten by these converter changes.

## Verification and limits

The synthetic archive regression replays a grouped GET/form POST against a
local server, checks response correlation, repeated form fields, encoding, and
source mappings. Additional cases cover host/port/query templates, embedded
host parameters, resource URL provenance, malformed URLs, distinct origins,
and quoted transaction declarations.

The supplied business script was converted and inspected, not executed against
its target services. `promotable` describes the converter's execution gates;
it does not establish LoadRunner-equivalent replay. The report retains three
compatibility-only categories: run-logic block scheduling, action sequencing,
and unmapped runtime settings (32 active source settings). Review those before
relying on equivalent load behavior. Form destinations inferred from recorded
step names/page context also need validation against actual application HTML.

## Check results

- Final `go test ./...`: passed, including importer regressions and the API suite.
- Dashboard type checking, lint, all 399 unit-test files (4,301 tests), and the
  fresh embedded build: passed.
- Dev-admin browser profile: 112 passed, eight failed, three skipped. All seven
  LoadRunner review/save/repair workflows passed. The failures are the workspace
  and run-control cases already documented in `docs/LOADRUNNER_IMPORT_REPAIR.md`.
- Local-auth browser profile: 38 passed, one failed. All seven LoadRunner
  workflows passed. The failure is the previously documented end-to-end
  workspace case in `workflow-local-auth.spec.ts`. Consequently the complete
  `npm run test:cycle` exits unsuccessfully despite the importer checks passing.
- `python3 scripts/check-docs.py`: failed on 30 existing local-link checks in
  other QA reports and two existing missing documentation-index entries. None
  of the diagnostics names this evaluation note.
- `git diff --check`: passed.
