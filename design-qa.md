# Closed VUser Guided Worksheet — Design QA

- Source visual truth: `/Users/jasonczech/.codex/generated_images/01a05eb5-16da-7340-990d-d53e9fa49ed7/exec-0517e758-2b13-41ff-895b-3bcf0a8af76d.png`
- Implementation: `http://127.0.0.1:4000/#/tests?view=builder`
- Browser capture: `/Users/jasonczech/.codex/visualizations/2026/09/01/01a05eb5-16da-7340-990d-d53e9fa49ed7/closed-vuser-desktop.png`
- Combined source/implementation comparison: `/Users/jasonczech/.codex/visualizations/2026/09/01/01a05eb5-16da-7340-990d-d53e9fa49ed7/closed-vuser-comparison.png`
- Verified state: 5 VUsers, 2-minute continuous ramp-up, 65-minute sustained load, enabled 2-minute continuous ramp-down, scenario scope, early stop disabled, phase table and sticky live preview visible.

## Findings

No actionable P0, P1, or P2 visual differences remain.

- The implementation preserves the selected worksheet hierarchy: three numbered sections on the left and a sticky live model preview on the right.
- The Closed VUser-specific phase controls replace the Stepped Ramp range/increment controls while retaining the same spacing, hierarchy, and APEX Load tokens.
- The preview correctly draws a continuous linear ramp-up, flat sustained segment, and optional continuous ramp-down. It does not render the closed profile as a staircase.
- The existing Scenario Builder shell, six-step navigation, theme controls, and Pre-Run Assurance panel remain product constraints and intentionally differ from the standalone visual reference.

## Required Fidelity Surfaces

- Typography: existing APEX Load type scale and weights remain consistent across worksheet headings, section titles, labels, helper copy, computed totals, and preview metadata.
- Layout: desktop retains the guided worksheet/sticky-preview split. Existing responsive rules collapse the workspace, phase fields, scope options, and protection controls to one column at the established breakpoints.
- Color and states: existing dark-theme tokens and purple accent are used for selected scope, enabled ramp-down, totals, chart series, phase metadata, and primary actions.
- Controls: scenario/group scope has radio semantics; ramp-down and early-stop controls are labeled checkboxes; duration controls retain keyboard-operable number/unit inputs.
- Assets: the implementation reuses the product's existing chart and icon assets. No placeholder, CSS-art, emoji, or custom SVG substitute was introduced.

## Interaction Evidence

- First enabling ramp-down seeds its duration from the current ramp-up value.
- Disabling and re-enabling ramp-down retains the operator's last ramp-down value.
- Phase edits update total elapsed time, generated stages, chart data, and phase rows immediately.
- Scenario scope repeats the complete profile per group; group scope retains independent definitions and derives scenario duration from the longest timeline.
- Shorter group timelines fall to zero at their endpoint rather than extending the hold.
- Goal Oriented is absent from the guided Closed VUser flow.
- Script shakeout, resource baseline, continue-on-error, and snapshot-on-error controls remain immediately after the worksheet.
- Browser logs contain no warnings or errors from the implemented flow.

## Comparison Notes

The combined board compares the selected guided worksheet reference and the browser-rendered Closed VUser implementation in one image. The implementation is denser because it runs inside the full Scenario Builder shell, but the dominant composition, section rhythm, option treatments, protection controls, primary action, chart, and detail table align with the selected direction.

## Implementation Checklist

- [x] Guided Target load, Run phases, and Protect the run sections.
- [x] Continuous ramp-up/hold/optional ramp-down preview.
- [x] Scenario and group scope behavior.
- [x] Preserved Pre-Run Assurance controls.
- [x] Immediate validation and preview updates.
- [x] Responsive collapse rules retained for desktop and narrow layouts.
- [x] Browser interaction and console verification complete.

final result: passed

---

# Post-test reporting — Design QA, 2026-09-08

Source truth: `docs/qa/reviews/post-test-reporting-2026-09-08/AUDIT.md` and its original captures `01-results-library.png`, `04-composer.png`, `06-run-dashboard.png`, `07-action-summary.png`. This is an intentional redesign of that audited flow, not a pixel-identical clone. The old app shell, APEX assets and theme tokens remain the visual reference.

Implementation: `http://localhost:4000/#/report/e484fd1a-1ee3-4677-87fb-35d0e42ca221`; screenshots in the same review directory: `09-implemented-summary.jpg`, `10-implemented-preview.jpg`, `11-implemented-no-samples.jpg`, `12-implemented-diagnostics.jpg`, `13-implemented-evidence.jpg`, `14-implemented-library.jpg`.

## Comparison conditions and evidence

Original and implementation captures are 923 × 987 pixels at a 923 × 987 CSS viewport, 1× capture density; no resampling was used. The completed mixed-user run, legacy release and dark application theme match. The report preview uses its existing light document theme. Baseline and empty-population states were also exercised. Active-run chips and build identity differ because the existing controller continued running between audit and implementation. The normal 1280 × 720 desktop viewport was checked and temporary overrides were reset.

Full-view source/implementation pairs were provided together in the same tool comparison inputs: original execution snapshot versus new Summary; original composer versus revised preview; original library versus revised library. This was a paired-image review, not a generated composite. Focused inspection used the legible 1× title/toolbar, KPI, population and library row regions in those captures, plus the diagnostics table capture. Those regions were readable without an additional crop.

## Findings and iteration history

No actionable P0/P1/P2 findings remain in the reviewed reporting surfaces.

- P1: The preview toolbar was intercepted by the application header. Rendering the composer through a body portal fixed the stacking context; preview/edit/close were clicked successfully. Evidence: `10-implemented-preview.jpg` and U16 browser workflow.
- P1: Empty populations displayed trend graphics despite unavailable KPIs. Positive sample counts now gate those graphics and baseline overlays. Evidence: `11-implemented-no-samples.jpg` and population/KPI unit tests.
- P2: Diagnostics lost inherited table styling and overflowed its panel. The archive component now imports its stylesheet; result tables scroll within their panels and filters wrap. Evidence: `12-implemented-diagnostics.jpg`.
- P2: Collapsed comparison controls obscured the active baseline context. A persistent baseline caption now remains, with context warnings independent of expansion; covered by RunReportPage tests.
- P2: The sticky library action initially allowed underlying text to show through, then covered part of the run identity at the audit width. An opaque theme surface, narrower leading columns, bounded name wrapping, unbroken Copy label and compact lifecycle badge now retain identity/status/action together. Evidence: `14-implemented-library.jpg`; U16 asserts opacity and non-overlapping name/action bounds at 923px.

## Required fidelity surfaces

- Typography: Existing application sans-serif and document heading/body hierarchy retained. Titles wrap fully; KPI values do not split. Table labels and reference-copy text were inspected at 1×. No replacement font was introduced.
- Spacing/layout: Four sections replace the sprawling archive navigation. Header actions wrap; report pages retain original document margins. Dense evidence tables scroll locally. Longer names wrap without covering primary actions.
- Colors/tokens: Existing dark surfaces, purple actions and status colors retained. Unassessed/unavailable values are not green success indicators. Preview definition labels use a darker readable foreground. Sticky cells use the opaque secondary-surface token.
- Image quality/assets: Existing APEX application/report logos and icon components are retained, with no generated or handcrafted substitutes. No new raster artwork was required.
- Copy/content: Computed acceptance, execution, evidence completeness, population and analyst assessment are distinct. Small nonzero errors and missing samples remain honest. Prior-run release trends are identified separately from a chosen comparison baseline. No audit or implementation instructions appear in product copy.

## Interaction verification and limits

Summary/Performance/Diagnostics/Evidence navigation, baseline selection/clearing, zero-sample comparison, selected action filters, preview/edit/close and library entry were exercised in the browser. The workflow suite also checks report downloads, annotation persistence/navigation guards and local-auth evidence retrieval. Browser error logs were empty during the final manual pass. Unit tests cover completed/live/missing-archive states and metric edge cases. This is not a full screen-reader or WCAG certification, nor a comprehensive mobile redesign.

The existing port 8080 controller was not restarted. Revised backend exports and aggregation were verified using fresh test controllers; the local preview therefore retains any old server-generated or saved narrative text until the normal controller is rebuilt/restarted. See `docs/qa/reviews/post-test-reporting-2026-09-08/IMPLEMENTATION.md` for the mapping of all audit recommendations.

## Implementation checklist

- [x] Consolidate navigation, export and completed-run entry.
- [x] Repair acceptance, population, baseline and missing-data semantics.
- [x] Review rendered views against original captures and fix the findings above.
- [x] Preserve unrelated workspace changes and existing saved annotations.

Final result: passed

Final validation: `go test ./...` passed; `cd dashboard && npm run test:cycle` passed with 356 unit-test files / 3,938 tests, 65 dev-admin browser workflows and 8 local-auth browser workflows, including type checking, lint and a fresh embedded build. Final cycle log: `/tmp/report-cycle-verified.log`. Wide desktop evidence: `docs/qa/reviews/post-test-reporting-2026-09-08/15-implemented-summary-desktop.jpg` (1280 × 720). `git diff --check` passed.

---

# Application-wide UX consistency — 2026-09-08

Source: `docs/qa/reviews/ux-consistency-2026-09-08/AUDIT.md`, its 21 source captures, and `UX_CONSISTENCY_SPEC.md`. Implementation: the dashboard at `http://localhost:4000/`, with the existing development controller. This is an intentional consistency migration of the source product, not a pixel-identical clone of its defects.

## Comparison record

Reference and rendered screenshots were opened together in the same comparison inputs. Primary matched comparisons use 923 × 987 CSS pixels/pixels in the in-app browser; wide Settings uses 1440 × 1000. Full-view images were legible at their native dimensions, so additional cropped images were unnecessary. The existing APEX logo and icon assets were retained.

| Surface | Reference | Post-fix evidence | Result |
|---|---|---|---|
| Notes workbench | `18-notes-dialog.jpg` | `after-notes.jpg`, `after-confirmation.jpg` | Title/body/footer are above app chrome; nesting and cancellation preserve the editor. |
| Compact creation | `08-project-dialog.jpg` | `after-project.jpg`, `after-project-light-wide.jpg` | Shared corners, title and actions; Create project is explicit; Escape restores the scope picker. |
| Dashboard | `01-dashboard.jpg` | `after-dashboard.jpg`, `after-dashboard-wide.jpg` | Activity comes first at narrow width; desktop snapshot retains its context and named control. |
| Script Library | `04-script-library.jpg` | `after-scripts.jpg` | Review details are disclosed; names and Edit remain readable with More for secondary actions. |
| Script outline | `06-editor-collapsed.jpg` | `after-editor-collapsed.jpg` | Collapsed rail retains Outline and a directional control. |
| Access | `07-access.jpg` | `after-access-collapsed.jpg` | Named reopen trigger replaces Hide/Show Details; focus moves with the trigger; arrow navigation works. |
| Monitoring | `09-monitoring.jpg` | `after-monitoring.jpg` | All navigation is visible; advisory no longer shares constrained space with tabs. |
| Schedules | `10-schedules.jpg` | `after-schedules.jpg` | Vertical dates and readable events replace compressed seven-column cells at narrow width. |
| Repository | `15-repository.jpg` | `after-repository.jpg` | Filters wrap; effective catalog scope and primary actions are visible. |
| API Guide | `16-api-guide.jpg` | `after-api-guide.jpg` | All page tabs are visible across two rows. |
| Command Center | `17-command-center.jpg` | `after-command-center.jpg` | Operator view names and truthful waiting/running language replace implementation versions and misleading status copy. |
| Settings | `03-settings.jpg`, `21-settings-wide.jpg` | `after-settings-runtime.jpg`, `after-settings-wide.jpg`, `after-preferences-light-wide.jpg` | Shared navigation trigger, labeled inline fields and a quiet clean-state status. |

Paths above are relative to `docs/qa/reviews/ux-consistency-2026-09-08/`. The Access comparison deliberately changes expanded to collapsed state to verify the replacement affordance. The compact project dialog's underlying route differs; its matched modal geometry/theme is the comparison target. Live controller counters and update timestamps can change between captures.

## Iterations and fidelity checks

The first implementation pass remained blocked on narrow script-name compression, Monitoring navigation clipping, and Repository filter overflow. The corrective pass added prioritized table columns with an explicit reveal control, wrapped Monitoring navigation/advisory layout, and responsive Repository grid columns. Each was captured and compared again after the correction.

- Typography: the existing font family and hierarchy are retained; dialog titles and field labels use a shared scale. Script identities no longer collapse into single-character columns.
- Spacing/layout: consistent dialog gutters and footer wrapping; named panel rails; main dashboard task precedes supporting context at narrow width. Workbenches and document canvases retain purposeful differences.
- Colors/tokens: shared surfaces inherit theme variables. Dark and light were reviewed; disabled controls and clean save state no longer look like available primary actions. Automated first-use accessibility checks cover both themes.
- Image quality: no generated or replacement imagery was introduced; the existing logo remains sharp and proportionate.
- Copy/content: explicit creation verbs, operator view names, loading/unavailable distinction, and domain-catalog scope. Existing acceptance/evidence population distinctions remain intact.
- Behavior: real body-level portals and modal isolation, nested Escape, cancel/discard, focus return, panel state, and tab keyboard navigation have dedicated tests and browser verification.

The review does not claim exhaustive screen-reader certification, every alternate color theme, every secret-bearing form, or phone-sized workflow support. Existing unavailable secret-readiness data comes from the current controller and remains explicitly unavailable. The existing controller was not restarted and no real run was launched or altered for visual QA. Temporary theme/viewport changes were restored; the unsaved review note was discarded without saving.

Final result: passed. No open P0/P1/P2 visual differences remain in the reviewed comparisons. The complete test cycle passed: 3,946 unit tests, 66 dev-admin browser workflows, and 8 local-auth browser workflows, plus type checking, lint, and the embedded build. See `docs/qa/reviews/ux-consistency-2026-09-08/VALIDATION.md`.


## Original-scope completion review — September 8, 2026

This follow-up closes the structural items that were only partially addressed in the earlier application-wide pass. See `docs/qa/reviews/ux-consistency-2026-09-08/REMAINING_WORK.md` for the item-by-item mapping and deliberate domain differences.

### Evidence and comparisons

- Original scenario library `13-scenario-library.jpg` and `completion-scenarios.jpg`, 923 × 987: compact creation beside Source/Refresh; search first; optional release filtering disclosed. Subsequent common spacing refinement is included in the final matrix.
- Original builder `14-scenario-builder.jpg` and `completion-builder.jpg`, 923 × 987: shared navigation geometry, one readiness disclosure, persistent footer, Save draft separated from Next, and explicit no-launch save explanation.
- Prior script library `after-scripts.jpg`, initial `completion-scripts.jpg`, then `completion-scripts-fixed.jpg`, 923 × 987: initial comparison found excess vertical spacing and an empty disabled bulk-action bar. The revised capture removes those issues and brings rows back into the first screen.
- Repository `after-repository.jpg` and `completion-repository.jpg`, 923 × 987: effective catalog scope retained, optional filters disclosed, common search/refresh behavior and visible primary asset actions.
- Command Center `after-command-center.jpg` and `completion-command-center.jpg`, 923 × 987: operations is the default and analysis is an explicit tool instead of a competing view-generation choice.
- Results `19-results-library.jpg` and `completion-results.jpg`, 923 × 987: search-first controls, visible active-filter indicator, comparison guidance before selection, and more rows visible without disabled bulk actions.
- Access `07-access.jpg` and `completion-access-solarized-light.jpg`, 923 × 987: the theme intentionally differs; hierarchy, typography and panel placement were compared, while colors were assessed against the existing Solarized Light tokens. Inline email validation was checked separately through the browser accessibility tree and DOM associations, then the temporary entry was cleared/discarded.

Reference and revised images for each comparison were emitted together. The primary-area theme matrix covers 11 routes × 5 themes × 2 widths (923 × 1000 and 1440 × 1000), with full-resolution screenshots retained in `theme-matrix/`. Cropped contact sheets aid cross-page layout/color review; full-size captures and manual comparisons above support typography, controls and focus review. Dataset differences are intentional: matrix workspaces are disposable seeded workspaces with one script and empty result/scenario lists, while manual comparisons use the existing populated local workspace.

### Review/fix findings

The first iteration was blocked on excessive library spacing, inactive bulk actions and the missing border token. These were corrected and compared again. A later behavior check found onboarding incorrectly becoming dirty after catalog hydration; its baseline is now stable and both the regression test and manual navigation confirm the fix. Results received the same selection treatment after the matrix review. Early transition-state screenshots are not accepted as final evidence; final capture waits for the route heading/loading state and disables animations.

- Fonts/typography: existing fonts and type scale retained; captions and labels remain readable in full-size checks; toolbar headings are secondary to page titles.
- Spacing/layout: common search/filter/action order, visible narrow navigation and table actions, consistent selection containers, and stable builder progression.
- Colors/tokens: all five concrete themes reviewed. Missing border token corrected to `--border`; disabled states remain intentionally subdued. No new palette introduced.
- Image quality: existing brand asset retained; no generated imagery, stretched logos or replacement illustrations.
- Copy/content: Save draft versus Next, explicit analysis entry/return, active-filter indication, truthful empty/loading/failure states and effective catalog scope.

Final result: passed. Final type checking, lint and embedded build passed, along with 3,953 unit tests, 76 dev-admin workflows and 8 local-auth workflows. All 110 matrix screenshots were refreshed after the fixes; Light Command Center and final Solarized Light/Monokai Results were reopened at full resolution. Manual Results selection exposed the correct action availability and disappeared when deselected. The untouched onboarding form navigated to Settings with no dialog. Preview restored to Dashboard, Dark theme, normal viewport. No original-scope work items remain open; no exhaustive screen-reader or phone-width certification is claimed.
