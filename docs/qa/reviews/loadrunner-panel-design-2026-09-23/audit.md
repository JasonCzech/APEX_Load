# LoadRunner import review — design consistency audit

> Historical baseline from the older Docker dashboard. The recommendations have since been implemented against current working-tree source. See [implementation and current verification](implementation.md); these original screenshots are not the current dashboard.

Reviewed 23 September 2026 against the populated local Docker application at port 8080, using the existing `acc_wls_reg_1.zip` review. Theme: Solarized Dark. Capture size: 2048 × 1163. All screenshots were captured and inspected during this audit.

**Verdict: the outer panel is consistent; the six tab interiors are not yet a coherent workspace.** The shared palette, border treatment, top-level tabs, and persistent save row are sound. Most friction comes from inconsistent content hierarchy, unexplained count scopes, and several overlapping ways to make or review changes.

This is an evaluation and implementation recommendation. No application code, draft values, credentials, or saved scripts were changed.

## Highest-impact findings

### 1. High — tab badges do not lead to the issues they advertise

Evidence: steps 1–4.

- Data & parameters has a **13** badge, but opens a table of **6** imported parameters with **Needs attention (0)**. The 13 missing-value/correlation issues are in Review. The return link appears after the entire parameter table, so the mismatch is not resolved at the point of entry.
- Credentials shows **2** on its tab, **4 unresolved credential requirements**, **12 credential groups**, and **1 binding needing setup**. These may all be correct at different levels of aggregation; the interface makes users reconcile them.
- Runtime settings shows **1** on the tab, **6 Requires decision** overall, and **5** in the current HTTP category. These represent different scopes, without an equally prominent explanation of each scope.

**Recommendation:** define tab badges consistently as unresolved grouped replay issues. Put an actionable sentence at the top of each relevant tab: “13 replay issues · Review missing values and correlations,” or “2 replay issues · 1 project binding needs setup.” Keep inventory totals in their tables. Label runtime totals “All categories” and the section results “HTTP — 5 settings.” A badge must either open the matching filtered content or have a conspicuous link to it. Do not force different data models to have identical counts.

### 2. High — navigation and repair ownership overlap

Evidence: Credentials, Runtime, and the Source Mapping → Review handoff in step 6.

Credentials has both **Back to issue queue** and **View remaining execution requirements**, in addition to the Review tab. Source inspection correctly links to a specific TLS issue, but Review then offers a TLS repair while Runtime settings exposes minimum/maximum TLS configuration. A user cannot tell from the presentation whether these paths have identical effects on migration acknowledgment and readiness.

**Recommendation:** keep Review as the issue index and use a single repair component/state contract for each issue type. Both Review and the relevant domain tab may open that component, but should share preview, apply, and readiness refresh behavior. Retain source-to-issue deep links. Replace the two generic Credentials return actions with one consistently positioned “Review issues” link, or a contextual “Back to selected issue” link when arriving from an issue. Preserve migration acknowledgment and protected-draft validation; a raw setting change must not silently stand in for those operations.

The audit establishes overlapping user-facing controls, not that their backend effects are identical.

### 3. Medium — headers, spacing, and usable width change by tab

Evidence: all six primary screenshots.

Review uses a large nested card with a sentence-case heading. Credentials adds a separate introduction card followed by an uppercase section heading. Data and Runtime use larger sentence-case headings. DSL Preview and Source Mapping use small uppercase headings; DSL's heading and download explanation are flush against the panel edge while nearby controls are inset. “Back to issue queue” is above most tabs' content but below Data's inventory.

Runtime's content is capped at 80rem, leaving a substantial unused right-hand area at this viewport. Data, Credentials, and the code comparison use the available width. The Runtime cap can be a valid readability choice, but currently reads as an unrelated layout rule.

**Recommendation:** give every tab one shared header with a title, one-line purpose, optional contextual return link, and local actions. Use one content inset (the existing 20px pattern is a suitable starting point), one heading scale, and one toolbar alignment. Keep prose at a readable width while allowing tables and code to fill the workspace. Standardize sentence case, including “DSL preview” and “Source mapping.” Keep distinct layouts where the task warrants them: a comparison editor should not be forced into a credential table.

### 4. Medium — filters look and behave like unrelated components

Evidence: steps 1–4 and 6.

Review has a very wide search with a right-aligned Show dropdown. Credentials has a short search, an inline state dropdown, and a separate review checkbox. Data has compact type/source filters; Runtime has rectangular checkbox status filters above a small search and category rail. Source Mapping uses pill-shaped status controls and very wide file/search fields. Static credential summary pills resemble interactive mapping filters.

**Recommendation:** share a toolbar shell: labeled search, scoped filters, result count, then secondary actions. Use one appearance for multi-select status filters and another for static totals. Make local differences explicit (“Search parameters,” “Search mappings”) without changing control height, label style, and spacing. Avoid a short “Name or source column” field that visibly clips its placeholder at this desktop size.

### 5. Medium — Data contains three change workflows with different action rules

Evidence: step 3 and its supplementary screenshots.

The parameter inventory has row editing; Optional substitutions uses “Preview substitutions”; Parameterization review uses “Apply Selected (0).” Their headings, typography, and action positions vary. Optional substitutions also repeats “Used in 478 requests” in both the summary and the helper sentence. Allocation repeats selection/update and exhaustion information already present in Parameters, then pushes its distinctive preview further down the page.

**Recommendation:** distinguish the jobs explicitly: **Parameters**, **Data sources**, **Allocation preview**, and an expandable **Suggested changes** area. Within suggestions, label environment substitution separately from parameter creation. Share the selection → preview → apply interaction and result feedback, while retaining each operation's semantics. Make Allocation primarily a simulation view with a compact rule summary and links that open the relevant parameter editor. Keep the read-only source inventory; it answers a different question from parameter editing.

### 6. Medium — code inspection repeats useful capability with inconsistent controls

Evidence: steps 5–6.

DSL Preview offers section/action navigation, a visible word-wrap control, and “Focus preview.” Source Mapping offers a generated-DSL pane, “Focus destination,” and word wrap hidden under “View options & export.” It also supports an original/current destination choice. These are related inspection capabilities, but their controls and wording diverge. Several rows of guidance and controls precede the code; in the Source Mapping overview the actual comparison begins near the bottom of the viewport.

**Recommendation:** retain both tabs for now. DSL Preview is the whole-draft inspection view; Source Mapping is the provenance comparison. Share code-pane controls and wording: “Word wrap,” “Expand pane,” “Copy,” with clearly scoped export actions. Move the preview-limits explanation into one compact notice with expandable details. Keep “Download protected review JSON” distinct from “Export filtered mappings”: these are different artifacts, not duplicate exports.

### 7. Medium — visual and accessible status communication need alignment

Evidence: the visible badge counts in all screenshots and the accessibility tree during tab navigation; source confirmation below.

The top-level tabs expose their names and selected state, but their numeric badges are excluded from accessible names. Runtime status checkboxes similarly expose their status name without the adjacent count. The information is visible elsewhere, but users navigating controls do not receive the same immediate summary. Small, muted labels and outlined actions also appear faint in Solarized Dark; no contrast-ratio conclusion is claimed without measurement.

**Recommendation:** expose counts with explicit meaning, for example “Data & parameters, 13 unresolved issues” and “Requires decision, 6 settings across all categories.” Preserve roving tab focus and active-state indicators. Measure text and control contrast in each supported theme and use the established semantic text tokens. Single-selection Data views should use the shared tab pattern rather than looking like independent toggle buttons.

## Tab-by-tab evidence

### 1. Review — usable, needs clearer hierarchy

Strengths: grouped issue queue, affected-location counts, a visible next-issue action, and a useful selected-issue pane. The global and local issue totals repeat. Long review content scrolls the tab strip away. The captured initial state leaves a large detail pane empty until selection; the linked TLS issue shows that the detail pane becomes useful once a specific task is chosen.

![Review tab](01-review.jpg)

The source-to-issue link selected the intended TLS repair. It did not apply a repair.

![Linked TLS repair in Review](01b-linked-issue.jpg)

### 2. Credentials — needs consolidation

Strengths: secret values remain hidden, affected locations are grouped, and destination/current state are separated. The multiple count scopes, duplicate return links, and two introductory/header layers make the one binding needing setup harder to distinguish from preserved values.

![Credentials tab](02-credentials.jpg)

### 3. Data & parameters — greatest information-architecture friction

Strengths: a useful compact parameter table, explicit sample-value caveat, source evidence, and distinct source/allocation views. The tab badge and “Needs attention” filter describe different populations. Optional workflows below the table follow older card patterns and separate apply conventions.

![Data and parameters tab](03-data-parameters.jpg)

The read-only Data sources inventory is worth keeping. It reports source files, row availability, columns, and consuming parameters.

![Data sources subview](03b-data-sources.jpg)

Allocation repeats much of the parameter table before presenting its distinctive simulation tool. Prioritize the preview and make rule edits directly reachable.

![Allocation subview](03c-allocation.jpg)

The return-to-queue link appears between the inventory and optional substitutions, rather than in the shared tab header.

![Optional substitutions position](03d-optional-substitutions.jpg)

The two optional change sections use different confirmation patterns and action placements.

![Substitution and parameterization workflows](03e-parameterization.jpg)

### 4. Runtime settings — strong comparison model, inconsistent framing

Strengths: source/current comparison, per-row evidence, category navigation, and separation of mapped versus preserved behavior. Counts need their scope labeled. The narrow capped workspace, raw source category names such as FILTERS/ModemSpeed, and adjacent Log/Logging categories interrupt the product's terminology.

Normalize display categories such as Log/Logging where their meaning warrants it, retaining original source-section names in evidence. Do not discard source provenance while simplifying navigation.

![Runtime settings tab](04-runtime.jpg)

### 5. DSL Preview — useful capability, needs layout cleanup

Strengths: read-only labeling, action search, section selection, word wrap, and explicit truncation disclosure. The heading scale, edge alignment, and download explanation differ from the neighboring tabs. A shared code-inspection header and compact notice would give more room to the JSON.

![DSL Preview tab](05-dsl-preview.jpg)

### 6. Source Mapping — valuable specialist view, too much before the code

Strengths: source/destination comparison, coverage filters, explicit source-preview limitations, original/current destination choices, and a working link to the related repair. Mapping-review counts describe provenance review and should remain distinct from replay issue counts. Retain that distinction in labels rather than merging the two systems.

![Source Mapping tab](06-source-mapping.jpg)

Expanded display controls and the actual comparison show the useful core experience. Consolidate the surrounding controls, and keep the common tab navigation available during long inspection sessions.

![Source Mapping comparison and controls](06b-source-comparison.jpg)

## Duplicate-functionality decisions

| Area | Assessment | Recommended treatment |
| --- | --- | --- |
| Review tab + Back to issue queue + View remaining execution requirements | Confirmed redundant generic navigation; both buttons call the same overview selection | Keep one contextual return link in a shared header |
| Review TLS repair + Runtime TLS configuration | Overlapping user task; backend equivalence not established | Share a canonical repair flow and readiness refresh; preserve explicit migration acknowledgment |
| Parameters versus Allocation rule table | Confirmed repeated presentation; allocation preview is distinct | Keep preview, compact the repeated rules, deep-link to edits |
| Parameter editing / optional substitutions / parameterization suggestions | Adjacent operations with inconsistent workflow; not exact functional duplicates | Separate purposes, share preview/apply conventions and feedback |
| DSL Preview versus generated DSL in Source Mapping | Intentional overlap serving different inspection tasks | Retain both; share code controls and selected-action navigation where appropriate |
| Protected JSON download versus mapping export | Different output contracts | Retain both and explicitly label scope and protection |
| Current issue queue versus original conversion diagnostics | Current actionable state versus historical evidence | Keep original diagnostics collapsed and clearly marked as original/static |

## Implementation starting points

- `dashboard/src/components/EnterpriseArchiveImport.tsx:4402`: six-tab assembly and issue badges. At 4453 and 4527, the two generic return actions both select the overview. The shared return link is rendered after the parameter inventory because the latter is injected earlier by ScriptImportReview.
- `dashboard/src/components/ScriptImportReview.tsx:932`: shared status/save header. At 1002 the non-tabbed parameter inventory precedes children; at 1106 the data extras mount separate substitution and parameterization components.
- `dashboard/src/components/ScriptImportReview.css:73`: legacy uppercase 13px section headings and credential framing.
- `dashboard/src/components/ImportSettingsTables.css:33`: newer 1rem workspace padding. At 44 Runtime alone gets an 80rem maximum width; at 40 the settings toolbar defines another control pattern.
- `dashboard/src/components/ImportParameterInventory.tsx:62`: the Data subviews and their shared table content. Keep Parameters as the editing owner while promoting Allocation preview's distinctive value.
- `dashboard/src/components/ImportRuntimeSettingsTable.tsx:53`: runtime heading, global status totals, search, category rail, and result groups.
- `dashboard/src/components/ImportGuidedRepair.tsx:179`: guided TLS selection and repair preview.

Suggested implementation order: fix issue scope and navigation first; extract shared tab header/toolbar and code-pane controls second; then consolidate repair entry points and simplify Data's optional workflows. Add workflow coverage for badge-to-issue navigation, shared counts, pending edits, source-to-repair links, and keyboard names. Run the repository's dashboard test cycle and verify the result visually after implementation.

## Evidence limits

The audit covers one populated HTTP archive, six tabs, three Data subviews, optional data sections, the Source Mapping display options, and its TLS issue handoff. Right-arrow keyboard navigation from Data & parameters to Runtime settings was verified. The browser was returned to Runtime settings, the originally selected tab.

No saves, applies, revalidations, downloads, credential edits, or replay runs were performed. Save/batch-save equivalence and backend repair equivalence were not tested. Empty/error states, alternate protocols, multi-archive states, other themes, narrow viewports, screen-reader operation, and measured color contrast remain unverified. No application tests were run because this pass only created audit evidence and recommendations. Repository source was used to corroborate visible patterns; the running Docker build was not rebuilt or asserted to match the checkout byte for byte.
