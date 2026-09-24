package api

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"time"
)

const competitiveSupportMatrixEvidence = "docs/COMPETITIVE_COMPARISON.md#support-matrix"
const apiComparisonEvidence = "docs/COMPETITIVE_COMPARISON.md#rest-api-comparison"

//go:embed cloudtest_parity_catalog.json
var cloudTestParityCatalogJSON []byte

type ParityStatusResponse struct {
	GeneratedAt           time.Time                 `json:"generated_at"`
	Overall               string                    `json:"overall"`
	Areas                 []ParityStatusArea        `json:"areas"`
	CompetitiveParity     []CompetitiveParityArea   `json:"competitive_parity,omitempty"`
	CloudTestCapabilities []CloudTestCapability     `json:"cloudtest_capabilities,omitempty"`
	APIParityProgram      []ParityStatusArea        `json:"api_parity_program,omitempty"`
	BrowserBenchmarks     []BrowserBenchmarkProfile `json:"browser_benchmarks,omitempty"`
}

// CloudTestCapability is the canonical, machine-readable outcome-parity ledger
// row. Documentation and dashboard views consume this contract rather than
// maintaining independent capability claims.
type CloudTestCapability struct {
	ID                   string `json:"id"`
	Category             string `json:"category"`
	Name                 string `json:"name"`
	CloudTestEvidenceURL string `json:"cloudtest_evidence_url"`
	EvidenceConfidence   string `json:"evidence_confidence"`
	APEXStatus           string `json:"apex_status"`
	Workstream           string `json:"workstream"`
	AcceptanceTest       string `json:"acceptance_test"`
	ImplementationStatus string `json:"implementation_status"`
}

var cloudTestParityCatalog = loadCloudTestParityCatalog()

func loadCloudTestParityCatalog() []CloudTestCapability {
	var capabilities []CloudTestCapability
	if err := json.Unmarshal(cloudTestParityCatalogJSON, &capabilities); err != nil {
		panic("decode embedded CloudTest parity catalog: " + err.Error())
	}
	if len(capabilities) == 0 {
		panic("embedded CloudTest parity catalog is empty")
	}
	return capabilities
}

func cloneCloudTestParityCatalog() []CloudTestCapability {
	return append([]CloudTestCapability(nil), cloudTestParityCatalog...)
}

type ParityStatusArea struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	Summary      string   `json:"summary"`
	Evidence     []string `json:"evidence,omitempty"`
	Remaining    []string `json:"remaining,omitempty"`
	ReleaseGate  bool     `json:"release_gate"`
	BlockingGate bool     `json:"blocking_gate,omitempty"`
}

type CompetitiveParityArea struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Status        string   `json:"status"`
	Competitors   []string `json:"competitors,omitempty"`
	Summary       string   `json:"summary"`
	Evidence      []string `json:"evidence,omitempty"`
	Remaining     []string `json:"remaining,omitempty"`
	Decision      string   `json:"decision,omitempty"`
	EvidenceLevel string   `json:"evidence_level,omitempty"`
}

func (s *Server) handleGetParityStatus(w http.ResponseWriter, r *http.Request) {
	response := buildParityStatus(time.Now().UTC())
	writeJSON(w, http.StatusOK, response)
}

func buildParityStatus(now time.Time) ParityStatusResponse {
	browserBenchmarks := buildBrowserBenchmarkEvidence(now)
	areas := []ParityStatusArea{
		{
			ID:          "architecture",
			Name:        "Platform Architecture",
			Status:      "partial",
			Summary:     "Project ownership, readiness checks, settings persistence, and archive/report contracts are in place.",
			Evidence:    []string{"/api/v1/system/readiness", "/api/v1/system/settings"},
			Remaining:   []string{"Complete external dependency evidence and production benchmark gates."},
			ReleaseGate: true,
		},
		{
			ID:          "fleet",
			Name:        "Fleet And Scale-Out",
			Status:      "partial",
			Summary:     "Fleet inventory, alerts, persisted drain state, capacity filtering, benchmark-calibrated density envelopes, and placement rejection visibility have first-pass APIs.",
			Evidence:    []string{"/api/v1/system/inventory", "/api/v1/fleet/alerts", "/api/v1/fleet/capacity"},
			Remaining:   []string{"Complete production deployment autoscaling evidence."},
			ReleaseGate: true,
		},
		{
			ID:          "authoring",
			Name:        "Authoring UX",
			Status:      "partial",
			Summary:     "Imports, validation, replay evidence, templates, runtime parity scoring, migration guardrails, and list-level authoring health badges are implemented across backend and dashboard slices.",
			Evidence:    []string{"/api/v1/scripts/import/enterprise-script-archive", "/api/v1/scripts/{id}/validate"},
			Remaining:   []string{"Complete final authoring benchmark evidence."},
			ReleaseGate: true,
		},
		{
			ID:          "ai_assistance",
			Name:        "AI-Assisted Workflows",
			Status:      "complete",
			Summary:     "Governed deterministic draft generation, repair suggestions, explain mode, policy controls, review queue, guardrails, and archive root-cause summaries are implemented without bypassing approval gates.",
			Evidence:    []string{"/api/v1/ai/authoring/contract", "/api/v1/ai/drafts", "/api/v1/ai/repairs", "/api/v1/ai/explain", "/api/v1/ai/review-queue", "/api/v1/ai/policy", "/api/v1/tests/{id}/archive/ai-root-cause", "docs/AI_ASSISTED_WORKFLOWS.md"},
			Remaining:   []string{"External LLM provider integrations and richer dashboard-specific AI affordances remain demand-gated behind the same policy contract."},
			ReleaseGate: true,
		},
		{
			ID:          "load_modeling",
			Name:        "Load Modeling",
			Status:      "partial",
			Summary:     "Staged ramps, rendezvous actions, arrival modeling, validation fit checks, goal reporting, and coordinated-omission comparison export evidence have backend coverage.",
			Evidence:    []string{"/api/v1/scenarios/validate-load-model", "/api/v1/tests/{id}/archive/report", "/api/v1/tests/{id}/archive/comparison"},
			Remaining:   []string{"Complete final load-model benchmark evidence."},
			ReleaseGate: true,
		},
		{
			ID:          "browser_testing",
			Name:        "Browser Testing",
			Status:      "partial",
			Summary:     "Browser replay, mobile profiles, client metrics, archive evidence, mobile browser conversion, Web 2.0 browser-profile review evidence, selector/wait repair guidance, and code-owned benchmark artifacts support diagnostic browser canaries.",
			Evidence:    []string{"/api/v1/system/browser-benchmarks", "/api/v1/browser-replay/sessions/{session_id}", "/api/v1/scripts/browser-to-http/preview", "docs/PERFORMANCE_BENCHMARKS.md", "docs/COMPETITIVE_COMPARISON.md#web-20-browser-profile", "examples/web-api/browser-canary-plus-protocol-load.json"},
			Remaining:   []string{"Attach controlled raw benchmark output for each release generator image before raising browser density envelopes."},
			ReleaseGate: true,
		},
		{
			ID:          "web2_browser_profile_migration",
			Name:        "Web 2.0 Browser Profile Review",
			Status:      "partial",
			Summary:     "Enterprise Script Archive import recognizes Web 2.0 browser-profile archives, preserves source/profile metadata, exposes runtime settings, replay evidence, migration readiness, and browser-to-HTTP preview workflows.",
			Evidence:    []string{"/api/v1/scripts/import/enterprise-script-archive", "/api/v1/scripts/authoring/capabilities", "/api/v1/runtime-settings/support-matrix", "/api/v1/scripts/browser-to-http/preview", "docs/COMPETITIVE_COMPARISON.md#web-20-browser-profile"},
			Remaining:   []string{"Keep production readiness gated on replay evidence, selector repair, runtime parity review, and generated HTTP validation per import."},
			ReleaseGate: true,
		},
		{
			ID:          "monitoring",
			Name:        "Real-Time Monitoring",
			Status:      "partial",
			Summary:     "Live metrics, telemetry lag, fleet alerts, schedule health, browser/JMS diagnostics, Prometheus export, and the operator parity checklist dashboard are available.",
			Evidence:    []string{"/metrics", "/api/v1/schedules/health", "/api/v1/fleet/alerts", "/api/v1/system/parity-status"},
			Remaining:   []string{"Complete final monitoring benchmark evidence."},
			ReleaseGate: true,
		},
		{
			ID:          "reporting",
			Name:        "Historical Reporting",
			Status:      "partial",
			Summary:     "Archives, standalone HTML, PDF, and DOCX reports, comparison baselines, merged archive analysis graph/drilldown, dataset/profile CSV, report-chart CSV/SVG exports, release trends, usage snapshots, cadence capture, and retention workflows are implemented.",
			Evidence:    []string{"/api/v1/tests/{id}/archive/analysis", "/api/v1/tests/{id}/archive/report?format=pdf", "/api/v1/tests/{id}/archive/report?format=docx", "/api/v1/system/usage/export", "/api/v1/system/usage/snapshots", "/api/v1/archives/retention/apply", "/api/v1/releases/{id}/trend/export"},
			Remaining:   []string{"Apply bounded CSV/SVG export to remaining operational charts, complete dashboard graph UX, and publish final historical reporting benchmark evidence."},
			ReleaseGate: true,
		},
		{
			ID:          "scheduling",
			Name:        "Scheduling",
			Status:      "partial",
			Summary:     "Persisted schedules, occurrences, approvals, missed-run handling, durable lease ownership, health, and restart reconciliation are implemented.",
			Evidence:    []string{"/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences", "/api/v1/schedules/health"},
			Remaining:   []string{"Expand governance conflict evidence for all external environment calendars."},
			ReleaseGate: true,
		},
	}
	return ParityStatusResponse{
		GeneratedAt:           now,
		Overall:               aggregateParityStatus(areas),
		Areas:                 areas,
		CompetitiveParity:     buildCompetitiveParityStatus(),
		CloudTestCapabilities: cloneCloudTestParityCatalog(),
		APIParityProgram:      buildAPIParityProgram(),
		BrowserBenchmarks:     browserBenchmarks.Profiles,
	}
}

// buildAPIParityProgram returns the in-flight LoadRunner Enterprise REST API-parity
// build dimensions. These are the "▼ APEX trails" rows from
// docs/COMPETITIVE_COMPARISON.md#rest-api-comparison, tracked here so the scorecard doc and the
// endpoint never drift. Each flips gap → partial → supported as its family lands.
// They are intentionally separate from Areas (shipped platform state) and do not
// participate in release-gate aggregation.
func buildAPIParityProgram() []ParityStatusArea {
	return []ParityStatusArea{
		{
			ID:        "capacity_reservation",
			Name:      "Capacity Reservations (Timeslots)",
			Status:    "supported",
			Summary:   "Forward-booking timeslot reservations have first-class CRUD, conflict/availability checks, an approval state, and atomic binding. Timeslots are now load-bearing: a test create accepts a timeslot_id and binds it strictly (the create fails if it can't be bound), and admission control is reservation-aware — an approved, bound timeslot reserves generators for its window so other tenants are held PENDING for the capacity it holds.",
			Evidence:  []string{apiComparisonEvidence, "/api/v1/capacity/timeslots", "/api/v1/capacity/timeslots/availability", "/api/v1/tests (timeslot_id)", "pkg/api/handlers_timeslots.go", "pkg/storage/sqlite_timeslots.go", "pkg/api/capacity.go", "pkg/api/timeslot_reservation.go"},
			Remaining: []string{"Auto-rebind a timeslot if its test is recreated; cross-window reservation rollover (future)."},
		},
		{
			ID:        "fleet_management",
			Name:      "Fleet Pool & Provisioning Management",
			Status:    "supported",
			Summary:   "Managed generator pools, persistent generator metadata (labels/region/cordon/pool membership), and provisioning requests have CRUD/PATCH APIs. A real in-cluster Kubernetes provisioner (APEXLOAD_GENERATOR_PROVISIONER=kubernetes) scales the generator StatefulSet's replicas via the controller's service account on POST /fleet/provision-requests (lifecycle: provisioning→success/failed with events; no new cloud credentials). The live inventory is pool-aware (GeneratorInventoryEntry carries fleet_pool_id/pool_name/cordon from persisted metadata), and a run can target a pool (assignment.pool_id) which resolves to the pool's member generators at placement.",
			Evidence:  []string{apiComparisonEvidence, "/api/v1/fleet/pools", "/api/v1/fleet/generators", "/api/v1/fleet/provision-requests", "/api/v1/fleet/provisioner", "pkg/api/fleet_provisioner.go", "pkg/kubeops/client.go", "pkg/api/handlers_fleet_management.go"},
			Remaining: []string{"Cloud (AWS/Azure/GCP) provisioner backends and autoscaling / idle-generator reaping remain future work via the GeneratorProvisioner registry; the in-cluster Kubernetes scaler is verified via an injected fake client (live scaling runs in-cluster)."},
		},
		{
			ID:        "execution_planning",
			Name:      "Enterprise Execution Planning",
			Status:    "supported",
			Summary:   "Execution plans (ordered scenario entries with per-entry runtime overrides, data-pool overrides, optional timeslot binding, and failure policy) have CRUD plus a materialize endpoint. Sequential plans are dependency-gated: each entry's run starts only after the previous run reaches a terminal state, honoring per-entry halt/skip; parallel plans stage all entries. Plan-run progress is persisted and queryable (GET /runs, /runs/{runId}) and streamed via execution_plan_* WebSocket events.",
			Evidence:  []string{apiComparisonEvidence, "/api/v1/execution-plans", "/api/v1/execution-plans/{id}/runs", "pkg/api/execution_plan_orchestrator.go", "pkg/api/handlers_execution_plans.go", "pkg/storage/sqlite_execution_plans.go"},
			Remaining: []string{"Reconcile in-flight sequential runs across a controller restart (live state is in-memory today); optional parallel-mode auto-start (future)."},
		},
		{
			ID:        "monitoring_collectors",
			Name:      "Monitoring Collectors",
			Status:    "supported",
			Summary:   "Restricted-network monitoring collectors register via API with SHA-256 scoped tokens (one-time issue + rotation) and allowed metric packs, then push collector-token-authenticated heartbeats (POST /collectors/{id}/heartbeat) that record health + last-seen and ingest in-scope metric packs; the latest snapshot is routed to live monitoring (WebSocket monitoring_collector_heartbeat + GET /collectors/{id}/metrics) and a dependency-free agent runtime (cmd/apexload-collector-agent) collects host metrics and pushes on an interval.",
			Evidence:  []string{apiComparisonEvidence, "/api/v1/monitoring/collectors", "/api/v1/monitoring/collectors/{id}/heartbeat", "/api/v1/monitoring/collectors/{id}/metrics", "pkg/api/collector_metrics.go", "pkg/collectoragent/agent.go", "cmd/apexload-collector-agent"},
			Remaining: []string{"Live snapshot is in-memory (like running-test metrics); durable metric history, richer per-pack schemas, and attaching collector metrics to a specific running test's resources are future work."},
		},
		{
			ID:        "entitlements",
			Name:      "Enterprise Entitlements (Read-Only)",
			Status:    "supported",
			Summary:   "Read-only entitlements project edition, enabled protocol bundles, and operational limits with richer consumption breakdowns: per-limit used-vs-cap (concurrent tests + active VUsers, global and per project), per-bundle historical run consumption, and a per-project view (system /entitlements/usage carries limits/protocol_bundles/by_project; GET /projects/{id}/entitlements/usage scopes to one project). Surfaced for visibility only — never enforced; there is no licensing engine.",
			Evidence:  []string{apiComparisonEvidence, "/api/v1/system/entitlements", "/api/v1/system/entitlements/usage", "/api/v1/projects/{id}/entitlements/usage", "pkg/api/entitlement_usage.go", "pkg/api/handlers_entitlements.go"},
			Remaining: []string{"By design, the surface stays read-only: live per-bundle active-test attribution and any tier enforcement are out of scope (no licensing engine)."},
		},
		{
			ID:        "correlation_persistence",
			Name:      "Persistent Correlation Rulesets",
			Status:    "supported",
			Summary:   "Per-script recorded baselines are persisted (request/response exchanges with selective credential redaction that preserves session/CSRF/view-state) and diagnose-replay ranks repair suggestions. Learned, reusable correlation rulesets are now project-scoped and persisted: discover-from-script runs correlation detection over a baseline and saves the rules; apply-to-script matches a ruleset against a target script's baseline and previews or writes the extractors + value-templating into a new script version.",
			Evidence:  []string{apiComparisonEvidence, "/api/v1/correlations/rules", "/api/v1/correlations/diagnose", "/api/v1/correlation-rulesets", "/api/v1/correlation-rulesets/discover-from-script", "/api/v1/correlation-rulesets/{id}/apply-to-script", "/api/v1/scripts/{id}/recording/status", "/api/v1/scripts/{id}/diagnose-replay", "pkg/api/handlers_correlation_rulesets.go", "pkg/storage/sqlite_correlation_rulesets.go", "pkg/convert/correlate", "pkg/convert/paramize"},
			Remaining: []string{"Cross-project ruleset sharing and an org-wide rule library (future)."},
		},
		{
			ID:        "object_repository",
			Name:      "Browser Object Repository",
			Status:    "supported",
			Summary:   "Project-scoped, reusable object repositories are persisted (named element descriptors with CSS/XPath/role/text + ranked selector candidates), creatable directly, seeded from a script's captured selectors, and bound to scripts by logical name via the action object_ref field. Bindings resolve on the controller at script preparation into concrete selectors + ranked fallbacks for both debug replay and distributed load, and replay self-heal feeds the working locator back into the bound repository object.",
			Evidence:  []string{apiComparisonEvidence, "/api/v1/object-repositories", "/api/v1/object-repositories/from-script", "pkg/api/handlers_object_repository.go", "pkg/api/object_ref_resolver.go", "pkg/api/object_ref_self_heal.go", "pkg/storage/sqlite_object_repository.go", "pkg/selector"},
			Remaining: []string{"Auto-promote a healed locator to primary (today a human promotes via the dashboard); cross-project object sharing."},
		},
	}
}

func buildCompetitiveParityStatus() []CompetitiveParityArea {
	return []CompetitiveParityArea{
		{
			ID:          "developer_experience",
			Name:        "Developer Script-As-Code Experience",
			Status:      "partial",
			Competitors: []string{"k6"},
			Summary:     "APEX Load has CLI execution, local script validation/explain commands, governed explain output, CI templates, k6 result ingestion, and constrained k6 HTTP import/export, but it is not a JavaScript-first local/cloud workflow.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"README.md#build--run",
				"docs/COMPETITIVE_COMPARISON.md#developer-experience",
				".github/actions/apexload-run/action.yml",
				"apexctl script validate",
				"apexctl script explain",
				"/api/v1/scripts/import/k6",
				"/api/v1/scripts/export/k6",
				"/api/v1/scripts/import/k6/support-matrix",
				"/api/v1/results/import",
			},
			Remaining: []string{
				"Expand k6 fixture coverage while keeping unsupported JavaScript and browser constructs review-only.",
			},
			Decision:      "differentiate",
			EvidenceLevel: "docs+code",
		},
		{
			ID:          "managed_scale_load_zones",
			Name:        "Managed Scale And Load Zones",
			Status:      "partial",
			Competitors: []string{"k6", "NeoLoad", "CloudTest"},
			Summary:     "APEX Load exposes regional generator inventory, named self-hosted load zones, capacity APIs, placement rejection visibility, and autoscaling guidance, but still needs published benchmark evidence per zone.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"/api/v1/system/inventory",
				"/api/v1/fleet/capacity",
				"/api/v1/fleet/load-zones",
				"/api/v1/fleet/alerts",
				"docs/ENTERPRISE_FLEET_AND_CAPACITY.md",
			},
			Remaining: []string{
				"Attach benchmark-backed density envelopes to each named self-hosted load zone before raising scale claims.",
			},
			Decision:      "match_with_packaging",
			EvidenceLevel: "api+docs+tests",
		},
		{
			ID:          "authoring_recording",
			Name:        "Authoring, Recording, And Guided Repair",
			Status:      "supported",
			Competitors: []string{"k6", "NeoLoad", "CloudTest"},
			Summary:     "APEX Load now supports the current C5 authoring scope: guided script and scenario creation, imports, templates, validation, replay evidence, authoring sessions, review approval, response-family correlation repair, provenance review, and reusable components.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"/api/v1/scripts/import/enterprise-script-archive",
				"/api/v1/scripts/import/jmeter",
				"/api/v1/scripts/import/sample",
				"/api/v1/scripts/authoring/sessions",
				"/api/v1/scripts/authoring/components",
				"/api/v1/correlations/rules",
				"/api/v1/correlations/diagnose",
				"docs/ENTERPRISE_CONTRACTS.md",
				"docs/ROADMAP.md#script-authoring-consolidation",
			},
			Remaining: []string{
				"Expand public demo packs, competitive fixtures, and richer recorder examples so evaluators can reproduce the supported workflow quickly.",
			},
			Decision:      "supported_for_current_scope",
			EvidenceLevel: "api+docs+dashboard+tests",
		},
		{
			ID:          "browser_testing",
			Name:        "Browser Testing And Client UX Evidence",
			Status:      "partial",
			Competitors: []string{"k6", "NeoLoad", "CloudTest"},
			Summary:     "APEX Load has browser replay, mobile web profile guidance, client metrics, and archive evidence, while deliberately treating browser VUsers as diagnostic canaries until benchmark data supports broader claims.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"/api/v1/scripts/browser-replay/sessions",
				"/api/v1/system/browser-benchmarks",
				browserBenchmarkArtifactEvidence,
				"docs/BROWSER_ROADMAP.md",
			},
			Remaining: []string{
				"Attach controlled raw benchmark artifacts for each release generator image before increasing browser density envelopes.",
			},
			Decision:      "bounded_canary_support",
			EvidenceLevel: "api+docs+external_evidence_required",
		},
		{
			ID:          "analysis_integrations",
			Name:        "Analysis, APM, And Root-Cause Integrations",
			Status:      "supported",
			Competitors: []string{"k6", "NeoLoad"},
			Summary:     "APEX Load now supports the current C4 analysis scope: OpenTelemetry and Prometheus, APM profiles, infrastructure monitor packs, archives, merged graph/drilldown analysis, comparison reports, release trends, root-cause summaries, SLA governance, report profiles, and external result ingestion.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"/metrics",
				"/api/v1/integrations/apm/profiles",
				"/api/v1/monitoring/packs",
				"/api/v1/tests/{id}/archive/report",
				"/api/v1/tests/{id}/archive/analysis",
				"/api/v1/tests/{id}/archive/comparison",
				"/api/v1/tests/{id}/archive/root-cause",
				"/api/v1/tests/{id}/archive/ai-root-cause",
				"/api/v1/releases/{id}/sla-governance",
				"/api/v1/reports/profiles",
				"/api/v1/results/import",
				"docs/ROADMAP.md#runtime-telemetry-sla",
				"docs/COMPETITIVE_COMPARISON.md#apm-observability-correlation",
			},
			Remaining: []string{
				"Deepen provider-specific APM fixtures, causal scoring, and packaged setup guides as connector evidence matures.",
			},
			Decision:      "supported_for_current_scope",
			EvidenceLevel: "api+docs+dashboard+tests",
		},
		{
			ID:          "specialized_enterprise_protocols",
			Name:        "Specialized Enterprise Protocol Breadth",
			Status:      "gap",
			Competitors: []string{"NeoLoad"},
			Summary:     "APEX Load covers HTTP/API, SOAP, Java-over-HTTP, JMS, WebSocket, gRPC, HAR, JMeter, DevWeb, and enterprise script archive migration paths, but native SAP GUI, Citrix, Oracle Forms, and desktop UI protocols remain demand-gated non-goals.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"docs/COMPETITIVE_COMPARISON.md#specialized-enterprise-protocols",
				"docs/archive/plans/PROTOCOL_EXTENSION_PLAN.md",
				"docs/BROWSER_ROADMAP.md#vision-and-architecture",
			},
			Remaining: []string{
				"Complete customer-backed demand assessment and publish explicit non-goal or coexistence guidance for unsupported legacy protocols.",
			},
			Decision:      "demand_gated_non_goal",
			EvidenceLevel: "docs",
		},
		{
			ID:          "packaging_samples_trust",
			Name:        "Packaging, Samples, And Trust",
			Status:      "partial",
			Competitors: []string{"k6", "NeoLoad"},
			Summary:     "APEX Load has docs, a support matrix, CI templates, dashboard workflows, canonical sample/import paths, one-command demo seeding, and release-evidence manifests; release-specific benchmark deltas remain evidence-gated.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"README.md#quick-start",
				"docs/COMPETITIVE_COMPARISON.md#packaging-samples-and-trust",
				"docs/COMPETITIVE_COMPARISON.md#enterprise-archive-runtime-settings-reference",
				"docs/COMPETITIVE_COMPARISON.md#evaluator-trust-pack",
				"docs/COMPETITIVE_COMPARISON.md#one-page-summary",
				"docs/GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md#release-evidence-manifest",
				"scripts/seed-evaluator-demo.sh",
			},
			Remaining: []string{
				"Attach release-specific benchmark deltas to release notes before promoting density claims.",
			},
			Decision:      "evidence_gate",
			EvidenceLevel: "docs+samples+release_artifacts",
		},
		{
			ID:          "visual_composition_authoring",
			Name:        "Visual Composition And Timed Tracks",
			Status:      "partial",
			Competitors: []string{"CloudTest"},
			Summary:     "APEX scripts already provide reusable ordered action clips and scenario groups provide multi-script composition, while active-time group offsets, a shared visual timeline, switch branching, and bounded parallel containers remain staged work.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"docs/CLOUDTEST_PARITY_PROGRAM.md",
				"https://techdocs.akamai.com/cloudtest/docs/test-composition",
			},
			Remaining: []string{
				"Ship offset runtime semantics, immutable provenance, peak-window capacity planning, visual timeline round-trip, and branch safety gates.",
			},
			Decision:      "outcome_parity",
			EvidenceLevel: "current_official+repo_verified",
		},
		{
			ID:          "streaming_result_drilldown",
			Name:        "Streaming Analytics And Safe Result Drill-Down",
			Status:      "partial",
			Competitors: []string{"CloudTest"},
			Summary:     "APEX has live metrics, archived transaction filtering and redacted request/response inspection, HTTP page resources, network timing entries, streaming-response pacing, and PDF reporting; regional live aggregation and policy-bounded success detail are not yet complete.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"docs/CLOUDTEST_PARITY_PROGRAM.md",
				"https://techdocs.akamai.com/cloudtest/docs/analytics",
				"/api/v1/tests/{id}/archive/transactions",
			},
			Remaining: []string{
				"Add bounded regional aggregation, capture admission and quotas, hierarchy/phase attribution, live metadata notifications, and bucket-to-waterfall navigation.",
			},
			Decision:      "outcome_parity",
			EvidenceLevel: "current_official+repo_verified",
		},
		{
			ID:          "agentless_infrastructure_monitors",
			Name:        "Run-Bound Private-Network Monitoring",
			Status:      "partial",
			Competitors: []string{"CloudTest"},
			Summary:     "APEX already has authenticated restricted-network collector registration, heartbeat metric packs, and live visibility. The outcome-parity design extends that collector instead of allowing the controller to dial customer infrastructure.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"docs/CLOUDTEST_PARITY_PROGRAM.md",
				"/api/v1/monitoring/collectors",
				"cmd/apexload-collector-agent",
			},
			Remaining: []string{
				"Persist run-bound samples and add signed, revisioned, destination-allowlisted connector jobs with sealed expiring secrets.",
			},
			Decision:      "outcome_parity",
			EvidenceLevel: "current_official+repo_verified",
		},
	}
}

func aggregateParityStatus(areas []ParityStatusArea) string {
	overall := "complete"
	for _, area := range areas {
		if area.BlockingGate || area.Status == "blocked" {
			return "blocked"
		}
		if area.Status != "complete" {
			overall = "partial"
		}
	}
	return overall
}
