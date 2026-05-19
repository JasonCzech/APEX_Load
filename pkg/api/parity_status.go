package api

import (
	"net/http"
	"time"
)

const competitiveSupportMatrixEvidence = "docs/COMPETITIVE_SUPPORT_MATRIX.md"

type ParityStatusResponse struct {
	GeneratedAt       time.Time                 `json:"generated_at"`
	Overall           string                    `json:"overall"`
	Areas             []ParityStatusArea        `json:"areas"`
	CompetitiveParity []CompetitiveParityArea   `json:"competitive_parity,omitempty"`
	BrowserBenchmarks []BrowserBenchmarkProfile `json:"browser_benchmarks,omitempty"`
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
			Evidence:    []string{"/api/v1/scripts/import/loadrunner", "/api/v1/scripts/{id}/validate"},
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
			Summary:     "Browser replay, mobile profiles, client metrics, archive evidence, TruClient Mobile Web conversion, VuGen Web 2.0 review-profile migration evidence, selector/wait repair guidance, and code-owned benchmark artifacts support diagnostic browser canaries.",
			Evidence:    []string{"/api/v1/system/browser-benchmarks", "/api/v1/browser-replay/sessions/{session_id}", "/api/v1/scripts/browser-to-http/preview", "docs/BROWSER_BENCHMARK_ARTIFACTS.md", "docs/VUGEN_WEB2_TRUCLIENT2_PARITY_PLAN.md", "examples/web-api/browser-canary-plus-protocol-load.json"},
			Remaining:   []string{"Attach controlled raw benchmark output for each release generator image before raising browser density envelopes."},
			ReleaseGate: true,
		},
		{
			ID:          "vugen_web2_migration",
			Name:        "VuGen Web 2.0 Migration",
			Status:      "partial",
			Summary:     "LoadRunner ZIP import recognizes TruClient 2.0 - Web, preserves source/profile metadata, exposes Web 2.0 runtime settings, replay evidence, migration readiness, and browser-to-HTTP preview workflows.",
			Evidence:    []string{"/api/v1/scripts/import/loadrunner", "/api/v1/scripts/authoring/capabilities", "/api/v1/runtime-settings/support-matrix", "/api/v1/scripts/browser-to-http/preview", "docs/VUGEN_WEB2_TRUCLIENT2_PARITY_PLAN.md"},
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
			Summary:     "Archives, standalone reports, comparison baselines, usage CSV export, archive dataset CSV export, profile-shaped CSV reports, comparison CSV export, release trend CSV export, usage snapshots, cadence capture, and retention apply workflow are implemented.",
			Evidence:    []string{"/api/v1/system/usage/export", "/api/v1/system/usage/snapshots", "/api/v1/archives/retention/apply", "/api/v1/releases/{id}/trend/export"},
			Remaining:   []string{"Complete final historical reporting benchmark evidence."},
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
		GeneratedAt:       now,
		Overall:           aggregateParityStatus(areas),
		Areas:             areas,
		CompetitiveParity: buildCompetitiveParityStatus(),
		BrowserBenchmarks: browserBenchmarks.Profiles,
	}
}

func buildCompetitiveParityStatus() []CompetitiveParityArea {
	return []CompetitiveParityArea{
		{
			ID:          "developer_experience",
			Name:        "Developer Script-As-Code Experience",
			Status:      "partial",
			Competitors: []string{"k6"},
			Summary:     "Stormrunner has CLI execution, JSON DSL validation, AI explain output, CI templates, and importable scripts, but it is not yet as familiar or portable as k6's JavaScript-first local/cloud workflow.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"README.md#build--run",
				"docs/K6_NEOLOAD_PARITY_PLAN.md#phase-c2-k6-aligned-developer-experience",
				".github/actions/stormrunner-run/action.yml",
			},
			Remaining: []string{
				"Add JSON validation mode, k6 result ingestion, and constrained k6 HTTP conversion/export.",
			},
			Decision:      "differentiate",
			EvidenceLevel: "docs+code",
		},
		{
			ID:          "managed_scale_load_zones",
			Name:        "Managed Scale And Load Zones",
			Status:      "partial",
			Competitors: []string{"k6", "NeoLoad"},
			Summary:     "Stormrunner exposes regional generator inventory, capacity APIs, placement rejection visibility, and autoscaling guidance, but still needs named load-zone packaging and published benchmark evidence.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"/api/v1/system/inventory",
				"/api/v1/fleet/capacity",
				"/api/v1/fleet/alerts",
				"docs/ENTERPRISE_GENERATOR_FLEET_MODEL.md",
				"docs/ENTERPRISE_AUTOSCALING_SIGNALS.md",
			},
			Remaining: []string{
				"Promote regions to named load zones with cost/capacity metadata and publish benchmark-backed density envelopes.",
			},
			Decision:      "match_with_packaging",
			EvidenceLevel: "api+docs+tests",
		},
		{
			ID:          "authoring_recording",
			Name:        "Authoring, Recording, And Guided Repair",
			Status:      "partial",
			Competitors: []string{"k6", "NeoLoad"},
			Summary:     "Stormrunner supports script imports, templates, validation, replay evidence, authoring sessions, and guided repair contracts; the remaining gap is workflow polish versus k6 Test Builder and NeoLoad Designer.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"/api/v1/scripts/import/loadrunner",
				"/api/v1/scripts/import/jmeter",
				"/api/v1/scripts/import/sample",
				"docs/ENTERPRISE_AUTHORING_CONTRACT.md",
				"docs/SCRIPT_AUTHORING_CONSOLIDATION_ROADMAP.md",
			},
			Remaining: []string{
				"Finish guided task flows and unify recorder/import/manual repair review into one calmer operator experience.",
			},
			Decision:      "match_core_workflows",
			EvidenceLevel: "api+docs+dashboard",
		},
		{
			ID:          "browser_testing",
			Name:        "Browser Testing And Client UX Evidence",
			Status:      "partial",
			Competitors: []string{"k6", "NeoLoad"},
			Summary:     "Stormrunner has browser replay, mobile web profile guidance, client metrics, and archive evidence, while deliberately treating browser VUsers as diagnostic canaries until benchmark data supports broader claims.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"/api/v1/scripts/browser-replay/sessions",
				"/api/v1/system/browser-benchmarks",
				browserBenchmarkArtifactEvidence,
				"docs/MOBILE_BROWSER_EMULATION_PLAN.md",
				"docs/BROWSER_CLIENT_METRICS_PLAN.md",
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
			Status:      "partial",
			Competitors: []string{"k6", "NeoLoad"},
			Summary:     "Stormrunner has OpenTelemetry, Prometheus export, archives, comparison reports, release trends, AI-assisted archive root-cause summaries, and trace correlation primitives, but still needs deeper packaged APM profile workflows.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"/metrics",
				"/api/v1/tests/{id}/archive/report",
				"/api/v1/tests/{id}/archive/comparison",
				"/api/v1/tests/{id}/archive/ai-root-cause",
				"docs/RUNTIME_TELEMETRY_SLA_PLAN.md",
				"docs/PLATFORM_GAP_ANALYSIS.md#51-apm--observability-correlation",
			},
			Remaining: []string{
				"Deepen APM profile setup guidance and infrastructure monitor workflows with provider-specific fixtures.",
			},
			Decision:      "match_packaged_analysis",
			EvidenceLevel: "api+docs",
		},
		{
			ID:          "specialized_enterprise_protocols",
			Name:        "Specialized Enterprise Protocol Breadth",
			Status:      "gap",
			Competitors: []string{"NeoLoad"},
			Summary:     "Stormrunner covers HTTP/API, SOAP, Java-over-HTTP, JMS, WebSocket, gRPC, HAR, JMeter, DevWeb, and LoadRunner migration paths, but native SAP GUI, Citrix, Oracle Forms, and desktop UI protocols remain demand-gated non-goals.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"docs/K6_NEOLOAD_PARITY_PLAN.md#phase-c6-specialized-enterprise-protocol-decisions",
				"docs/PROTOCOL_EXTENSION_PLAN.md",
				"docs/BROWSER_CLIENT_METRICS_PLAN.md#non-goals",
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
			Summary:     "Stormrunner has docs, CI templates, dashboard workflows, and several sample/import paths, but evaluators still need canonical demos, a public support matrix, and release notes that connect claims to benchmark artifacts.",
			Evidence: []string{
				competitiveSupportMatrixEvidence,
				"README.md#quick-start",
				"docs/K6_NEOLOAD_PARITY_PLAN.md#phase-c7-competitive-packaging-samples-and-trust",
				"docs/LOADRUNNER_RTS_REFERENCE.md",
			},
			Remaining: []string{
				"Publish canonical sample projects, one-command demo seeding, and an honest Stormrunner versus k6 versus NeoLoad matrix.",
			},
			Decision:      "evidence_gate",
			EvidenceLevel: "docs",
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
