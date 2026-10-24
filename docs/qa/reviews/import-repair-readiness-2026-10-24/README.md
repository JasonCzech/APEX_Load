# LoadRunner import, Script Editor and debug readiness — 2026-10-24

An evaluation of a real LoadRunner 24.3 Salesforce Aura iPad archive (customer data, not committed) walked the import wizard, the Script Editor and **Debug one iteration** and found that the three surfaces told different stories: guided repairs applied during validation were lost on save, so the editor showed blockers the import had resolved and debug was refused; the import and the editor reported missing values the runtime provides; the Debug button stayed enabled over known blockers and failed with a raw gate string; and every count disagreed. The user approved four fixes and the standard independent review cycle, stopping at the first round with no medium findings. This record states the resulting contract and the review.

## Contract

### FIX1: a reviewed draft is saved as reviewed

- A validated archive whose draft was changed in the review (guided repair, applied parameterization or shared values; `reviewedDraftEdits`) is saved from that draft, never converted again (`runConvertValidatedItems` reuses it). Validate and convert return the same envelope, and the save restores the exact attested candidate, so no server change was needed for the save itself.
- Optional parameterization suggestions never halt a save and are not applied automatically.
- Reviewed drafts are protected from batch validation; the queue row offers **Validate again** (which discards the repairs), except while an unconfirmed atomic save of that draft is being reconciled. A reviewed draft keeps the name its review showed (`reviewNameOverride`), and a repaired lane keeps its own credential requirements (`script_index`).
- A refused save is classified: an expired or used candidate, or `409 import_update_target_stale` (and other definitive `4xx`), drops the retained operation and keeps the draft; an unconfirmed save (`5xx`, transport) is resent unchanged, even if the re-import choice changed since, so the server's operation receipt answers whether it committed.

### FIX2: one producer contract

- `pkg/dsl/varflow` models every source step's writes, reads and removals as the engine performs them (exact name plus lower-case alias, `SelectAll` arrays `NAME_1..n` and `NAME_count`, `web_convert_param`, the `lr_save_*` family, OAuth outputs, live tables) with the execution-order walk (branches intersected, loops promoted only when they run, `do_while` bodies before their condition, parallel outputs prefixed). Server readiness (`requestimport`), the import dependency review (`importreview.AnalyzeDependencies`) and the engine conformance test use it; `dashboard/src/components/variableFlow.ts` ports it, and the shared corpus `pkg/dsl/varflow/testdata/readiness_cases.json` holds both to the same expectations.
- Runtime fidelity: engine steps that only log or use their arguments literally (transactions, timers, metrics, runtime settings, rendezvous) read nothing; an `http_sequence` wrapper reads only its attribution, not the copy of its first request's target; HTTP, SOAP and Java HTTP keep a missing `{{var.*}}` as placeholder text (a Java HTTP stored body that is not base64-encoded excepted); an undeclared `{{param.*}}` blocks.
- Import-evidence metadata (`messaging.MetadataKeyIsEvidence`: `original_url`, `source_*` provenance, `source_evidence`, `asyncapi_*`) is attribution, never interpolated and never refused by the engine; Kafka and JMS sends no longer put `source_evidence` on the wire. `bind_variable` rewrites exactly the metadata replay reads and leaves nested steps and evidence as recorded.
- The engine dispatches source steps on the canonical action type, so legacy `loadrunner_*` spellings write their outputs.

### FIX3: Debug follows the server, and blockers are repairable in the editor

- The start gate (`replayStartGate.ts`) counts only server-equivalent blockers (review gates, blocking variables, data-source issues, missing project secrets, current server validation issues other than promotion folds). While any exist, Debug reads **Resolve N blockers**; each click (and F5) opens the next blocker where it is fixed and never saves. A draft clean of gates whose saved copy still has them reads **Save and debug**.
- A refused stored-script start answers `400` with `code: script_replay_blocked`, `gate_scope`, `issues` and the saved copy's `preflight` (step names scrubbed); the editor shows it as readiness data, not an error, and never counts it twice.
- `POST /api/v1/scripts/{id}/repairs/preview` and `POST /api/v1/scripts/{id}/repairs/apply` apply the guided repairs (`select_tls`, `replace_proxy_secret`, `bind_variable`, `add_parameter`, `add_extractor`; `inspect` for preview; `restore_body` only by re-import) to a saved script: strong `If-Match` (`428`, `412`), editor access, Java Vuser and memory-only stores refused (`409`, `503 script_revisions_unavailable`), a credential allowance that admits only the operation's own transport fields, a trusted durable write and the audit event `script.repair.apply`. Apply returns the stored copy and its validation without repeating the script. One `select_tls` clears every legacy TLS step.
- The blocked step's inspector explains the gate and hosts the repair panel; repairs need a clean editor.

### FIX4: one readiness story

- The import wizard, the queue chip, the editor header, Author Problems, Checks & readiness, the start gate and the repair previews count the same blockers, warnings and review notes for the same script; promotion-only review notes are counted separately. Converter TLS warnings are covered by their gates, configuration references are one script-wide blocker, multi-code gates count per code, and a gated value counts once.
- A repair restamps the preserved fidelity report and replay preflight: archive-level converter blockers are kept, project-secret state never enters the persisted report, a repair never turns a promotable report blocked, and the launch refusal names only code-shaped review codes.
- An editor save of an imported script refreshes its server-owned import evaluation inside the durable write (`UpdateScriptEditorSaveDurable`); the client cannot author it, and project secrets are resolved before the write takes its locks.

### Credential guard and disclosure

- `RetainedImportCredentialContextChange` ignores non-executable preserved review evidence (`source_review`, `review_artifact`, `project_settings` and other review subtrees) and server-stamped dependency codes, while `HasNewProjectedCredential` still covers them. On the real archive a recorded transaction-order list and long-named review codes had pinned every edit "at script"; credentials kept at script level still hold the whole script.
- `fidelity_report.secret_gated` is exempt from masking only when it is a number.
- Draft validation of a saved credential-preserving import uses the debug start's validator only for a caller that may read the saved script (`requestMayReadStoredScript`); everyone else gets strict draft validation.

## Real-data verification

Against a scratch controller built from this branch under a `sandbox-exec` profile that denies outbound connections (so debug never reached the customer's systems): validation read `2 block replay · 1 warning · 3 review notes` (the false `SelectAll` and `web_convert_param` warnings gone), one TLS repair cleared both gates, one **Save draft and open in Visual Editor** saved the repaired draft without reconversion, the editor showed the same single warning, Author Problems and the Problems tab agreed, and debug started and executed until the first outbound request was refused locally. Saved unrepaired, the editor offered **Resolve 2 blockers**, focused the repair, and applied it with the editor updated in place (after round 3 slimmed the apply response under the dashboard's 12 MiB cap). A request edit that the credential guard had refused saved after round 4.

## Review

Nine rounds of independent reviewers read a frozen snapshot (diff plus after-files) with no git; each candidate finding was verified by skeptics before it was fixed, and every fix carries a test shown to fail without it.

| Round | Confirmed | Medium or higher |
| --- | --- | --- |
| 1 | 23 | 7 (repair dropped converter blockers; secret state persisted; config gates no repair clears; nested gates unreachable; fidelity blockers double-counted) |
| 2 | 16 | 1 high, 4 medium (repair turned a promotable report blocked; Kafka/JMS metadata reads; re-validation dropped repairs; merged Author Problems rows; duplicated refusal issue) |
| 3 | 9 + 2 | apply response over the client cap (also found on the real archive); no-op steps read; preview tiles disagreed; a regressed bind test |
| 4 | 8 + 3 | unscrubbed refusal labels; stale wizard preflight; phantom credential blocker; editor closing; the real-archive guard pin and stale review codes |
| 5 | 10 | allowance reset review keys; draft validation disagreed with the debug start |
| 6 | 4 | SOAP and Java HTTP placeholder rule |
| 7 | 9 | Resolve saved unsaved drafts; sequence wrapper reads; stale-target save loop |
| 8 | 8 | draft-validation equality oracle (re-scoped to callers who cannot read the script) |
| 9 | 8 | none: the cycle stopped and its lows were fixed with tests |

## Decisions

- An undeclared `{{param.*}}` blocks (the runtime refuses it); promotion-only review notes are a separate count; Debug is gated only on server-equivalent blockers; one TLS repair clears every legacy TLS step; in-editor repairs need a clean editor and never offer `restore_body`.
- The user approved loosening the credential guard for the real archive; the cause turned out to be non-executable evidence, so no executable credential's protection changed.

## Open

- Converted-path saves still derive their operation identity from the current update targets (pre-existing), and a principal allowed to start a saved script but not to read it can observe credential reconciliation through the start routes (pre-existing; a product decision).
- A repair preview carries the script before and after, so a script over about 6 MiB cannot be previewed in the editor (it says so).
- The six `TestReplayArtifact*` tests fail locally on macOS on the base commit as well; Linux CI runs them.
