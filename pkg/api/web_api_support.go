package api

import (
	"net/http"
	"sort"
)

type webAPIExecutionSupportEntry struct {
	ID                  string   `json:"id"`
	Category            string   `json:"category"`
	Capability          string   `json:"capability"`
	Status              string   `json:"status"`
	ExecutionStatus     string   `json:"execution_status"`
	Importance          string   `json:"importance"`
	SupportedInputs     []string `json:"supported_inputs,omitempty"`
	RuntimeCoverage     []string `json:"runtime_coverage,omitempty"`
	RemainingGaps       []string `json:"remaining_gaps,omitempty"`
	RecommendedWorkflow string   `json:"recommended_workflow,omitempty"`
}

type webAPIBenchmarkEnvelope struct {
	Protocol          string `json:"protocol"`
	RecommendedVUsers int    `json:"recommended_vusers_per_generator"`
	Envelope          string `json:"envelope"`
	CalibrationSource string `json:"calibration_source"`
	Notes             string `json:"notes,omitempty"`
}

type webAPIDiagnosticSurface struct {
	Protocol string   `json:"protocol"`
	Signals  []string `json:"signals"`
	Exports  []string `json:"exports"`
}

type webAPISampleProject struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Protocol    string   `json:"protocol"`
	Path        string   `json:"path"`
	Coverage    []string `json:"coverage"`
	ReviewReady bool     `json:"review_ready"`
}

func (s *Server) handleWebAPIExecutionSupportMatrix(w http.ResponseWriter, r *http.Request) {
	matrix := webAPIExecutionSupportMatrix()
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix":      matrix,
		"status_counts":       webAPIStatusCounts(matrix),
		"benchmark_envelopes": webAPIBenchmarkEnvelopes(),
		"diagnostics":         webAPIDiagnostics(),
		"sample_projects":     webAPISampleProjects(),
		"notes": []string{
			"Browser VUsers remain diagnostic canaries, not HTTP-density load generators.",
			"Imported OpenAPI, Postman, WSDL, DevWeb, JMeter, HAR, and enterprise script archive assets are review artifacts until validation and replay evidence are attached.",
			"Density envelopes are conservative defaults and should be recalibrated with local generator benchmarks before production capacity reservations.",
		},
	})
}

func webAPIExecutionSupportMatrix() []webAPIExecutionSupportEntry {
	return []webAPIExecutionSupportEntry{
		{
			ID:                  "http-web",
			Category:            "Native Protocol",
			Capability:          "HTTP/Web request execution",
			Status:              "supported",
			ExecutionStatus:     "executable",
			Importance:          "critical",
			SupportedInputs:     []string{"JSON DSL", "HAR", "curl/raw HTTP samples", "OpenAPI imports", "Postman collections", "enterprise script archive Web HTTP/HTML imports"},
			RuntimeCoverage:     []string{"HTTP/1.1", "HTTP/2", "headers", "cookies", "redirects", "TLS/proxy/auth runtime settings", "content checks", "network shaping"},
			RemainingGaps:       []string{"HTTP/3 is not a native execution target"},
			RecommendedWorkflow: "Author or import HTTP scripts, validate runtime settings, replay, then promote to scenarios.",
		},
		{
			ID:                  "openapi-authoring",
			Category:            "Import/Authoring",
			Capability:          "OpenAPI-assisted REST script authoring",
			Status:              "supported",
			ExecutionStatus:     "review_then_executable",
			Importance:          "critical",
			SupportedInputs:     []string{"OpenAPI 3.x JSON/YAML", "Swagger 2.0 JSON/YAML"},
			RuntimeCoverage:     []string{"operation selection", "path/query parameter data source seeding", "request body templates", "auth hints", "status assertions"},
			RemainingGaps:       []string{"schema examples still require human test-data review"},
			RecommendedWorkflow: "Import OpenAPI, review generated data source and auth placeholders, validate, replay, approve.",
		},
		{
			ID:                  "postman-authoring",
			Category:            "Import/Authoring",
			Capability:          "Postman collection import",
			Status:              "supported",
			ExecutionStatus:     "review_then_executable",
			Importance:          "high",
			SupportedInputs:     []string{"Postman v2.1 collection JSON"},
			RuntimeCoverage:     []string{"nested folders", "requests", "collection variables", "headers", "raw/urlencoded/form-data/graphql bodies", "auth placeholders"},
			RemainingGaps:       []string{"pre-request and test scripts require manual review before replay"},
			RecommendedWorkflow: "Import Postman, review variables and auth placeholders, validate, replay, approve.",
		},
		{
			ID:                  "soap-wsdl",
			Category:            "Native Protocol",
			Capability:          "SOAP/WSDL-assisted execution",
			Status:              "supported",
			ExecutionStatus:     "review_then_executable",
			Importance:          "high",
			SupportedInputs:     []string{"JSON DSL SOAP", "WSDL imports"},
			RuntimeCoverage:     []string{"SOAP 1.1/1.2", "SOAPAction", "fault classification", "operation timeline", "WS-Security config surface"},
			RemainingGaps:       []string{"complex XSD payload generation is scaffolded for review, not fully synthesized"},
			RecommendedWorkflow: "Import WSDL, review generated envelopes against service XSD, validate, then replay.",
		},
		{
			ID:                  "java-over-http",
			Category:            "Native Protocol",
			Capability:          "Java-over-HTTP payload execution",
			Status:              "supported",
			ExecutionStatus:     "executable",
			Importance:          "high",
			RuntimeCoverage:     []string{"codec/helper mode", "request/response mapping", "binary result preservation", "extractor metadata"},
			RemainingGaps:       []string{"requires protocol-specific fixtures for each enterprise serialization codec"},
			RecommendedWorkflow: "Use the Java-over-HTTP template, attach codec mappings, replay, and publish diagnostics.",
		},
		{
			ID:                  "websocket",
			Category:            "Native Protocol",
			Capability:          "WebSocket execution",
			Status:              "supported",
			ExecutionStatus:     "executable",
			Importance:          "high",
			SupportedInputs:     []string{"JSON DSL", "DevWeb import"},
			RuntimeCoverage:     []string{"connect", "send", "receive", "close", "timeouts", "subprotocol/origin config"},
			RemainingGaps:       []string{"complex subscription protocols need custom assertions"},
			RecommendedWorkflow: "Author protocol steps or import DevWeb review scripts, replay, and tune message assertions.",
		},
		{
			ID:                  "grpc",
			Category:            "Native Protocol",
			Capability:          "gRPC execution",
			Status:              "supported",
			ExecutionStatus:     "executable",
			Importance:          "high",
			SupportedInputs:     []string{"JSON DSL", "DevWeb unary import"},
			RuntimeCoverage:     []string{"unary", "server stream", "client stream", "bidirectional stream", "reflection/proto config", "metadata"},
			RemainingGaps:       []string{"DevWeb gRPC import still requires schema and metadata review"},
			RecommendedWorkflow: "Attach proto/reflection context, validate target service/method names, replay.",
		},
		{
			ID:                  "jms-solace",
			Category:            "Native Protocol",
			Capability:          "JMS Solace execution",
			Status:              "supported",
			ExecutionStatus:     "executable",
			Importance:          "high",
			RuntimeCoverage:     []string{"publish", "consume", "request/reply", "ack/commit/rollback", "dead-letter diagnostics"},
			RemainingGaps:       []string{"IBM MQ adapter remains cataloged but not registered in this build"},
			RecommendedWorkflow: "Use JMS template with mock mode for local validation, then attach broker settings for controlled runs.",
		},
		{
			ID:                  "browser-canaries",
			Category:            "Browser Testing",
			Capability:          "Browser canary execution",
			Status:              "supported",
			ExecutionStatus:     "diagnostic_canary",
			Importance:          "high",
			RuntimeCoverage:     []string{"desktop/mobile profiles", "client metrics", "screenshots", "browser replay evidence"},
			RemainingGaps:       []string{"not intended for high-density HTTP load generation"},
			RecommendedWorkflow: "Run browser groups as low-density canaries alongside HTTP/API load groups.",
		},
		{
			ID:                  "devweb-import",
			Category:            "Import/Authoring",
			Capability:          "DevWeb construct import and review",
			Status:              "supported",
			ExecutionStatus:     "review_then_executable",
			Importance:          "medium",
			SupportedInputs:     []string{"DevWeb JavaScript", "DevWeb zip"},
			RuntimeCoverage:     []string{"WebRequest", "WebSocket", "gRPC unary", "think time", "action grouping"},
			RemainingGaps:       []string{"dynamic JavaScript runtime compatibility is review-only"},
			RecommendedWorkflow: "Import DevWeb, inspect the construct matrix, split mixed protocol scripts, replay, approve.",
		},
		{
			ID:                  "har-jmeter-enterprise-archive",
			Category:            "Migration",
			Capability:          "HAR, JMeter, and enterprise script archive import",
			Status:              "supported",
			ExecutionStatus:     "review_then_executable",
			Importance:          "critical",
			SupportedInputs:     []string{"HAR", "JMeter JMX", "enterprise script archive Web HTTP/HTML ZIP"},
			RuntimeCoverage:     []string{"shared import envelopes", "review sections", "unsupported element reporting", "runtime setting parity notes"},
			RemainingGaps:       []string{"legacy browser/TruClient stays out of scope"},
			RecommendedWorkflow: "Import, resolve blockers from review sections, validate, replay, then promote.",
		},
	}
}

func webAPIStatusCounts(matrix []webAPIExecutionSupportEntry) map[string]int {
	counts := map[string]int{}
	for _, entry := range matrix {
		counts[entry.Status]++
	}
	return counts
}

func webAPIBenchmarkEnvelopes() []webAPIBenchmarkEnvelope {
	return []webAPIBenchmarkEnvelope{
		{Protocol: "http", RecommendedVUsers: 10000, Envelope: "10k VUsers/generator on 4 vCPU / 8 GB", CalibrationSource: "docs/ENTERPRISE_AUTOSCALING_SIGNALS.md", Notes: "Primary density load protocol."},
		{Protocol: "grpc", RecommendedVUsers: 12000, Envelope: "12k gRPC VUsers/generator", CalibrationSource: "docs/ENTERPRISE_AUTOSCALING_SIGNALS.md"},
		{Protocol: "websocket", RecommendedVUsers: 6000, Envelope: "6k socket VUsers/generator", CalibrationSource: "docs/ENTERPRISE_AUTOSCALING_SIGNALS.md"},
		{Protocol: "soap", RecommendedVUsers: 5000, Envelope: "5k SOAP VUsers/generator", CalibrationSource: "docs/ENTERPRISE_AUTOSCALING_SIGNALS.md"},
		{Protocol: "java_http", RecommendedVUsers: 7000, Envelope: "7k Java-over-HTTP VUsers/generator", CalibrationSource: "docs/ENTERPRISE_AUTOSCALING_SIGNALS.md"},
		{Protocol: "jms-solace", RecommendedVUsers: 3000, Envelope: "3k JMS VUsers/generator", CalibrationSource: "docs/ENTERPRISE_AUTOSCALING_SIGNALS.md"},
		{Protocol: "browser", RecommendedVUsers: 40, Envelope: "20-40 browser canary VUsers/generator", CalibrationSource: "docs/ENTERPRISE_AUTOSCALING_SIGNALS.md", Notes: "Diagnostic canary capacity only."},
	}
}

func webAPIDiagnostics() []webAPIDiagnosticSurface {
	return []webAPIDiagnosticSurface{
		{Protocol: "http", Signals: []string{"status", "TTFB", "TLS/connect", "redirects", "content checks", "network shaping"}, Exports: []string{"run archive", "CSV/JSON reports", "trend comparisons"}},
		{Protocol: "soap", Signals: []string{"operation", "SOAPAction", "fault code", "fault string", "fault detail"}, Exports: []string{"operation timeline", "archive report", "root-cause summaries"}},
		{Protocol: "java_http", Signals: []string{"codec", "helper mode", "mapping failures", "binary result preservation"}, Exports: []string{"protocol diagnostics", "archive report"}},
		{Protocol: "websocket", Signals: []string{"connect latency", "send/receive counts", "timeout", "message size"}, Exports: []string{"live metrics", "archive report"}},
		{Protocol: "grpc", Signals: []string{"service/method", "status", "metadata", "stream message counts"}, Exports: []string{"live metrics", "archive report"}},
		{Protocol: "jms-solace", Signals: []string{"destination", "message id", "correlation id", "dead-letter reason", "delivery count"}, Exports: []string{"run archive", "root-cause summaries"}},
		{Protocol: "browser", Signals: []string{"LCP", "CLS", "FID/INP proxy", "screenshots", "console/network evidence"}, Exports: []string{"browser diagnostics bundle", "archive report"}},
	}
}

func webAPISampleProjects() []webAPISampleProject {
	projects := []webAPISampleProject{
		{ID: "ecommerce-http", Name: "E-commerce HTTP Checkout", Protocol: "http", Path: "examples/web-api/ecommerce-http.json", Coverage: []string{"REST flow", "correlation placeholder", "content assertions"}, ReviewReady: true},
		{ID: "soap-account-service", Name: "SOAP Account Service", Protocol: "soap", Path: "examples/web-api/soap-account-service.json", Coverage: []string{"SOAPAction", "generated envelope", "fault handling"}, ReviewReady: true},
		{ID: "grpc-inventory", Name: "gRPC Inventory", Protocol: "grpc", Path: "examples/web-api/grpc-inventory.json", Coverage: []string{"unary", "server stream", "metadata"}, ReviewReady: true},
		{ID: "websocket-order-feed", Name: "WebSocket Order Feed", Protocol: "websocket", Path: "examples/web-api/websocket-order-feed.json", Coverage: []string{"connect", "send", "receive", "close"}, ReviewReady: true},
		{ID: "jms-order-flow", Name: "JMS Order Flow", Protocol: "jms-solace", Path: "examples/web-api/jms-order-flow.json", Coverage: []string{"publish", "consume", "request/reply", "dead-letter diagnostics"}, ReviewReady: true},
		{ID: "mixed-http-browser-canary", Name: "Mixed HTTP Plus Browser Canary", Protocol: "scenario", Path: "examples/web-api/mixed-http-browser-canary.json", Coverage: []string{"HTTP density group", "browser canary group", "scenario assembly"}, ReviewReady: true},
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	return projects
}
