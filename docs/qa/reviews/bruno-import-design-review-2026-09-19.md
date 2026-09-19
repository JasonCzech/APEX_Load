# Bruno import design review — 19 September 2026

Overall health: **Needs significant improvement.** The importer exposes useful safeguards and detailed provenance, but the operator must reconcile conflicting readiness messages, technical diagnostics, and disconnected repair surfaces.

## Scope and evidence

Reviewed the running Docker dashboard at localhost:8080 in Chrome, using a fresh inspection of `CTA_Collection 6a.zip`. Covered source entry plus all five native import tabs. The archive contains 41 HTTP request YAML files, two collection/folder metadata YAML files, and one ignore file. The UI found 41 requests and generated 41 native candidates: no request-count loss was observed. This is inventory validation, not proof of payload or replay equivalence.

The fresh inspection reported five unconfigured project-secret bindings, 16 unresolved runtime variables, and two review-gated conversions. These categories overlap in affected scripts and must not be added together as unique blocked-script counts. The two review findings concern an after-response hook in `InquireCPNIContactInformation_224.yml` and the composed sensitive header in `InquireAccountProfile_312.yml`. The former source contains response access and `bru.setGlobalEnvVar` constructs; it is not merely an empty hook marker. No source hooks or requests were executed, credentials supplied, findings acknowledged, folders created, or scripts saved.

Screenshots are local, unmodified captures. The normal browser capture is wide; a temporary viewport override produced unreliable scaling and was reset. Responsive compliance is therefore not asserted. This audit evaluates the deployed build, which can differ from current repository source.

## 1. Source entry — Needs improvement (P2)

**Observed:** Before a source is inspected, canonical execution controls expose sandbox, selection, paths, execution settings, tags, delays, and other runtime options. Uploading this YAML archive selects native mode. After inspection, source and execution-profile forms remain above every review tab and consume substantial vertical space.

**Change:** Make source selection the first decision. After detection, show a concise mode recommendation with output consequences: “41 editable native HTTP scripts; 2 conversions need review.” Keep an explicit canonical alternative and explain its runner requirements. Collapse advanced canonical settings. Once inspected, replace the setup forms with a compact source/mode/destination summary and Change controls. Warn clearly when changing source or mode invalidates reviewed work.

**Acceptance:** An operator reaches the next unresolved task without scrolling past the full source form. Both modes remain discoverable; source detection never implies complete native fidelity. Preserve the existing session-expiration notice and explain recovery without suggesting that unsaved work is durable.

## 2. Summary — Poor (P1)

**Observed:** “Fidelity: lossless” and “41 executable items” appear alongside review-gated conversions and missing values. “Credential review is complete” is followed by five unconfigured secrets. Completed sanitization events and required repairs appear together under Security findings, using raw diagnostic identifiers. Long identifiers visibly overlap the adjacent descriptions in the captured build.

**Evidence in code:** `pkg/convert/bruno/inspect.go` initializes the bundle fidelity status to `lossless`. `dashboard/src/components/BrunoImport.tsx` displays that status in the native Summary. Native candidates separately carry fidelity states such as `review_required` in `pkg/convert/bruno/native.go`. Bundle preservation is being presented where operators need conversion readiness.

**Change:** Lead with separate, authoritative statuses for **Save readiness** and **Replay readiness**, plus generated/selected request counts. Label bundle preservation explicitly and use native candidate diagnostics for native conversion fidelity. Rename “executable items” to “requests detected” in this context. Replace the dump with three groups: **Needed to save**, **Needed before replay**, and **Automatically handled**. Every actionable group needs a count and a link that opens the relevant tab and focuses its unresolved item. Collapse handled sanitization details; retain diagnostic codes in expandable technical detail. Use wrapping layouts without fixed code-column collisions.

**Acceptance:** The same unresolved requirement has the same status on every tab. Acknowledgement never changes replay readiness. The supplied archive cannot receive an unqualified native “lossless” claim while its hook remains untranslated.

## 3. Source APIs — Needs improvement (P2)

**Observed:** Pagination, selection, naming, Inspect, and Review are present. The Request column contains only an Inspect button; the visible table does not immediately show method/target. Every visible row says Execution blocked without showing why. Repeated inherited paths dominate rows. The default visible ordering is Source ascending while explanatory copy emphasizes Bruno sequence/path order.

**Change:** Rename to **APIs**. Add search and filters for readiness and review requirements. Show method, redacted target, and short reason/count beside status. Keep naming as an explicit edit action. Collapse inheritance into the request detail view. Provide an explicit Bruno sequence sort and distinguish displayed order from saved ordering. Unify Inspect and Review into one details surface with request, provenance, conversion changes, and linked requirements. Retain selected count across pagination and filtering; state whether Select all covers all candidates or only filtered results.

**Acceptance:** Operators can find either affected API directly, understand its blocking reason without opening every row, and return from details without losing selection, search, or page position.

## 4. Credentials — Fair (P2)

**Observed:** This is the strongest existing organization: credentials are grouped, unresolved bindings appear first, source variables and affected-file counts are visible, and preserved values remain protected. However, the header alternates between groups and “protected occurrences”; individual groups can cover 40 request locations. Provider identifiers such as `encrypted_database,environment_read_only` leak into primary presentation. Long generated reference names and cramped value inputs compete with explanatory copy.

**Change:** Preserve server-verified grouping and redaction. Use clear groups **5 need values before replay** and **19 preserved**; collapse preserved entries by default. Distinguish unique bindings from request occurrences. Offer readable existing-secret selection where supported, with advanced reference editing. Label deferred entry “Configure later — blocks replay.” Replace provider identifiers with human-readable capability guidance only where it affects the available action. Add a direct destination for later secret configuration. Do not merge same-named credentials unless server evidence permits it.

**Acceptance:** The five unresolved bindings can be completed or deliberately deferred without traversing preserved entries. Review-complete and value-configured remain separate states. Standard input sizing, labels, spacing, and visible keyboard focus apply to every binding control.

## 5. Data — Poor (P1)

**Observed:** Required variables and optional repeated values form a long undifferentiated list. A second Preflight requirements list repeats missing names. `$isoTimestamp` appears there but has no matching editable row above, although the instruction says to supply these values above. The archive contains that token in `ManageAutomaticPayments_v234.yml`. The deployed copy claims values apply to every script even when a row reports use in only one or 25 scripts. Inputs and unlabeled-looking selection boxes are visually cramped.

**Change:** Separate **Required runtime inputs** from **Optional substitutions**. Add search, unresolved-only filtering, affected-API links, scope, and value-source choices supported by the engine. Every preflight requirement must lead to a real repair action or a precise explanation of why it cannot be repaired here. Investigate `$isoTimestamp` as a source dynamic-value classification/translation case before asking the operator to enter a static timestamp; do not silently invent equivalent behavior. Consolidate duplicate requirement lists into one authoritative queue. Clearly identify whether a value applies only to detected occurrences or to all selected scripts.

**Repository caveat:** `SharedImportValues.tsx` already contains newer scope copy and optional-substitution guidance than the deployed screen. Reuse applicable improvements, but make them mode-aware: sending Bruno operators to a nonexistent “Review queue” is not a complete integration.

**Acceptance:** All 16 currently reported requirements have a reachable resolution or explicit supported fallback. Required and optional counts remain independent. Setting a value updates readiness immediately, and partial selection recalculates affected scripts. Verify dynamic-value semantics separately before removing any blocker.

## 6. Import APIs — Needs improvement (P1)

**Observed:** The individual review cards explain what was found, why replay is blocked, and what is required. They correctly state that acknowledgement only allows saving an incomplete draft. However, remedies are prose instructions, with no direct repair navigation. Destination selection lives far above. The final action foregrounds five secrets while unresolved variables and conversion limitations also remain.

**Change:** Rename to **Review & save**. Group review findings by required action and link to the exact source/request or credential control. Preserve the explicit distinction between acknowledgement and repair. Put destination, selected count, save blockers, and replay blockers in one final checklist with direct links. Use a stable primary label such as **Save 41 drafts**, with separate readiness text rather than putting only one blocker category into the button label. Explain whether Continue importing resets this collection and what happens after saving. Provide a post-save route to the saved batch and guided single-iteration debug when ready.

**Acceptance:** The operator can resolve every save prerequisite from this screen without hunting. Acknowledged incomplete drafts remain blocked from replay. Selection changes recalculate the checklist; save success provides a durable batch destination and remaining requirements. Save and post-save behavior were not exercised in this audit.

## Cross-tab implementation priorities

1. **P1 — Trust and correctness:** Separate bundle/native fidelity and save/replay readiness; reconcile variable requirements with editable controls, especially `$isoTimestamp`; use one source of truth for counts and statuses.
2. **P1 — Connected repair:** Link Summary, API details, credentials, data, and final review to the same requirement. Keep acknowledgements distinct from repairs.
3. **P2 — Reduce navigation cost:** Collapse inspected setup, persist review navigation, add actionable tab counts, and keep a compact readiness summary visible.
4. **P2 — Scannability and accessibility:** Repair diagnostic overlap, normalize form sizing, constrain text measures, add useful filtering, and group required versus preserved/optional content.

Keyboard ArrowRight successfully moved selection from Summary to Source APIs and updated roving tab stops. Retain this behavior. Remaining accessibility verification should include input labels/descriptions, focus arrival after linked repairs, focus visibility, status announcements after edits, screen-reader reading order, contrast measurement, and narrow-layout testing. Screenshots establish overlap and density risks; they do not establish a complete WCAG result.

## Screenshot evidence

### Source entry

![Source entry](/Users/jasonczech/.codex/visualizations/2026/09/19/01a0b9de-7e8c-7221-a5ac-e2ae5f564188/bruno-import-audit/01-entry.png)

### Summary: conflicting fidelity and readiness; overlapping diagnostic labels

![Summary: conflicting fidelity and readiness; overlapping diagnostic labels](/Users/jasonczech/.codex/visualizations/2026/09/19/01a0b9de-7e8c-7221-a5ac-e2ae5f564188/bruno-import-audit/02-summary.png)

### Source APIs: selection and opaque blocking status

![Source APIs: selection and opaque blocking status](/Users/jasonczech/.codex/visualizations/2026/09/19/01a0b9de-7e8c-7221-a5ac-e2ae5f564188/bruno-import-audit/03-source-apis.png)

### Credentials: grouped bindings and cramped form controls

![Credentials: grouped bindings and cramped form controls](/Users/jasonczech/.codex/visualizations/2026/09/19/01a0b9de-7e8c-7221-a5ac-e2ae5f564188/bruno-import-audit/04-credentials.png)

### Data: required values and optional substitutions

![Data: required values and optional substitutions](/Users/jasonczech/.codex/visualizations/2026/09/19/01a0b9de-7e8c-7221-a5ac-e2ae5f564188/bruno-import-audit/05-data.png)

### Import APIs: explanatory review gates without connected repairs

![Import APIs: explanatory review gates without connected repairs](/Users/jasonczech/.codex/visualizations/2026/09/19/01a0b9de-7e8c-7221-a5ac-e2ae5f564188/bruno-import-audit/06-import-apis.png)

