package api

import (
	"net/http"
	"time"
)

const browserBenchmarkArtifactEvidence = "docs/BROWSER_BENCHMARK_ARTIFACTS.md"

type BrowserBenchmarkEvidenceResponse struct {
	GeneratedAt       time.Time                    `json:"generated_at"`
	ClaimPolicy       string                       `json:"claim_policy"`
	HardwareProfile   BrowserBenchmarkHardware     `json:"hardware_profile"`
	Profiles          []BrowserBenchmarkProfile    `json:"profiles"`
	EnvironmentMatrix []BrowserBenchmarkEnvProfile `json:"environment_matrix"`
	Commands          []string                     `json:"commands"`
	Evidence          []string                     `json:"evidence"`
}

type BrowserBenchmarkHardware struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CPUCores    int    `json:"cpu_cores"`
	MemoryGB    int    `json:"memory_gb"`
	Runtime     string `json:"runtime"`
	Description string `json:"description"`
}

type BrowserBenchmarkProfile struct {
	ID                      string                          `json:"id"`
	Name                    string                          `json:"name"`
	BrowserEngine           string                          `json:"browser_engine"`
	DeviceProfile           string                          `json:"device_profile"`
	DeviceClass             string                          `json:"device_class"`
	Platform                string                          `json:"platform"`
	Status                  string                          `json:"status"`
	EvidenceLevel           string                          `json:"evidence_level"`
	ArtifactPath            string                          `json:"artifact_path"`
	Acceptance              BrowserBenchmarkAcceptance      `json:"acceptance"`
	DensityEnvelope         BrowserBenchmarkDensityEnvelope `json:"density_envelope"`
	BenchmarkCommands       []string                        `json:"benchmark_commands"`
	Evidence                []string                        `json:"evidence"`
	ReleaseGate             bool                            `json:"release_gate"`
	HighDensityClaimAllowed bool                            `json:"high_density_claim_allowed"`
	Notes                   []string                        `json:"notes,omitempty"`
}

type BrowserBenchmarkAcceptance struct {
	LaunchP50TargetMs        int    `json:"launch_p50_target_ms"`
	ActionOverheadTargetMs   int    `json:"action_overhead_target_ms"`
	RSSPerVUserTargetMB      int    `json:"rss_per_vuser_target_mb,omitempty"`
	RSSAttribution           string `json:"rss_attribution"`
	RequiredRawRunCount      int    `json:"required_raw_run_count"`
	RequiredBenchmarkCommand string `json:"required_benchmark_command"`
}

type BrowserBenchmarkDensityEnvelope struct {
	RecommendedCanaryVUsersPerGenerator int    `json:"recommended_canary_vusers_per_generator"`
	MaximumClaimedVUsersPerGenerator    int    `json:"maximum_claimed_vusers_per_generator"`
	PairingModel                        string `json:"pairing_model"`
	ClaimRule                           string `json:"claim_rule"`
}

type BrowserBenchmarkEnvProfile struct {
	ID                string   `json:"id"`
	BrowserEngine     string   `json:"browser_engine"`
	DeviceProfiles    []string `json:"device_profiles"`
	ValidationCommand string   `json:"validation_command"`
	CIExpectation     string   `json:"ci_expectation"`
	Evidence          []string `json:"evidence"`
}

func (s *Server) handleGetBrowserBenchmarkEvidence(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, buildBrowserBenchmarkEvidence(time.Now().UTC()))
}

func buildBrowserBenchmarkEvidence(now time.Time) BrowserBenchmarkEvidenceResponse {
	const benchmarkCommand = "APEXLOAD_BROWSER_BENCHMARKS=1 go test ./pkg/protocol/browser -run '^$' -bench 'BenchmarkBrowser(InitWithDeviceProfile|TouchOrClickActionLatency|RSSPerChromiumDeviceProfile)' -benchmem -count 5 -timeout 30m"
	compileCommand := "go test ./pkg/protocol/browser"
	hardware := BrowserBenchmarkHardware{
		ID:          "generator-canary-4vcpu-8gb",
		Name:        "4 vCPU / 8 GB browser canary generator",
		CPUCores:    4,
		MemoryGB:    8,
		Runtime:     "local, container, or Kubernetes node with browser dependencies installed",
		Description: "Reference host shape for diagnostic browser canary envelopes. Release teams must attach raw benchmark output for their actual generator image before increasing density claims.",
	}

	profiles := []BrowserBenchmarkProfile{
		{
			ID:            "desktop_chromium_1080p",
			Name:          "Desktop Chromium 1080p",
			BrowserEngine: "chromium",
			DeviceProfile: "desktop_1080p",
			DeviceClass:   "desktop",
			Platform:      "desktop",
			Status:        "baseline_artifact_published",
			EvidenceLevel: "code+docs+release_artifact_required",
			ArtifactPath:  browserBenchmarkArtifactEvidence + "#desktop-chromium-1080p",
			Acceptance: BrowserBenchmarkAcceptance{
				LaunchP50TargetMs:        3000,
				ActionOverheadTargetMs:   10,
				RSSPerVUserTargetMB:      250,
				RSSAttribution:           "chromium_cdp_process_snapshot",
				RequiredRawRunCount:      5,
				RequiredBenchmarkCommand: benchmarkCommand,
			},
			DensityEnvelope: browserDiagnosticCanaryEnvelope(),
			BenchmarkCommands: []string{
				compileCommand,
				benchmarkCommand,
			},
			Evidence:                []string{"pkg/protocol/browser/mobile_benchmark_test.go", "docs/MOBILE_BROWSER_BENCHMARK_RUNBOOK.md", browserBenchmarkArtifactEvidence},
			ReleaseGate:             true,
			HighDensityClaimAllowed: false,
			Notes:                   []string{"Use as the desktop UX sentinel beside protocol load groups; do not substitute for HTTP density load."},
		},
		{
			ID:            "mobile_chromium_pixel_8",
			Name:          "Mobile Chromium Pixel 8",
			BrowserEngine: "chromium",
			DeviceProfile: "pixel_8",
			DeviceClass:   "mobile",
			Platform:      "android",
			Status:        "baseline_artifact_published",
			EvidenceLevel: "code+docs+release_artifact_required",
			ArtifactPath:  browserBenchmarkArtifactEvidence + "#mobile-chromium-pixel-8",
			Acceptance: BrowserBenchmarkAcceptance{
				LaunchP50TargetMs:        3000,
				ActionOverheadTargetMs:   10,
				RSSPerVUserTargetMB:      250,
				RSSAttribution:           "chromium_cdp_process_snapshot",
				RequiredRawRunCount:      5,
				RequiredBenchmarkCommand: benchmarkCommand,
			},
			DensityEnvelope: browserDiagnosticCanaryEnvelope(),
			BenchmarkCommands: []string{
				compileCommand,
				benchmarkCommand,
			},
			Evidence:                []string{"pkg/protocol/browser/mobile_benchmark_test.go", "docs/MOBILE_BROWSER_EMULATION_PLAN.md", browserBenchmarkArtifactEvidence},
			ReleaseGate:             true,
			HighDensityClaimAllowed: false,
			Notes:                   []string{"Touch and network shaping evidence must be reviewed with replay screenshots and client metrics before promotion."},
		},
		{
			ID:            "mobile_webkit_iphone_15_pro",
			Name:          "Mobile WebKit iPhone 15 Pro",
			BrowserEngine: "webkit",
			DeviceProfile: "iphone_15_pro",
			DeviceClass:   "mobile",
			Platform:      "ios",
			Status:        "baseline_artifact_published",
			EvidenceLevel: "code+docs+release_artifact_required",
			ArtifactPath:  browserBenchmarkArtifactEvidence + "#mobile-webkit-iphone-15-pro",
			Acceptance: BrowserBenchmarkAcceptance{
				LaunchP50TargetMs:        3000,
				ActionOverheadTargetMs:   10,
				RSSAttribution:           "not_available_for_webkit; use launch/action latency plus host-level memory telemetry",
				RequiredRawRunCount:      5,
				RequiredBenchmarkCommand: benchmarkCommand,
			},
			DensityEnvelope: browserDiagnosticCanaryEnvelope(),
			BenchmarkCommands: []string{
				compileCommand,
				benchmarkCommand,
			},
			Evidence:                []string{"pkg/protocol/browser/mobile_benchmark_test.go", "docs/MOBILE_BROWSER_BENCHMARK_RUNBOOK.md", browserBenchmarkArtifactEvidence},
			ReleaseGate:             true,
			HighDensityClaimAllowed: false,
			Notes:                   []string{"WebKit RSS is not attributed through CDP; keep density bounded by host telemetry and replay reliability."},
		},
	}

	return BrowserBenchmarkEvidenceResponse{
		GeneratedAt:     now,
		ClaimPolicy:     "Browser VUsers are diagnostic canaries paired with protocol load. High-density real-browser load remains a non-claim until controlled benchmark artifacts prove otherwise.",
		HardwareProfile: hardware,
		Profiles:        profiles,
		EnvironmentMatrix: []BrowserBenchmarkEnvProfile{
			{
				ID:                "chromium-desktop-mobile",
				BrowserEngine:     "chromium",
				DeviceProfiles:    []string{"desktop_1080p", "pixel_8", "galaxy_s24"},
				ValidationCommand: compileCommand,
				CIExpectation:     "Normal CI verifies profile registration and benchmark compilation without launching browsers.",
				Evidence:          []string{"pkg/protocol/browser/mobile_benchmark_test.go::TestMobileBrowserBenchmarkProfilesResolve"},
			},
			{
				ID:                "webkit-mobile",
				BrowserEngine:     "webkit",
				DeviceProfiles:    []string{"iphone_15_pro", "iphone_se", "ipad_air"},
				ValidationCommand: compileCommand,
				CIExpectation:     "Normal CI verifies WebKit-backed device profiles remain registered; opt-in release jobs install Playwright WebKit assets and run launch/action benchmarks.",
				Evidence:          []string{"pkg/protocol/browser/mobile_benchmark_test.go::TestMobileBrowserBenchmarkProfilesResolve"},
			},
		},
		Commands: []string{
			compileCommand,
			benchmarkCommand,
		},
		Evidence: []string{
			"/api/v1/system/browser-benchmarks",
			browserBenchmarkArtifactEvidence,
			"docs/MOBILE_BROWSER_BENCHMARK_RUNBOOK.md",
			"pkg/protocol/browser/mobile_benchmark_test.go",
		},
	}
}

func browserDiagnosticCanaryEnvelope() BrowserBenchmarkDensityEnvelope {
	return BrowserBenchmarkDensityEnvelope{
		RecommendedCanaryVUsersPerGenerator: 2,
		MaximumClaimedVUsersPerGenerator:    5,
		PairingModel:                        "Run 1-5 browser canary VUsers beside high-density HTTP/API/JMS groups; use browser evidence for UX health, screenshots, console, network, and Core Web Vitals.",
		ClaimRule:                           "Do not claim high-density browser load. Increase only with release-attached raw benchmark output for the exact generator image and hardware.",
	}
}
