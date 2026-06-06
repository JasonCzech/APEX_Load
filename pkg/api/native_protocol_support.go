package api

import (
	"net/http"
	"sort"
	"time"
)

const nativeProtocolSupportSchemaVersion = "apexload.native_protocol_support.v1"

type NativeProtocolSupportResponse struct {
	SchemaVersion     string                       `json:"schema_version"`
	GeneratedAt       time.Time                    `json:"generated_at"`
	SupportMatrix     []NativeProtocolSupportEntry `json:"support_matrix"`
	StatusCounts      map[string]int               `json:"status_counts"`
	ExecutionPostures []string                     `json:"execution_postures"`
	Notes             []string                     `json:"notes,omitempty"`
}

type NativeProtocolSupportEntry struct {
	Protocol            string   `json:"protocol"`
	DisplayName         string   `json:"display_name"`
	Status              string   `json:"status"`
	ExecutionPosture    string   `json:"execution_posture"`
	AuthoringSurface    string   `json:"authoring_surface"`
	RuntimeConfig       []string `json:"runtime_config,omitempty"`
	ActionEditor        []string `json:"action_editor,omitempty"`
	Validation          []string `json:"validation,omitempty"`
	ReplayEvidence      []string `json:"replay_evidence,omitempty"`
	ReportExport        []string `json:"report_export,omitempty"`
	ImportSources       []string `json:"import_sources,omitempty"`
	RemainingGaps       []string `json:"remaining_gaps,omitempty"`
	RecommendedWorkflow string   `json:"recommended_workflow,omitempty"`
	Evidence            []string `json:"evidence,omitempty"`
}

func (s *Server) handleNativeProtocolSupportMatrix(w http.ResponseWriter, r *http.Request) {
	matrix := nativeProtocolSupportMatrix()
	writeJSON(w, http.StatusOK, NativeProtocolSupportResponse{
		SchemaVersion:     nativeProtocolSupportSchemaVersion,
		GeneratedAt:       time.Now().UTC(),
		SupportMatrix:     matrix,
		StatusCounts:      nativeProtocolStatusCounts(matrix),
		ExecutionPostures: nativeProtocolExecutionPostures(matrix),
		Notes: []string{
			"This matrix is the code-owned analyst parity contract for native executable protocols.",
			"HTTP, SOAP, Java-over-HTTP, and Browser remain reference baselines; this endpoint focuses on remaining native protocol parity.",
			"JMS IBM MQ remains explicit in bundle governance as review-only until a native adapter is registered.",
		},
	})
}

func nativeProtocolSupportMatrix() []NativeProtocolSupportEntry {
	entries := []NativeProtocolSupportEntry{
		{
			Protocol:            "websocket",
			DisplayName:         "WebSocket",
			Status:              "supported",
			ExecutionPosture:    "native_linux",
			AuthoringSurface:    "blank editor, starter template, DevWeb import review",
			RuntimeConfig:       []string{"base URL", "read/write timeouts", "message size", "subprotocol", "origin", "compression", "reconnect"},
			ActionEditor:        []string{"connect", "send", "receive", "ping", "close", "message assertions", "message extractors"},
			Validation:          []string{"config validation", "action type validation", "preflight variable/secret readiness"},
			ReplayEvidence:      []string{"protocol evidence", "message transcript", "handshake/receive diagnosis", "snapshots on failure"},
			ReportExport:        []string{"replay JSON", "debug session export", "archive transaction evidence"},
			ImportSources:       []string{"JSON DSL", "DevWeb WebSocket"},
			RemainingGaps:       []string{"large binary stream fixtures need customer-specific benchmark coverage"},
			RecommendedWorkflow: "Replay connect/subscribe/receive flows with match assertions before release-gate scenarios.",
			Evidence:            []string{"pkg/protocol/websocket/websocket_test.go", "/api/v1/runtime-settings/support-matrix"},
		},
		{
			Protocol:            "grpc",
			DisplayName:         "gRPC",
			Status:              "supported",
			ExecutionPosture:    "native_linux",
			AuthoringSurface:    "blank editor, starter template, DevWeb import review",
			RuntimeConfig:       []string{"target", "reflection", "proto files", "plaintext/TLS", "authority", "metadata", "timeout", "max receive size"},
			ActionEditor:        []string{"unary", "server stream", "client stream", "bidirectional stream", "metadata", "message count", "status assertions"},
			Validation:          []string{"method target validation", "timeout validation", "proto/reflection readiness"},
			ReplayEvidence:      []string{"protocol evidence", "status metadata", "stream message counts", "contract/status diagnosis"},
			ReportExport:        []string{"replay JSON", "debug session export", "archive transaction evidence"},
			ImportSources:       []string{"JSON DSL", "DevWeb gRPC"},
			RemainingGaps:       []string{"customer-specific protos and reflection policies must be attached per project"},
			RecommendedWorkflow: "Attach proto/reflection evidence, replay unary and stream calls, then approve with status/metadata export.",
			Evidence:            []string{"pkg/protocol/grpc/grpc_test.go", "/api/v1/runtime-settings/support-matrix"},
		},
		{
			Protocol:            "database",
			DisplayName:         "SQL / ODBC-style Database",
			Status:              "supported",
			ExecutionPosture:    "native_linux",
			AuthoringSurface:    "blank editor, starter template, LoadRunner database import",
			RuntimeConfig:       []string{"driver", "DSN secret", "pool limits", "query timeout", "max rows", "read-only guard", "rollback on teardown"},
			ActionEditor:        []string{"begin", "query", "exec", "procedure", "commit", "rollback", "binds", "row/scalar assertions", "result extractors"},
			Validation:          []string{"driver/DSN validation", "SQL presence validation", "read-only mutation review"},
			ReplayEvidence:      []string{"protocol evidence", "SQL and bind summary", "row preview", "transaction state", "connection/SQL diagnosis"},
			ReportExport:        []string{"replay JSON", "debug session export", "archive transaction evidence"},
			ImportSources:       []string{"JSON DSL", "LoadRunner database/ODBC SQL-style imports"},
			RemainingGaps:       []string{"non-SQLite drivers require project-approved driver/client packaging"},
			RecommendedWorkflow: "Replay against non-production fixtures with read-only guard enabled unless write behavior is explicitly approved.",
			Evidence:            []string{"pkg/protocol/database/database_test.go", "docs/COMPETITIVE_COMPARISON.md#loadrunner-import-routing"},
		},
		{
			Protocol:            "network",
			DisplayName:         "Network Protocols",
			Status:              "supported",
			ExecutionPosture:    "native_linux",
			AuthoringSurface:    "blank editor, starter template, LoadRunner network import",
			RuntimeConfig:       []string{"subprotocol", "host/address", "port", "timeout", "TLS", "username/password", "mail sender/recipients", "LDAP base DN"},
			ActionEditor:        []string{"DNS", "TCP", "UDP", "Winsock", "FTP", "SMTP", "POP3", "IMAP", "LDAP", "transcript assertions", "record extractors"},
			Validation:          []string{"subprotocol validation", "endpoint validation", "credential/recipient readiness"},
			ReplayEvidence:      []string{"protocol evidence", "DNS/connect timings", "command transcript", "record evidence", "TLS/auth/connection diagnosis"},
			ReportExport:        []string{"replay JSON", "debug session export", "archive transaction evidence"},
			ImportSources:       []string{"JSON DSL", "LoadRunner DNS/FTP/SMTP/POP3/IMAP/LDAP/Winsock imports"},
			RemainingGaps:       []string{"MAPI and Tuxedo remain review-only external-runner protocols"},
			RecommendedWorkflow: "Replay only against controlled local or allow-listed fixtures, then approve endpoint and credential policy.",
			Evidence:            []string{"pkg/protocol/network/network_test.go", "docs/COMPETITIVE_COMPARISON.md#loadrunner-import-routing"},
		},
		{
			Protocol:            "jms-solace",
			DisplayName:         "JMS Solace",
			Status:              "supported",
			ExecutionPosture:    "native_linux",
			AuthoringSurface:    "blank editor, starter template, component library",
			RuntimeConfig:       []string{"broker URL", "VPN", "destination", "destination type", "reply-to", "acknowledgement", "delivery mode", "timeout", "mock mode"},
			ActionEditor:        []string{"publish", "consume", "request/reply", "ack", "commit", "rollback", "properties", "correlation", "message assertions"},
			Validation:          []string{"broker/destination readiness", "mock broker local replay", "destination policy review"},
			ReplayEvidence:      []string{"protocol evidence", "message envelope", "correlation evidence", "timeout/dead-letter diagnosis"},
			ReportExport:        []string{"replay JSON", "debug session export", "archive transaction evidence"},
			ImportSources:       []string{"JSON DSL", "sample templates"},
			RemainingGaps:       []string{"IBM MQ remains review-only until a registered adapter exists"},
			RecommendedWorkflow: "Use mock replay for script shakeout, then attach broker credentials and export correlation/dead-letter evidence.",
			Evidence:            []string{"pkg/protocol/jms/jms_test.go", "/api/v1/protocol-bundles/support-matrix"},
		},
		{
			Protocol:            "kafka",
			DisplayName:         "Apache Kafka",
			Status:              "supported",
			ExecutionPosture:    "native_linux",
			AuthoringSurface:    "blank editor, starter template, AsyncAPI import",
			RuntimeConfig:       []string{"brokers", "security protocol", "SASL mechanism/credentials", "TLS", "consumer group", "topic", "acks", "compression", "partition strategy", "timeout", "mock mode"},
			ActionEditor:        []string{"produce", "consume", "request/reply", "commit", "seek", "key/partition", "headers", "offset/partition extractors", "message/offset/lag assertions"},
			Validation:          []string{"broker/topic readiness", "security/acks/compression enum validation", "mock broker local replay"},
			ReplayEvidence:      []string{"protocol evidence", "partition/offset evidence", "consumer lag", "correlation evidence", "timeout diagnosis"},
			ReportExport:        []string{"replay JSON", "debug session export", "archive transaction evidence"},
			ImportSources:       []string{"JSON DSL", "AsyncAPI 2.x/3.x"},
			RemainingGaps:       []string{"real franz-go network adapter is opt-in via the kafka_real build tag (default build is mock replay only); Avro/Protobuf schema-registry payloads remain review-only"},
			RecommendedWorkflow: "Author or import from AsyncAPI, shake out against the in-memory broker, then build with -tags kafka_real and attach real brokers/SASL for live execution.",
			Evidence:            []string{"pkg/protocol/kafka/kafka_test.go", "/api/v1/scripts/import/asyncapi/support-matrix"},
		},
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Protocol < entries[j].Protocol
	})
	return entries
}

func nativeProtocolStatusCounts(entries []NativeProtocolSupportEntry) map[string]int {
	counts := make(map[string]int)
	for _, entry := range entries {
		counts[entry.Status]++
	}
	return counts
}

func nativeProtocolExecutionPostures(entries []NativeProtocolSupportEntry) []string {
	set := make(map[string]struct{})
	for _, entry := range entries {
		if entry.ExecutionPosture != "" {
			set[entry.ExecutionPosture] = struct{}{}
		}
	}
	postures := make([]string, 0, len(set))
	for posture := range set {
		postures = append(postures, posture)
	}
	sort.Strings(postures)
	return postures
}
