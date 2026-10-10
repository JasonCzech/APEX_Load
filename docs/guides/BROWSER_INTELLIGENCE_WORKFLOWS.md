# Browser intelligence workflows

Status: implemented (Wave 5, E6). This guide covers BI-4 of the [feature completion specification](../FEATURE_COMPLETION_SPEC.md#bi-4-user-workflows-and-acceptance): where a run's browser intelligence is read, how its policies are edited, and what reports, the CLI and MCP tools carry. The results themselves are described in [browser findings](BROWSER_FINDINGS.md), [interaction evidence](BROWSER_INTERACTION_EVIDENCE.md) and [journey accounting](JOURNEY_ACCOUNTING.md).

## The Browser area of a run report

A run with browser groups, browser document evidence or a pinned browser policy (a project default is pinned at Start) has a **Browser** area in its run report (`#/report/{id}?section=browser`). It reads the finalized archive. On an HA controller, a panel says the archive is not available yet until it is published, and offers to check again. The area holds:

- **Device, engine and group comparison.** For each engine, device profile and scenario group, it shows each document metric's p75 over final qualified main-frame navigation documents, with how many documents it counts.
  - The p75 is computed as the controller computes it: the upper bound of the bucket holding it, capped by the observed maximum.
  - Documents record no page, so the scenario group stands in for one.
  - One engine, device and group that ran under more than one runtime cohort (for example, different network shaping) gets one row per cohort.
  - Child-frame and back-forward cache tuples are counted and left out.
- **Browser findings and the qualified score** under the run's pinned browser policy. For a run that pinned a browser policy, the area offers editors an on-demand evaluation under it; anyone who can view a run may also request an evaluation, under the pinned policy or a named revision, through the API or `apexctl`. An evaluation under a named revision is read from that evaluation and never replaces the run's findings shown here.
- **Browser baseline.** The baseline comparison of the run's latest root-cause analysis (anomalies, qualification and exclusions). Without an analysis, the area links to root-cause analysis.
- **Interaction patterns and heatmaps.**
- **Journey risk.**
- **The retained resource inspector.**

The live execution page shows the same comparison over the documents published so far. Findings, interaction patterns and journeys read the finalized archive, so they appear only in the run report.

### Drill-down: finding → document → resources → HAR entry

1. A finding lists its worst documents. Selecting one opens the retained resource inspector filtered to that document's requests (the `document_id` filter).
2. Each request's details offer **Show this request's HAR entry** to users who may download HARs (editors and project administrators). It reads `GET /api/v1/tests/{id}/archive/browser/har-entry?request_id=…`:
   - **Synthetic entry.** The request is returned as a synthetic HAR 1.2 entry, scrubbed and bounded exactly as the downloadable synthetic HAR is, with its `_apexload.request_id`.
   - **Native HAR.** A request whose browser context recorded a native HAR is not in the synthetic HAR. The response names that native HAR instead, and the inspector offers to download it. Native HARs recorded before they named their browser context cannot be matched to a request; when the archive has any, the response counts them, since the request may be in one.
   - **Left out.** A request the synthetic HAR leaves out (for example a URL with no host) says why, as a downloaded synthetic HAR does.
   - **Unknown request.** A request identity the archive does not hold answers 404.
   - **Busy.** The read scans the archive and shares the resource inspector's one scan at a time and its time limit: while another scan runs, or when it runs out of time, it answers 503 and can be retried.

## Editing browser and journey policies

**Settings → Workspaces → Browser & journey policies** lists a project's browser and journey policies. Editors, project administrators and system administrators can create, edit and delete them; viewers can read them.

- **Browser policy fields:** the scored metrics with their good and poor bounds and weights, the engines and device profiles it applies to, the effect floors (which only raise the defaults), and the baseline comparison mode.
- **Journey policy fields:** journeys with a transaction or action start and success boundary, a weight, and whether the step-risk index is reported.
- **Revisions:** every save names the revision it was read at. A policy someone changed in the meantime is refused, and the editor offers to load the current revision.
- **Past revisions:** any past revision can be shown read-only.
- **Defaults and pins:** a policy marked as the project default is pinned when runs start. Runs keep the revision they pinned, and findings keep the revision they were evaluated under, so editing a policy never rewrites a finished run's results.

## Saved panels

Project analytics panels can plot **Archived browser documents**: a document metric (`document_lcp`, `document_fcp`, `document_inp`, `document_cls`) at p50 to p99 over elapsed time.
- **One tuple per panel:** a panel reads one measurement tuple, named by engine, device profile, group, region, runtime cohort, frame scope and lifecycle. A selection that spans several tuples is refused and names the first difference.
- **Viewing:** saved panels open in the run's archived panels.

## Reports

The engineer report profile adds three sections, in HTML, PDF, CSV and (through HTML) DOCX:
- **Browser Findings:** the score and up to 20 findings, active ones first.
- **Interaction Patterns and Heatmaps:** coverage, up to 20 patterns and 12 heatmaps.
- **Journey Risk:** the weighted failure index and each journey's attempts.

Each section reads its result when the report is exported. A section whose result could not be read (no evaluation, an archive not finalized, no pinned journey policy) says why instead. The browser baseline comparison is part of the root-cause section, with the analysis it comes from, for projects that present root-cause v2 (`APEXLOAD_RCA_V2_PRESENT_PROJECTS`); the v1 root-cause section has no baseline. The Browser area shows the baseline for every project.

## CLI and MCP tools

| Task | `apexctl` | MCP tool |
| --- | --- | --- |
| Findings and score | `analysis browser-findings --run ID [--evaluate …]` | `run_archive_browser_findings` |
| Interaction patterns and heatmaps | `analysis interactions --run ID` | `run_archive_browser_interactions` |
| Journey accounting | `analysis journeys --run ID [--policy ID [--revision N]]` | `run_archive_journeys` |
| One request's HAR entry | `analysis har-entry --run ID --request-id ID` | `run_archive_browser_har_entry` |
| Policies | `analysis policies list\|get\|save\|delete --project ID …` | `analysis_policy_list`, `_get`, `_create`, `_update`, `_delete` |

`apexctl analysis policies save` with `--id` needs the `--revision` the policy was read at, as the editor does.

## Acceptance

[Browser intelligence acceptance](../qa/BROWSER_INTELLIGENCE_ACCEPTANCE.md) lists the tests that accept each of these behaviors, including a golden page run in Chromium and WebKit against measured ground truth.
