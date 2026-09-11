package enterprisearchive

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
)

func TestConvertDatabaseRetainsUnsupportedJDBCCredentialsInInertDraft(t *testing.T) {
	const (
		username = "jdbc-review-user"
		password = "jdbc-review-password-canary"
	)
	driver, dsn, reason, ok := javaJDBCDriverAndDSN(
		"jdbc:acme://db.example.test/orders",
		username,
		password,
		true,
	)
	if ok || driver != "" || dsn == "" ||
		reason != "jdbc_connection_driver_unsupported" {
		t.Fatalf(
			"unsupported JDBC projection = driver=%q dsn=%q reason=%q ok=%t",
			driver,
			dsn,
			reason,
			ok,
		)
	}
	actions := []*ParsedAction{{
		Filename: "Actions.java",
		Phase:    "action",
		Functions: []ParsedFunction{
			{
				Name:       "java_sql_connect",
				Filename:   "Actions.java",
				LineNumber: 4,
				Phase:      "action",
				javaJDBCConnection: &javaJDBCConnection{
					Driver:       driver,
					DSN:          dsn,
					ReviewReason: reason,
				},
			},
			{
				Name:       "java_sql_execute",
				Body:       "select 1",
				Filename:   "Actions.java",
				LineNumber: 5,
				Phase:      "action",
			},
		},
	}}
	result, err := ConvertDatabase(actions, ConvertOptions{}, DetectedProtocol{
		CanonicalSourceProtocol: "java_jdbc",
		APEXLoadProtocol:        "database",
		ImportMode:              ImportModeExecutable,
	})
	if err != nil {
		t.Fatalf("ConvertDatabase: %v", err)
	}
	if result.Script == nil || result.Script.Metadata == nil ||
		result.Script.Metadata.Import == nil {
		t.Fatalf("database draft metadata = %#v", result.Script)
	}
	envelope, ok := result.Script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseJavaJDBCSourceKey].(map[string]any)
	connections, connectionsOK := envelope["connections"].([]map[string]any)
	if !ok || !connectionsOK || len(connections) != 1 {
		t.Fatalf("retained JDBC envelope = %#v", envelope)
	}
	decodedDSN, decodeErr := base64.StdEncoding.DecodeString(
		connections[0]["dsn_base64"].(string),
	)
	if decodeErr != nil || string(decodedDSN) != dsn ||
		!strings.Contains(string(decodedDSN), password) {
		t.Fatalf("retained JDBC DSN = %q / %v, want exact protected source", decodedDSN, decodeErr)
	}
	if len(result.Script.Actions) == 0 ||
		result.Script.Actions[0].Metadata["execution_blocker"] !=
			"import_review_required" {
		t.Fatalf("unsupported JDBC draft was not gated: %#v", result.Script.Actions)
	}

	projected, err := scriptdisclosure.Project(result.Script)
	if err != nil {
		t.Fatalf("project disclosure-safe script: %v", err)
	}
	payload, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal disclosure-safe script: %v", err)
	}
	if strings.Contains(string(payload), password) ||
		strings.Contains(string(payload), base64.StdEncoding.EncodeToString([]byte(password))) {
		t.Fatalf("disclosure projection leaked retained JDBC material: %s", payload)
	}
}

func TestConvertDatabaseRetainsUnresolvedJDBCConnectionSource(t *testing.T) {
	tests := []struct {
		name       string
		reason     string
		connection *javaJDBCConnection
		raw        string
		canary     string
	}{
		{
			name:   "unparseable arguments",
			reason: "jdbc_connection_arguments_unparseable",
			connection: &javaJDBCConnection{
				ReviewReason: "jdbc_connection_arguments_unparseable",
			},
			raw:    `Connection c = DriverManager.getConnection("jdbc:acme://db.example.test/orders", "operator", "unparseable-jdbc-password-37a1");`,
			canary: "unparseable-jdbc-password-37a1",
		},
		{
			name:   "dynamic password",
			reason: "jdbc_connection_password_dynamic",
			connection: &javaJDBCConnection{
				ReviewReason: "jdbc_connection_password_dynamic",
			},
			raw:    `Connection c = DriverManager.getConnection("jdbc:postgresql://db.example.test/orders", "operator", "dynamic-jdbc-password-82d4" + suffix());`,
			canary: "dynamic-jdbc-password-82d4",
		},
		{
			name:   "unsupported overload",
			reason: "jdbc_connection_overload_unsupported",
			connection: &javaJDBCConnection{
				ReviewReason: "jdbc_connection_overload_unsupported",
			},
			raw:    `Connection c = DriverManager.getConnection("jdbc:acme://db.example.test/orders", "operator", "overload-jdbc-password-19ce", options);`,
			canary: "overload-jdbc-password-19ce",
		},
		{
			name:       "missing connection analysis",
			reason:     "jdbc_connection_arguments_unavailable",
			connection: nil,
			raw:        `Connection c = DriverManager.getConnection("jdbc:acme://operator:unavailable-jdbc-password-a5e2@db.example.test/orders");`,
			canary:     "unavailable-jdbc-password-a5e2",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ConvertDatabase(
				[]*ParsedAction{{
					Filename: "Actions.java",
					Phase:    "action",
					Functions: []ParsedFunction{{
						Name:               "java_sql_connect",
						Filename:           "Actions.java",
						LineNumber:         4,
						EndLine:            4,
						Phase:              "action",
						RawBlock:           test.raw,
						javaJDBCConnection: test.connection,
					}},
				}},
				ConvertOptions{},
				DetectedProtocol{
					CanonicalSourceProtocol: "java_jdbc",
					APEXLoadProtocol:        "database",
					ImportMode:              ImportModeExecutable,
				},
			)
			if err != nil {
				t.Fatalf("ConvertDatabase: %v", err)
			}
			if result.Script == nil || result.Script.Metadata == nil ||
				result.Script.Metadata.Import == nil {
				t.Fatalf("database draft metadata = %#v", result.Script)
			}
			envelope, ok := result.Script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseJavaJDBCSourceKey].(map[string]any)
			connections, connectionsOK := envelope["connections"].([]map[string]any)
			if !ok || !connectionsOK || len(connections) != 1 {
				t.Fatalf("retained JDBC envelope = %#v", envelope)
			}
			decodedSource, decodeErr := base64.StdEncoding.DecodeString(
				connections[0]["dsn_base64"].(string),
			)
			if decodeErr != nil || string(decodedSource) != test.raw {
				t.Fatalf(
					"retained JDBC source = %q / %v, want exact %q",
					decodedSource,
					decodeErr,
					test.raw,
				)
			}
			if got, _ := connections[0]["reason"].(string); got != test.reason {
				t.Fatalf("retained JDBC reason = %q, want %q", got, test.reason)
			}
			if len(result.Script.Actions) != 1 ||
				result.Script.Actions[0].Type != "source_review" ||
				result.Script.Actions[0].Metadata["execution_blocker"] !=
					"import_review_required" {
				t.Fatalf("unresolved JDBC draft was not gated: %#v", result.Script.Actions)
			}

			projected, err := scriptdisclosure.Project(result.Script)
			if err != nil {
				t.Fatalf("project disclosure-safe script: %v", err)
			}
			payload, err := json.Marshal(projected)
			if err != nil {
				t.Fatalf("marshal disclosure-safe script: %v", err)
			}
			if strings.Contains(string(payload), test.canary) ||
				strings.Contains(
					string(payload),
					base64.StdEncoding.EncodeToString([]byte(test.canary)),
				) {
				t.Fatalf("disclosure projection leaked retained JDBC material: %s", payload)
			}
			label := "Review connection " + test.canary
			if projectedLabel := scriptdisclosure.ProjectLabel(
				label,
				result.Script,
			); strings.Contains(projectedLabel, test.canary) {
				t.Fatalf("label projection leaked retained JDBC material: %q", projectedLabel)
			}
		})
	}
}

func TestPreserveJavaJDBCConnectionDraftsRejectsOversizedSource(t *testing.T) {
	script := &dsl.Script{}
	err := preserveJavaJDBCConnectionDrafts(
		script,
		[]retainedJavaJDBCConnection{{
			DSN:       strings.Repeat("x", maxRetainedJavaJDBCValueBytes+1),
			Reason:    "jdbc_connection_arguments_unparseable",
			SourceRef: "Actions.java line 4",
		}},
	)
	if err == nil {
		t.Fatal("oversized exact JDBC source was accepted")
	}
}

func TestFinalizedJavaJDBCSourceReviewUsesRetainedCredentialEquivalence(t *testing.T) {
	const canary = "jdbc-neutral-review-canary-54e8"
	files := map[string]string{
		"Actions.java": `import java.sql.*;
public class Actions {
  private static final String JDBC_URL = "jdbc:acme://db.example.test/orders";
  public int action() throws Throwable {
    String neutralReviewLabel = "` + canary + `";
    Connection connection = DriverManager.getConnection(JDBC_URL, "operator", "` + canary + `");
    Statement statement = connection.createStatement();
    statement.execute("select 1");
    return neutralReviewLabel.length();
  }
}`,
	}
	program, _ := parseJavaProgram(files)
	parsed := javaDatabaseParsedActions(program, files)
	result, err := ConvertDatabase(parsed, ConvertOptions{}, DetectedProtocol{
		CanonicalSourceProtocol: "java_jdbc",
		APEXLoadProtocol:        "database",
		ImportMode:              ImportModeExecutable,
	})
	if err != nil {
		t.Fatalf("ConvertDatabase: %v", err)
	}
	if err := finalizeEnterpriseArchiveImportResult(
		result,
		archiveFinalizationContext{
			Files:           files,
			ParsedActions:   parsed,
			ProjectFileName: "Actions.java",
		},
	); err != nil {
		t.Fatalf("finalizeEnterpriseArchiveImportResult: %v", err)
	}
	if result.Script == nil || result.Script.Metadata == nil ||
		result.Script.Metadata.Import == nil {
		t.Fatalf("finalized script = %#v", result.Script)
	}
	envelope, ok := result.Script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseJavaJDBCSourceKey].(map[string]any)
	connections, connectionsOK := envelope["connections"].([]map[string]any)
	if !ok || !connectionsOK || len(connections) != 1 {
		t.Fatalf("protected destination envelope = %#v", envelope)
	}
	retained, err := base64.StdEncoding.DecodeString(
		connections[0]["dsn_base64"].(string),
	)
	if err != nil || !strings.Contains(string(retained), canary) {
		t.Fatalf(
			"protected destination envelope lost exact credential: %q / %v",
			retained,
			err,
		)
	}

	reviewPayload, err := json.Marshal(struct {
		Warnings       []string
		Detected       *DetectedProtocol
		FidelityReport *ImportFidelityReport
		ReviewArtifact *ReviewArtifact
		SourceReview   *SourceReview
	}{
		Warnings:       result.Warnings,
		Detected:       result.DetectedProtocol,
		FidelityReport: result.FidelityReport,
		ReviewArtifact: result.ReviewArtifact,
		SourceReview:   result.SourceReview,
	})
	if err != nil {
		t.Fatalf("json.Marshal review response: %v", err)
	}
	if !strings.Contains(string(reviewPayload), canary) {
		t.Fatalf("API-style review response lost retained JDBC credential: %s", reviewPayload)
	}
}
