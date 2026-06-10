package api

import (
	"net/http"
	"sort"
)

type protocolBundleSupportResponse struct {
	SupportMatrix []ProtocolBundleSupportEntry      `json:"support_matrix"`
	StatusCounts  map[string]int                    `json:"status_counts"`
	BundleCounts  map[string]map[string]int         `json:"bundle_counts"`
	RunnerClasses []ProtocolBundleRunnerClass       `json:"runner_classes"`
	Entitlements  []ProtocolBundleEntitlement       `json:"entitlements"`
	Sources       []ProtocolBundleSource            `json:"sources"`
	Notes         []string                          `json:"notes"`
	Workstreams   []ProtocolBundleWorkstreamSummary `json:"workstreams"`
}

type ProtocolBundleSupportEntry struct {
	ID                  string   `json:"id"`
	Bundle              string   `json:"bundle"`
	Protocol            string   `json:"protocol"`
	DisplayName         string   `json:"display_name"`
	Status              string   `json:"status"`
	ExecutionPosture    string   `json:"execution_posture"`
	RunnerRequirement   string   `json:"runner_requirement"`
	EntitlementRequired string   `json:"entitlement_required"`
	Importance          string   `json:"importance"`
	DensityEnvelope     string   `json:"density_envelope,omitempty"`
	SupportedInputs     []string `json:"supported_inputs,omitempty"`
	RuntimeCoverage     []string `json:"runtime_coverage,omitempty"`
	ImportCoverage      []string `json:"import_coverage,omitempty"`
	RemainingGaps       []string `json:"remaining_gaps,omitempty"`
	RecommendedWorkflow string   `json:"recommended_workflow,omitempty"`
	Source              string   `json:"source"`
	Evidence            []string `json:"evidence,omitempty"`
}

type ProtocolBundleRunnerClass struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"display_name"`
	OS              string   `json:"os"`
	PlacementLabels []string `json:"placement_labels,omitempty"`
	Protocols       []string `json:"protocols"`
	CapacitySignal  string   `json:"capacity_signal"`
	Notes           string   `json:"notes,omitempty"`
}

type ProtocolBundleEntitlement struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Bundles     []string `json:"bundles"`
	Protocols   []string `json:"protocols"`
	Default     string   `json:"default"`
	Governance  string   `json:"governance"`
}

type ProtocolBundleSource struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	ValidatedOn string `json:"validated_on"`
}

type ProtocolBundleWorkstreamSummary struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Complete []string `json:"complete,omitempty"`
	Deferred []string `json:"deferred,omitempty"`
}

func (s *Server) handleProtocolBundleSupportMatrix(w http.ResponseWriter, r *http.Request) {
	matrix := protocolBundleSupportMatrix()
	writeJSON(w, http.StatusOK, protocolBundleSupportResponse{
		SupportMatrix: matrix,
		StatusCounts:  protocolBundleStatusCounts(matrix),
		BundleCounts:  protocolBundleCounts(matrix),
		RunnerClasses: protocolBundleRunnerClasses(),
		Entitlements:  protocolBundleEntitlements(),
		Sources:       protocolBundleSources(),
		Notes: []string{
			"Execution posture is intentionally separate from entitlement so projects can govern bundle usage before every protocol has a native runtime.",
			"Desktop/client-heavy protocols such as SAP GUI, Oracle NCA, MAPI, Tuxedo, and Template VUsers require governed runner pools rather than the high-density Linux HTTP generator path.",
			"Density envelopes are planning defaults until benchmark artifacts are published for the target runner class and customer fixture.",
		},
		Workstreams: protocolBundleWorkstreams(matrix),
	})
}

func protocolBundleSupportMatrix() []ProtocolBundleSupportEntry {
	entries := []ProtocolBundleSupportEntry{
		{
			ID:                  "database-sql",
			Bundle:              "database",
			Protocol:            "database",
			DisplayName:         "SQL / ODBC-style Database",
			Status:              "supported",
			ExecutionPosture:    "native_linux",
			RunnerRequirement:   "go_generator",
			EntitlementRequired: "database",
			Importance:          "P0",
			DensityEnvelope:     "500-2000 database VUsers per generator pending connection/query benchmark",
			SupportedInputs:     []string{"APEX Load database DSL", "enterprise script archive database import review"},
			RuntimeCoverage:     []string{"connection lifecycle", "query", "exec", "transactions", "binds", "row-count and rows-affected assertions", "read-only mutation guard"},
			ImportCoverage:      []string{"SQL extraction", "connection review", "transaction review", "unsupported API preservation"},
			RemainingGaps:       []string{"ODBC and Oracle client bridges require customer/client prerequisite validation beyond the native database/sql execution path"},
			RecommendedWorkflow: "Author or import database scripts, validate read/write classification, replay against non-production fixtures, then approve.",
			Source:              "license-bundles-2023",
			Evidence:            []string{"pkg/protocol/database/database_test.go", "pkg/dsl/validate_test.go::TestValidateScriptIssues_DatabaseProtocolAllowsTransactionActionsWithoutTarget", "docs/COMPETITIVE_COMPARISON.md#protocol-bundle-coverage"},
		},
		{
			ID:                  "database-oracle-2tier",
			Bundle:              "database",
			Protocol:            "oracle_2_tier",
			DisplayName:         "Oracle 2-Tier",
			Status:              "external_runner_required",
			ExecutionPosture:    "client_bridge",
			RunnerRequirement:   "oracle_client_generator",
			EntitlementRequired: "database",
			Importance:          "P0",
			DensityEnvelope:     "customer calibrated; depends on Oracle client and server session policy",
			ImportCoverage:      []string{"Oracle client prerequisite review", "SQL and transaction preservation"},
			RemainingGaps:       []string{"native Oracle client image, wallet/auth prerequisites, and benchmark evidence"},
			RecommendedWorkflow: "Classify and preserve imported Oracle scripts until a project-approved Oracle client runner is available.",
			Source:              "license-bundles-2023",
			Evidence:            []string{"docs/COMPETITIVE_COMPARISON.md#protocol-bundle-coverage"},
		},
		networkBundleEntry("network-dns", "dns", "DNS", "supported", "native_linux", "go_generator", "P0", "10k DNS VUsers per generator pending benchmark", []string{"A/AAAA/CNAME/MX/NS/TXT lookups", "response record diagnostics", "DNS timing telemetry"}),
		networkBundleEntry("network-ftp", "ftp", "FTP", "supported", "native_linux", "go_generator", "P0", "2k-5k FTP control-command VUsers per generator pending file-size mix", []string{"connect", "login", "NOOP/raw control commands", "response diagnostics"}),
		networkBundleEntry("network-smtp", "smtp", "SMTP", "supported", "native_linux", "go_generator", "P0", "2k-5k SMTP VUsers per generator pending message-size mix", []string{"send", "AUTH PLAIN", "message template diagnostics", "response diagnostics"}),
		networkBundleEntry("network-pop3", "pop3", "POP3", "supported", "native_linux", "go_generator", "P0", "2k-5k POP3 VUsers per generator pending mailbox fixture", []string{"connect", "auth", "raw POP3 command execution", "response diagnostics"}),
		networkBundleEntry("network-imap", "imap", "IMAP", "supported", "native_linux", "go_generator", "P0", "2k-5k IMAP VUsers per generator pending mailbox fixture", []string{"capability", "login", "tagged command execution", "logout"}),
		networkBundleEntry("network-ldap", "ldap", "LDAP", "supported", "native_linux", "go_generator", "P0", "1k-3k LDAP VUsers per generator pending directory fixture", []string{"bind", "search", "attribute diagnostics", "entry count reporting"}),
		networkBundleEntry("network-winsock", "winsock", "Windows Sockets", "supported", "native_linux", "go_generator", "P1", "5k TCP/UDP socket VUsers per generator pending payload-size mix", []string{"TCP connect/send/receive/close", "UDP send/receive", "binary payload diagnostics"}),
		{
			ID:                  "network-mapi",
			Bundle:              "network",
			Protocol:            "mapi",
			DisplayName:         "MAPI",
			Status:              "review_only",
			ExecutionPosture:    "external_runner_required",
			RunnerRequirement:   "windows_mail_client_runner",
			EntitlementRequired: "network",
			Importance:          "P1",
			DensityEnvelope:     "customer calibrated; Windows/client constrained",
			ImportCoverage:      []string{"MAPI script classification", "client prerequisite review", "unsupported fragment preservation"},
			RemainingGaps:       []string{"native MAPI runner and Microsoft client dependency model"},
			RecommendedWorkflow: "Classify imported assets and prefer HTTP/Graph/EWS alternatives when performance objective allows.",
			Source:              "license-bundles-2025",
			Evidence:            []string{"pkg/api/protocol_bundle_support.go", "docs/COMPETITIVE_COMPARISON.md#protocol-bundle-coverage"},
		},
		{
			ID:                  "network-tuxedo",
			Bundle:              "network",
			Protocol:            "tuxedo",
			DisplayName:         "Tuxedo",
			Status:              "review_only",
			ExecutionPosture:    "client_bridge",
			RunnerRequirement:   "tuxedo_client_generator",
			EntitlementRequired: "network",
			Importance:          "P1",
			DensityEnvelope:     "customer calibrated; Tuxedo client constrained",
			ImportCoverage:      []string{"Tuxedo asset classification", "client prerequisite review", "unsupported fragment preservation"},
			RemainingGaps:       []string{"customer-backed Tuxedo client libraries and sample assets"},
			RecommendedWorkflow: "Preserve source artifacts and require explicit feasibility decision before execution claims.",
			Source:              "license-bundles-2025",
			Evidence:            []string{"pkg/api/protocol_bundle_support.go", "docs/COMPETITIVE_COMPARISON.md#protocol-bundle-coverage"},
		},
		oebEntry("oeb-devweb", "devweb", "DevWeb", "supported", "review_then_executable", "go_generator", "P0", []string{"existing DevWeb import support matrix", "Oracle E-Business review caveats"}),
		oebEntry("oeb-jmeter", "jmeter", "JMeter", "supported", "review_then_executable", "go_generator", "P0", []string{"existing JMeter import", "Oracle E-Business review caveats"}),
		oebEntry("oeb-gatling", "gatling", "Gatling", "review_only", "import_review", "go_generator", "P1", []string{"Gatling scripts are classified for Oracle E-Business migration review"}),
		oebEntry("oeb-oracle-web", "oracle_web", "Oracle-Web", "supported", "review_then_executable", "go_generator", "P1", []string{"Oracle session/correlation pack", "HTTP-backed execution", "package review endpoint", "scenario templates"}),
		oebEntry("oeb-siebel-web", "siebel_web", "Siebel-Web", "supported", "review_then_executable", "go_generator", "P1", []string{"Siebel session/correlation pack", "HTTP-backed execution", "package review endpoint", "scenario templates"}),
		oebEntry("oeb-web-http", "web_http_html", "Web-HTTP/HTML", "supported", "review_then_executable", "go_generator", "P0", []string{"enterprise script archive Web HTTP/HTML import and runtime settings parity"}),
		{
			ID:                  "oeb-oracle-nca",
			Bundle:              "oracle_e_business",
			Protocol:            "oracle_nca",
			DisplayName:         "Oracle NCA",
			Status:              "review_only",
			ExecutionPosture:    "external_runner_required",
			RunnerRequirement:   "oracle_forms_client_runner",
			EntitlementRequired: "oracle_e_business",
			Importance:          "P1",
			DensityEnvelope:     "customer calibrated; client/protocol constrained",
			ImportCoverage:      []string{"NCA script classification", "source-fragment preservation", "client prerequisite findings"},
			RemainingGaps:       []string{"Oracle Forms/NCA runtime feasibility and representative fixture"},
			RecommendedWorkflow: "Preserve NCA imports as governed review artifacts until client runner feasibility is approved.",
			Source:              "license-bundles-2025",
			Evidence:            []string{"pkg/api/oracle_ebusiness_review.go", "docs/COMPETITIVE_COMPARISON.md#protocol-bundle-coverage"},
		},
		templateEntry("template-c", "c_vuser", "C Vuser", "review_only", "template_worker", "P1"),
		templateEntry("template-cpp-dotnet", "cpp_dotnet", "C++ .NET", "review_only", "template_worker", "P1"),
		templateEntry("template-csharp-dotnet", "csharp_dotnet", "C# .NET", "review_only", "template_worker", "P1"),
		templateEntry("template-java", "java_vuser", "Java Vuser", "review_only", "template_worker", "P1"),
		templateEntry("template-vb-dotnet", "vb_dotnet", "VB .NET", "review_only", "template_worker", "P1"),
		{
			ID:                  "sap-gui-foundation",
			Bundle:              "sap_gui_foundation",
			Protocol:            "sap_gui",
			DisplayName:         "SAP GUI",
			Status:              "external_runner_required",
			ExecutionPosture:    "desktop_client",
			RunnerRequirement:   "windows_sap_gui_runner",
			EntitlementRequired: "sap_gui_foundation",
			Importance:          "P0",
			DensityEnvelope:     "low-density desktop sessions; calibrated by Windows runner capacity",
			SupportedInputs:     []string{"enterprise script archive SAP GUI import review", "future SAP GUI DSL"},
			RuntimeCoverage:     []string{"runner prerequisite validation", "screen/object identity preservation", "status-bar diagnostics"},
			ImportCoverage:      []string{"transactions", "screen/object identifiers", "synchronization points", "parameters", "unsupported calls"},
			RemainingGaps:       []string{"Windows runner bridge and SAP GUI client version certification"},
			RecommendedWorkflow: "Govern entitlement first, import for review, then place only on SAP GUI-capable Windows runner pools.",
			Source:              "sap-solutions-2024",
			Evidence:            []string{"docs/COMPETITIVE_COMPARISON.md#protocol-bundle-coverage"},
		},
		{
			ID:                  "sap-web",
			Bundle:              "sap_gui_foundation",
			Protocol:            "sap_web",
			DisplayName:         "SAP-Web / Fiori / WebGUI",
			Status:              "supported",
			ExecutionPosture:    "review_then_executable",
			RunnerRequirement:   "go_generator_or_browser_canary",
			EntitlementRequired: "sap_gui_foundation",
			Importance:          "P1",
			DensityEnvelope:     "HTTP density load plus low-density browser canaries",
			SupportedInputs:     []string{"HTTP/Web DSL", "browser canary DSL", "enterprise script archive Web HTTP/HTML import"},
			RuntimeCoverage:     []string{"HTTP-backed SAP-Web load", "browser canary diagnostics", "network/client evidence"},
			ImportCoverage:      []string{"SAP Web/Fiori correlation review", "parameter fit", "runtime settings parity"},
			RemainingGaps:       []string{"does not replace native SAP GUI desktop protocol parity"},
			RecommendedWorkflow: "Use HTTP/API load for density and browser canaries for SAP Web/Fiori client evidence.",
			Source:              "sap-solutions-2024",
			Evidence:            []string{"docs/COMPETITIVE_COMPARISON.md#protocol-bundle-coverage"},
		},
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Bundle == entries[j].Bundle {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].Bundle < entries[j].Bundle
	})
	return entries
}

func networkBundleEntry(id, protocolName, displayName, status, posture, runner, importance, envelope string, coverage []string) ProtocolBundleSupportEntry {
	return ProtocolBundleSupportEntry{
		ID:                  id,
		Bundle:              "network",
		Protocol:            protocolName,
		DisplayName:         displayName,
		Status:              status,
		ExecutionPosture:    posture,
		RunnerRequirement:   runner,
		EntitlementRequired: "network",
		Importance:          importance,
		DensityEnvelope:     envelope,
		SupportedInputs:     []string{"APEX Load network DSL", "enterprise script archive network import review"},
		RuntimeCoverage:     coverage,
		ImportCoverage:      []string{"protocol classification", "connection settings", "credentials review", "unsupported source preservation"},
		RecommendedWorkflow: "Author or import network scripts, validate credentials and endpoint policy, replay against controlled fixtures, then approve.",
		Source:              "license-bundles-2025",
		Evidence:            []string{"pkg/protocol/network/network_test.go", "pkg/dsl/validate_test.go::TestValidateScriptIssues_NetworkProtocolAllowsSupportedActions", "docs/COMPETITIVE_COMPARISON.md#protocol-bundle-coverage"},
	}
}

func oebEntry(id, protocolName, displayName, status, posture, runner, importance string, coverage []string) ProtocolBundleSupportEntry {
	return ProtocolBundleSupportEntry{
		ID:                  id,
		Bundle:              "oracle_e_business",
		Protocol:            protocolName,
		DisplayName:         displayName,
		Status:              status,
		ExecutionPosture:    posture,
		RunnerRequirement:   runner,
		EntitlementRequired: "oracle_e_business",
		Importance:          importance,
		DensityEnvelope:     "inherits HTTP/Web/API density envelope unless client-runner posture is required",
		SupportedInputs:     []string{"Oracle E-Business migration review", "HTTP/Web/API DSL"},
		RuntimeCoverage:     coverage,
		ImportCoverage:      []string{"Oracle/Siebel token review", "parameter fit", "runtime setting parity", "unsupported source preservation"},
		RecommendedWorkflow: "Import or author HTTP-backed Oracle E-Business flows, apply package correlation review, replay, then promote.",
		Source:              "license-bundles-2025",
		Evidence:            []string{"pkg/api/oracle_ebusiness_review.go", "pkg/api/oracle_ebusiness_review_test.go", "docs/COMPETITIVE_COMPARISON.md#protocol-bundle-coverage"},
	}
}

func templateEntry(id, protocolName, displayName, status, runner string, importance string) ProtocolBundleSupportEntry {
	return ProtocolBundleSupportEntry{
		ID:                  id,
		Bundle:              "templates",
		Protocol:            protocolName,
		DisplayName:         displayName,
		Status:              status,
		ExecutionPosture:    "external_worker",
		RunnerRequirement:   runner,
		EntitlementRequired: "templates",
		Importance:          importance,
		DensityEnvelope:     "worker-image calibrated; arbitrary code execution is isolated from generator hot path",
		SupportedInputs:     []string{"enterprise script archive Template VUser import review", "future template worker SDK"},
		RuntimeCoverage:     []string{"lifecycle contract", "parameter bridge", "transaction metrics", "worker log capture"},
		ImportCoverage:      []string{"language detection", "file preservation", "compile hint preservation", "unsupported API reporting"},
		RemainingGaps:       []string{"worker SDK/runtime images and language-specific sandboxing"},
		RecommendedWorkflow: "Import for review and execute only through signed, project-approved worker images.",
		Source:              "license-bundles-2025",
		Evidence:            []string{"docs/COMPETITIVE_COMPARISON.md#protocol-bundle-coverage"},
	}
}

func protocolBundleStatusCounts(entries []ProtocolBundleSupportEntry) map[string]int {
	counts := make(map[string]int)
	for _, entry := range entries {
		counts[entry.Status]++
	}
	return counts
}

func protocolBundleCounts(entries []ProtocolBundleSupportEntry) map[string]map[string]int {
	counts := make(map[string]map[string]int)
	for _, entry := range entries {
		if counts[entry.Bundle] == nil {
			counts[entry.Bundle] = make(map[string]int)
		}
		counts[entry.Bundle][entry.Status]++
	}
	return counts
}

func protocolBundleRunnerClasses() []ProtocolBundleRunnerClass {
	return []ProtocolBundleRunnerClass{
		{ID: "go_generator", DisplayName: "Linux Go Generator", OS: "linux", PlacementLabels: []string{"runner_os=linux", "runtime=go"}, Protocols: []string{"database", "dns", "ftp", "smtp", "pop3", "imap", "ldap", "winsock", "oracle_web", "siebel_web", "web_http_html"}, CapacitySignal: "active_slots/reserved_slots plus protocol density envelope", Notes: "High-density protocol execution remains here."},
		{ID: "oracle_client_generator", DisplayName: "Oracle Client Generator", OS: "linux_or_windows", PlacementLabels: []string{"client=oracle", "runtime=go_or_bridge"}, Protocols: []string{"oracle_2_tier"}, CapacitySignal: "client sessions and DB connection policy"},
		{ID: "windows_sap_gui_runner", DisplayName: "Windows SAP GUI Runner", OS: "windows", PlacementLabels: []string{"runner_os=windows", "client=sap_gui"}, Protocols: []string{"sap_gui"}, CapacitySignal: "desktop_session_capacity", Notes: "Low-density desktop execution only."},
		{ID: "template_worker", DisplayName: "Template Worker", OS: "linux_or_windows", PlacementLabels: []string{"runtime=template_worker"}, Protocols: []string{"c_vuser", "cpp_dotnet", "csharp_dotnet", "java_vuser", "vb_dotnet"}, CapacitySignal: "worker slots and sandbox policy"},
		{ID: "client_bridge", DisplayName: "Client Bridge", OS: "linux_or_windows", PlacementLabels: []string{"runtime=client_bridge"}, Protocols: []string{"mapi", "tuxedo", "oracle_nca"}, CapacitySignal: "client prerequisite capacity"},
	}
}

func protocolBundleEntitlements() []ProtocolBundleEntitlement {
	return []ProtocolBundleEntitlement{
		{ID: "database", DisplayName: "Database Protocol Bundle", Bundles: []string{"database"}, Protocols: []string{"database", "oracle_2_tier"}, Default: "disabled_until_project_admin_enables", Governance: "Requires project-admin approval and non-production data classification for write-capable scripts."},
		{ID: "network", DisplayName: "Network Protocol Bundle", Bundles: []string{"network"}, Protocols: []string{"dns", "ftp", "smtp", "pop3", "imap", "ldap", "winsock", "mapi", "tuxedo"}, Default: "disabled_until_project_admin_enables", Governance: "Requires endpoint allow-list and credential review for mail/directory protocols."},
		{ID: "oracle_e_business", DisplayName: "Oracle E-Business Protocol Bundle", Bundles: []string{"oracle_e_business"}, Protocols: []string{"devweb", "jmeter", "gatling", "oracle_nca", "oracle_web", "siebel_web", "web_http_html"}, Default: "disabled_until_project_admin_enables", Governance: "Requires packaged-app correlation review before promotion."},
		{ID: "templates", DisplayName: "Templates Protocol Bundle", Bundles: []string{"templates"}, Protocols: []string{"c_vuser", "cpp_dotnet", "csharp_dotnet", "java_vuser", "vb_dotnet"}, Default: "disabled_until_system_admin_enables", Governance: "Requires signed worker images and arbitrary-code execution approval."},
		{ID: "sap_gui_foundation", DisplayName: "SAP GUI Foundation Entitlement", Bundles: []string{"sap_gui_foundation"}, Protocols: []string{"sap_gui", "sap_web"}, Default: "disabled_until_project_admin_enables", Governance: "Requires SAP GUI runner prerequisites for native desktop execution."},
	}
}

func protocolBundleSources() []ProtocolBundleSource {
	return []ProtocolBundleSource{
		{ID: "license-bundles-2025", Title: "OpenText Professional Performance Engineering and OpenText Enterprise Performance Engineering License Bundles", URL: "https://www.opentext.com/media/solution-overview/opentext-professional-performance-engineering-and-opentext-enterprise-performance-engineering-license-bundles-slo-en.pdf", ValidatedOn: "2026-05-19"},
		{ID: "license-bundles-2023", Title: "LoadRunner Professional and LoadRunner Enterprise 2023 License Bundles", URL: "https://www.opentext.com/assets/documents/en-US/pdf/loadrunner-professional-and-loadrunner-enterprise-license-bundles-flyer-en.pdf", ValidatedOn: "2026-05-19"},
		{ID: "sap-solutions-2024", Title: "OpenText Performance Engineering for SAP Solutions", URL: "https://www.opentext.com/media/data-sheet/opentext-performance-engineering-for-sap-solutions-ds-en.pdf", ValidatedOn: "2026-05-19"},
	}
}

func protocolBundleWorkstreams(entries []ProtocolBundleSupportEntry) []ProtocolBundleWorkstreamSummary {
	statusByBundle := make(map[string][]ProtocolBundleSupportEntry)
	for _, entry := range entries {
		statusByBundle[entry.Bundle] = append(statusByBundle[entry.Bundle], entry)
	}
	return []ProtocolBundleWorkstreamSummary{
		workstreamSummary("B0", "Bundle Registry, Entitlement, And Governance", "implemented", []string{"code-owned catalog", "support-matrix API", "runner classes", "entitlement definitions"}, nil),
		bundleWorkstreamSummary("DB1", "Database Protocol Bundle", statusByBundle["database"]),
		bundleWorkstreamSummary("NET1", "Network Protocol Bundle", statusByBundle["network"]),
		bundleWorkstreamSummary("OEB1", "Oracle E-Business Protocol Bundle", statusByBundle["oracle_e_business"]),
		workstreamSummary("TPL1", "Templates Protocol Bundle", "planned", nil, []string{"language worker runtimes", "signed worker image pipeline"}),
		workstreamSummary("SAP1", "SAP GUI Foundation Entitlement", "planned", nil, []string{"Windows SAP GUI runner bridge", "SAP GUI client certification"}),
	}
}

func bundleWorkstreamSummary(id, name string, entries []ProtocolBundleSupportEntry) ProtocolBundleWorkstreamSummary {
	complete := make([]string, 0)
	deferred := make([]string, 0)
	status := "implemented"
	for _, entry := range entries {
		switch entry.Status {
		case "supported", "review_only", "external_runner_required":
			complete = append(complete, entry.DisplayName+": "+entry.Status)
		default:
			status = "in_progress"
			deferred = append(deferred, entry.DisplayName+": "+entry.Status)
		}
	}
	return workstreamSummary(id, name, status, complete, deferred)
}

func workstreamSummary(id, name, status string, complete []string, deferred []string) ProtocolBundleWorkstreamSummary {
	sort.Strings(complete)
	sort.Strings(deferred)
	return ProtocolBundleWorkstreamSummary{ID: id, Name: name, Status: status, Complete: complete, Deferred: deferred}
}
