# Load impact heatmap operator-experience review

Reviewed 2026-09-14. Verdict: retain 2D as the default operational view; treat 3D as optional exploration until its scale, motion, labeling, and interaction controls are explicit. The most consequential improvements concern interpretation and investigation, followed by presentation polish.

## Scope and evidence

Current dashboard source was served by Vite against the existing local controller binary and an isolated SQLite backup. Schedules were disabled and nonterminal tests marked complete only in that disposable backup to prevent executions. No application implementation was edited or tests launched. Browser evidence used the archived `web wiki` run, `LOC-RUN-20260730-001`: 275 interval samples, 15 available metrics, one observed load level of 10 VUsers. Its legacy interval population differs from the request/action KPI population, as the report itself explains.

The audit followed Results → Performance → Load impact, switched 2D/3D, selected P95, and opened/closed configuration. Screenshots were captured during this review, saved, reopened, and inspected. They are viewport captures, not complete-page captures. The browser's native surface changed width during the review; the accepted 2D capture is 1422px wide and the remaining captures are 690px wide. These are not same-viewport visual comparisons. Temporary viewport overrides produced capture scaling artifacts and were reset; those captures were replaced.

| Step | Task | Health |
| --- | --- | --- |
| 1 | Find and read the 2D map | Usable numeric comparison; unclear color meaning and excessive surrounding chrome |
| 2 | Switch to 3D and inspect the retained selection | Renders and preserves selection; difficult labels, automatic motion, unexplained height |
| 3 | Select P95 and inspect samples | Details work; separated from selection, wide table, duplicate P95 column |
| 4 | Open configuration | Logical metric groups and focus restoration; translucent panel damages readability |

## Prioritized recommendations

### P1 — Explain what color and height measure

Evidence: steps 1–2. In the single-load run, ART 476ms, P95 514ms, and Error 0% have the same 2D shade. The 3D bars have the same model height across metrics. Source inspection confirms that each metric independently normalizes its bucket averages to that row's observed minimum/maximum. A constant row receives intensity 0.65, including a constant zero row. Height is `0.14 + intensity * 2.7`.

Add a persistent legend: “Relative value within each metric · lighter = lower · darker = higher.” Explain that shading does not establish an SLA breach and that values cannot be compared across different units. For constant rows use a neutral “No variation” treatment. For a single load, explain “One observed load level; load sensitivity cannot be assessed,” and show a compact value summary by default.

Offer a separate threshold or baseline comparison mode when supported by valid evidence. Threshold direction must be metric-specific: high error rate and low free memory mean different things; rising TPS alone does not establish poor health. Use text/icons with severity colors. In live views, consider locking scale and load buckets during inspection; this is an implementation-derived follow-up, not a behavior verified during this archived-run audit.

Source: `dashboard/src/components/loadImpactModel.ts` (buildLoadImpactModel), `LoadImpactHeatmap.tsx` (cell alpha), `LoadImpact3DHeatmap.tsx` (heightValue).

### P1 — Make 3D stable, labeled, and deliberately interactive

Evidence: step 2. The view rotates without operator input. Metric labels are tiny and overlap in perspective; there is no visible height scale, pause control, reset camera, or interaction instruction. Small chips repeat some axis labels but expose only eight metric names plus “+7.” The selected P95 remains selected across the view switch, which is a useful behavior to preserve.

Disable automatic rotation by default. Add explicit Rotate/Pause, Reset view, Top view, and zoom controls plus concise drag/zoom instructions. Fit labels to available space, keep critical axis titles outside the moving scene, and offer a focus-on-metric control. Keep selected and hovered values visually distinct. Provide keyboard-accessible selection outside the canvas; current canvas semantics expose an image, while 2D cells expose named buttons.

If the current cross-metric layout remains, label height as normalized relative value. A future exploration design should consider a single selected metric with a real unit-bearing height axis and meaningful load/time dimensions, but only when the available sampling supports those dimensions. That would be a design proposal, not an existing capability.

Automatic motion warrants accessibility review against [W3C's Pause, Stop, Hide guidance](https://www.w3.org/WAI/WCAG22/Understanding/pause-stop-hide). The existing 2D switch can hide 3D, so this review does not assert a formal WCAG failure. Also honor reduced-motion preferences and test keyboard/screen-reader equivalents.

### P1 — Put aggregation and evidence scope beside the map

Evidence: steps 1 and 3. The row label is P95, but its selected summary is “Avg 514ms.” Source confirms an arithmetic mean of interval P95 values, rather than recomputing a pooled percentile. “275 samples” represents telemetry points, not necessarily 275 requests. The report's top KPI shows a different P95 population; its contextual explanation is far above the map.

Label the display “Mean interval P95” or add an always-visible “Cells show averages of interval metrics” explanation. Show population, time window, sample interval, and resource ownership near the map. Resource metrics should explicitly identify the load generators; they should not invite inference about application-server saturation. Distinguish missing data, zero values, and intervals with no relevant observations where the source evidence supports that distinction. Mark low-coverage cells using a defined, evidence-based rule rather than treating sample counts as confidence by themselves.

### P2 — Make a heatmap read like a matrix

Evidence: step 1. The outer “Load impact” disclosure repeats the inner “Load Impact” title. Nested bordered surfaces, a tall header, three count chips, cell-level sample counts, and a table-pagination toolbar consume space. Only 10 of the 15 enabled metric rows are initially visible because the global table-pagination manager applies to the matrix.

Flatten to one panel with a compact toolbar and legend. Exempt this small metric matrix from generic row pagination; show grouped Performance and Generator resources rows together, with sticky headings where needed. Offer a concise key-metrics preset such as ART, P95, TPS, Error %, CPU, and memory. Put repeated sample counts in a secondary detail treatment while keeping coverage discoverable. Preserve exact numeric values and row/column highlighting.

Source-only follow-up: clicking a load header currently sorts different metric rows by their raw numeric averages, mixing milliseconds, percentages, MB, and rates. Such a ranking has no common meaning. Replace it with within-unit sorting or a defined dimensionless baseline/threshold deviation. This behavior was confirmed in source, not exercised as a browser task.

### P2 — Shorten the path from an interesting cell to an explanation

Evidence: step 3. Clicking P95 adds details below the entire matrix; the click leaves focus on the originating cell. The table contains both the selected-metric P95 column and the standard P95 column, and requires horizontal scrolling at the narrower width. It supplies timestamps and useful adjacent measurements, but no direct investigation action.

Use a compact pinned inspector beside the map on wide screens and directly below the selected area on smaller screens. Start with value, load, units, sample count, min/max, and coverage; show raw samples as a secondary disclosure. Remove duplicate metric columns. Add a small time series and a “View these intervals in timeline” action; a load bucket can contain disjoint intervals, so preserve that distinction. Consider related error and generator-metric links where evidence exists. Provide a visible close action and an accessible update announcement.

### P2 — Use an opaque configuration surface and task presets

Evidence: step 4. Text and blue cells from the underlying page remain visible through the drawer. A read-only computed-style inspection confirmed background `rgba(15, 17, 26, 0.65)` with opacity 1. This is the panel's color, not a transitional screenshot. The panel uses `--bg-card`.

Use an opaque modal/drawer token, stronger text and control contrast, and a clearly dimmed backdrop. Preserve the existing Performance/Resource grouping, native checkboxes, initial Close-button focus, and return focus to Configure Map. Add category-level selection and useful presets; rename Add All/Remove All to Select all/Clear selection, and clarify that Reset means default selection. If an empty selection is allowed, place a direct Restore defaults action in that state. These configuration recommendations do not imply the current controls are broken.

## Screenshots

### 1. 2D map — readable values, unexplained uniform shading

![Step 1: 2D load impact map](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/heatmap-audit-2026-09-14/01-2d-map.png)

### 2. 3D map — tiny labels and retained P95 selection

![Step 2: 3D map](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/heatmap-audit-2026-09-14/02-3d-map.png)

### 3. Sample details — useful evidence behind a wide table

![Step 3: P95 sample details](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/heatmap-audit-2026-09-14/03-cell-details.png)

### 4. Configuration — background content shows through the drawer

![Step 4: Heatmap configuration](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/heatmap-audit-2026-09-14/04-configuration.png)

## Limits and recommended validation

This was an operator-experience review of the rendered archived-run flow and shared component source, not an audit of telemetry correctness. No live stream, multilevel ramp, sparse-load fixture, WebGL failure, full keyboard-only workflow, screen reader, formal contrast measurement, or complete responsive matrix was tested. No full accessibility compliance is claimed. Detailed live behavior and dense multibucket occlusion remain follow-up checks. The legacy run cannot establish whether current runtime telemetry encodes missing observations correctly.

For an implementation pass, validate fixed load, ramp-up/down, constant-zero rows, sparse/missing observations, and different metric populations. Test scale meaning, population labels, duplicate-column removal, selection preservation, keyboard access, reduced motion, and the opaque drawer. Repository policy requires the dashboard test cycle for frontend changes; no application code changes were made by this review.

Start with the legend/aggregation copy, single-load treatment, opaque drawer, and default-static 3D controls before investing in a larger 3D redesign.
