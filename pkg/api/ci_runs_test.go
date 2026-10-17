package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

func ciTestScript(baseURL string) *dsl.Script {
	return &dsl.Script{
		Name: "CI smoke", Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: baseURL},
		Actions: []dsl.Action{{Name: "health", Type: "request", Method: http.MethodGet, Target: "/health"}},
	}
}

func ciTestRunRequest() ciRunCreateRequest {
	return ciRunCreateRequest{
		ProjectID: "project-ci",
		Execution: ciRunExecutionRequest{VUsers: 5, Duration: "2m", RampUp: "10s"},
		Goals:     ciRunGoalsRequest{RequireResults: true},
		Source:    ciTestRunSource("1"),
	}
}

func ciTestRunSource(attempt string) ciRunSource {
	return ciRunSource{
		Provider: "github", ServerURL: "https://github.com", APIURL: "https://api.github.com",
		Repository: "octo/apex", RepositoryID: "12345", Ref: "refs/heads/main",
		SHA:         "0123456789abcdef0123456789abcdef01234567",
		WorkflowRef: "octo/apex/.github/workflows/performance.yml@refs/heads/main",
		RunID:       "9001", RunAttempt: attempt, Actor: "octocat", EventName: "push",
		Environment: "production", RunnerEnvironment: "github-hosted", ScriptPath: "tests/performance.json",
	}
}

func persistValidCIRunArchive(t *testing.T, store storage.Store, test *ManagedTest, transactionCount int64, slaEvaluated, slaBreached bool, slaDetails []string) *storage.RunArchiveRecord {
	t.Helper()
	writer, err := newRunArchiveWriter(test, RunArchiveProvenance{
		ProjectID:   test.ProjectID,
		RequestedAt: test.CreatedAt,
	})
	if err != nil {
		t.Fatalf("newRunArchiveWriter: %v", err)
	}
	started := time.Now().UTC()
	for index := int64(0); index < transactionCount; index++ {
		if err := writer.RecordResult(&protocol.Result{
			ActionName: "ci-evidence", StartTime: started.Add(time.Duration(index) * time.Millisecond),
			Duration: time.Millisecond, Success: true,
		}); err != nil {
			t.Fatalf("RecordResult: %v", err)
		}
	}
	record, err := writer.Finalize(strings.ToUpper(strings.TrimSpace(test.Status)), started.Add(time.Second), strings.TrimSpace(test.Error), slaEvaluated, slaBreached, slaDetails)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatalf("SaveRunArchive: %v", err)
	}
	return record
}

func rewriteCIRunArchiveManifest(t *testing.T, store storage.Store, record *storage.RunArchiveRecord, mutate func(*RunArchiveManifest)) {
	t.Helper()
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		t.Fatalf("loadRunArchiveManifest: %v", err)
	}
	mutate(manifest)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal rewritten archive manifest: %v", err)
	}
	if err := os.WriteFile(record.ManifestPath, data, 0o644); err != nil {
		t.Fatalf("rewrite archive manifest: %v", err)
	}
	record.ManifestSHA256 = sha256BytesHex(data)
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatalf("save rewritten archive trust anchor: %v", err)
	}
}

func createTerminalCIContractTest(t *testing.T, server *Server, store storage.Store, status, runError string) *ManagedTest {
	t.Helper()
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-terminal-evidence-script", ProjectID: "project-ci", Name: "Terminal evidence", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Method: "GET", Target: "/health"}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("1"), ScriptIdentity: "script_id:" + script.ID,
		RequireResults: true, PolicyRevision: ciTestActionsPolicy().Revision, IdempotencySHA: sha256String("terminal-evidence-" + status),
	}
	key, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatalf("encodeCIRunMetadata: %v", err)
	}
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Terminal evidence", ProjectID: "project-ci", ScriptID: script.ID, VUsers: 1, Duration: "1m", MaterializationKey: key,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	now := time.Now().UTC()
	server.runner.mu.Lock()
	managed := server.runner.tests[testRun.ID]
	managed.Status = strings.ToUpper(strings.TrimSpace(status))
	managed.StartedAt = ptr(now.Add(-time.Second))
	managed.EndedAt = ptr(now)
	managed.Error = runError
	managed.LiveMetrics = &LiveMetrics{TotalTx: 9, TPS: 9}
	server.runner.mu.Unlock()
	if err := store.UpdateTestStatus(testRun.ID, managed.Status, &now, runError); err != nil {
		t.Fatalf("UpdateTestStatus: %v", err)
	}
	terminal, ok := server.runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("GetTest(%s) failed", testRun.ID)
	}
	return terminal
}

func ciRunSourceTestPolicy() storage.GitHubActionsPolicy {
	return ciTestActionsPolicy()
}

type staticCIProjectReadStore struct {
	project *storage.Project
}

func (s *staticCIProjectReadStore) GetProject(id string) (*storage.Project, error) {
	if s == nil || s.project == nil || normalizeProjectID(s.project.ID) != normalizeProjectID(id) {
		return nil, nil
	}
	clone := *s.project
	return &clone, nil
}

func newRevokedPendingCIRunner(t *testing.T, count int) (*TestRunner, []string) {
	t.Helper()
	policy := ciRunSourceTestPolicy()
	metadata := ciRunMetadata{
		SchemaVersion:  ciRunSchemaVersion,
		Source:         ciTestRunSource("1"),
		ScriptIdentity: "script_id:revoked-pending",
		PolicyRevision: policy.Revision,
		IdempotencySHA: sha256String("revoked-pending"),
	}
	key, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	policy.Enabled = false
	runner := NewTestRunner(count+1, NewHub(), nil)
	runner.projectStore = &staticCIProjectReadStore{project: &storage.Project{
		ID:   "project-ci",
		Name: "CI",
		Settings: storage.ProjectSettings{GitHub: storage.GitHubProjectPolicy{
			Actions: policy,
		}},
	}}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	ids := make([]string, 0, count)
	runner.mu.Lock()
	for index := 0; index < count; index++ {
		id := fmt.Sprintf("revoked-pending-%04d", index)
		ids = append(ids, id)
		runner.tests[id] = &ManagedTest{
			ID:                 id,
			Name:               id,
			ProjectID:          "project-ci",
			Status:             "PENDING",
			CreatedAt:          pendingSince,
			Duration:           time.Minute,
			TotalVUsers:        1,
			PendingSince:       &pendingSince,
			PendingReason:      "awaiting capacity",
			MaterializationKey: key,
			Groups:             []TestGroup{{Name: "load", VUsers: 1}},
		}
	}
	runner.mu.Unlock()
	return runner, ids
}

func TestValidateCIRunRequestEnforcesPolicyAndSecretBoundary(t *testing.T) {
	basePolicy := ciRunSourceTestPolicy()
	tests := []struct {
		name      string
		mutate    func(*ciRunCreateRequest, *storage.GitHubActionsPolicy, *dsl.Script)
		wantError string
	}{
		{name: "allowed"},
		{name: "vuser cap", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Execution.VUsers = 51
		}, wantError: "vusers exceeds"},
		{name: "duration cap", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Execution.Duration = "31m"
		}, wantError: "duration exceeds"},
		{name: "protocol", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Protocol = "kafka"
		}, wantError: "protocol"},
		{name: "target host", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Config.BaseURL = "https://attacker.example"
		}, wantError: "target host"},
		{name: "dynamic target", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Config.BaseURL = "https://{{env.TARGET_HOST}}"
		}, wantError: "dynamic target"},
		{name: "template action target", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Actions[0].Target = "{{var.target_url}}"
		}, wantError: "dynamic target"},
		{name: "fixed host with templated path", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Actions[0].Target = "https://api.example.test/{{var.path}}?q={{var.query}}"
		}},
		{name: "kafka mock is not authoritative", mutate: func(_ *ciRunCreateRequest, policy *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Protocol = "kafka"
			policy.AllowedProtocols = append(policy.AllowedProtocols, "kafka")
			policy.AllowedTargetHosts = append(policy.AllowedTargetHosts, "broker.example.test")
			script.Config.Kafka.Brokers = []string{"broker.example.test:9092"}
			script.Config.Kafka.Mock = true
		}, wantError: "kafka_real"},
		{name: "OAuth token target", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Actions = append(script.Actions, dsl.Action{Name: "token", Type: "http_oauth2_token", Params: map[string]any{"token_url": "https://attacker.example/token"}})
		}, wantError: "target host"},
		{name: "sequence child target", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Actions = []dsl.Action{{Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"name": "child", "type": "request", "method": "GET", "target": "https://attacker.example/child"}}}}}
		}, wantError: "target host"},
		{name: "browser open-window param target", mutate: func(_ *ciRunCreateRequest, policy *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Protocol = "browser"
			policy.AllowedProtocols = append(policy.AllowedProtocols, "browser")
			script.Actions = []dsl.Action{{Name: "popup", Type: "browser_open_window", Params: map[string]any{"url": "https://attacker.example/window"}}}
		}, wantError: "target host"},
		{name: "browser proxy target", mutate: func(_ *ciRunCreateRequest, policy *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Protocol = "browser"
			policy.AllowedProtocols = append(policy.AllowedProtocols, "browser")
			script.Config.Browser.Proxy.Server = "http://attacker.example:3128"
		}, wantError: "target host"},
		{name: "plaintext script credential", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Config.Database.DSN = "postgres://admin:password@db.example.test/app"
		}, wantError: "plaintext credential"},
		{name: "gRPC proto filesystem path", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Config.GRPC.ProtoFiles = []string{"/shared/other-project/service.proto"}
		}, wantError: "filesystem paths"},
		{name: "browser upload filesystem path", mutate: func(_ *ciRunCreateRequest, policy *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Protocol = "browser"
			policy.AllowedProtocols = append(policy.AllowedProtocols, "browser")
			script.Actions = []dsl.Action{{Name: "upload", Type: "browser_set_files", Selector: "#file", Params: map[string]any{"files": []any{"/shared/other-project/secret.txt"}}}}
		}, wantError: "cannot read filesystem"},
		{name: "plaintext variable credential", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Execution.Variables = map[string]string{"api_token": "plaintext"}
		}, wantError: "controller-managed secret"},
		{name: "authorization variable credential", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Execution.Variables = map[string]string{"authorization": "opaque-value"}
		}, wantError: "controller-managed secret"},
		{name: "AWS access-key variable credential", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Execution.Variables = map[string]string{"AWS_ACCESS_KEY_ID": "AKIAIOSFODNN7EXAMPLE"}
		}, wantError: "controller-managed secret"},
		{name: "credential-shaped variable value", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Execution.Variables = map[string]string{"header_value": "Bearer short"}
		}, wantError: "controller-managed secret"},
		{name: "credential-bearing variable URL", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Execution.Variables = map[string]string{"endpoint": "https://user:password@example.test/path"}
		}, wantError: "controller-managed secret"},
		{name: "signed variable URL", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Execution.Variables = map[string]string{"endpoint": "https://example.test/path?sig=short"}
		}, wantError: "controller-managed secret"},
		{name: "required SLA missing threshold", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Goals.RequireSLA = true
		}, wantError: "no SLA threshold"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := ciTestRunRequest()
			policy := basePolicy
			script := ciTestScript("https://api.example.test")
			if test.mutate != nil {
				test.mutate(&req, &policy, script)
			}
			err := validateCIRunRequest(req, policy, script)
			if test.wantError == "" && err != nil {
				t.Fatalf("validateCIRunRequest returned error: %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("validateCIRunRequest error = %v, want substring %q", err, test.wantError)
			}
		})
	}

	req := ciTestRunRequest()
	script := ciTestScript("https://api.example.test")
	script.Config.Database.DSN = "{{secret.database_dsn}}"
	if err := validateCIRunRequest(req, basePolicy, script); err == nil || !strings.Contains(err.Error(), "could not be resolved") {
		t.Fatalf("unresolved controller target secret error = %v", err)
	}
	policy := basePolicy
	policy.AllowedTargetHosts = append(append([]string(nil), policy.AllowedTargetHosts...), "db.example.test")
	resolver := engine.SecretResolverFunc(func(name string) (string, bool) {
		if name == "database_dsn" {
			return "postgres://user:password@db.example.test/app", true
		}
		return "", false
	})
	if err := validateCIRunRequest(req, policy, script, resolver); err != nil {
		t.Fatalf("controller-managed secret reference rejected: %v", err)
	}
}

func TestValidateCIScriptRejectsCredentialMaterialInNonJSONBodies(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantError bool
	}{
		{name: "form password", body: "password=hunter2", wantError: true},
		{name: "form percent encoded password", body: "password=hunter%202", wantError: true},
		{name: "form malformed separator cannot hide password", body: "status=ready;password=hunter2", wantError: true},
		{name: "form authorization value", body: "payload=Bearer+opaque-token", wantError: true},
		{name: "form nested JSON", body: `payload=%7B%22password%22%3A%22hunter2%22%7D`, wantError: true},
		{name: "form secret reference", body: "password=%7B%7Bsecret.login_password%7D%7D"},
		{name: "form spaced secret reference", body: "password=%7B%7B+secret.login_password+%7D%7D"},
		{name: "benign form", body: "status=ready&message=hello"},
		{name: "XML password", body: `<login><password>hunter2</password></login>`, wantError: true},
		{name: "XML namespaced password", body: `<soap:Envelope xmlns:soap="urn:soap"><soap:Password>hunter2</soap:Password></soap:Envelope>`, wantError: true},
		{name: "XML password attribute", body: `<login password="hunter2"/>`, wantError: true},
		{name: "XML embedded form password", body: `<payload>password=hunter2</payload>`, wantError: true},
		{name: "malformed XML password", body: `<login><password>hunter2`, wantError: true},
		{name: "XML secret reference", body: `<login><password>{{secret.login_password}}</password></login>`},
		{name: "XML spaced secret reference", body: `<login><password>{{ secret.login_password }}</password></login>`},
		{name: "XML secret reference attribute", body: `<login password="{{ secret.login_password }}"/>`},
		{name: "benign XML", body: `<request><message>hello</message></request>`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := ciTestScript("https://api.example.test")
			script.Actions[0].Body = test.body
			err := validateCIScriptHasNoPlaintextSecrets(script)
			if test.wantError && (err == nil || !strings.Contains(err.Error(), "plaintext credential")) {
				t.Fatalf("validateCIScriptHasNoPlaintextSecrets error = %v, want plaintext credential rejection", err)
			}
			if !test.wantError && err != nil {
				t.Fatalf("validateCIScriptHasNoPlaintextSecrets returned error: %v", err)
			}
		})
	}
}

func TestValidateCIScriptCredentialBindingsAcrossEncodings(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*dsl.Script)
		wantError bool
	}{
		{name: "JSON Bearer wrapper", mutate: func(script *dsl.Script) {
			script.Actions[0].Headers = map[string]string{"Authorization": "Bearer {{secret.api_token}}"}
		}},
		{name: "JSON Basic wrapper with spaced template", mutate: func(script *dsl.Script) {
			script.Actions[0].Headers = map[string]string{"Proxy-Authorization": "Basic {{ secret.proxy_credentials }}"}
		}},
		{name: "JSON ApiKey wrapper", mutate: func(script *dsl.Script) {
			script.Actions[0].Params = map[string]any{"token": "ApiKey {{secret.api_key}}"}
		}},
		{name: "JSON secret name rejects whitespace", mutate: func(script *dsl.Script) {
			script.Actions[0].Params = map[string]any{"token": "{{secret.api key}}"}
		}, wantError: true},
		{name: "JSON secret name rejects line breaks", mutate: func(script *dsl.Script) {
			script.Actions[0].Params = map[string]any{"token": "{{secret.api\nkey}}"}
		}, wantError: true},
		{name: "JSON wrapper rejects unknown scheme", mutate: func(script *dsl.Script) {
			script.Actions[0].Headers = map[string]string{"Authorization": "Digest {{secret.api_token}}"}
		}, wantError: true},
		{name: "JSON wrapper rejects suffix", mutate: func(script *dsl.Script) {
			script.Actions[0].Headers = map[string]string{"Authorization": "Bearer {{secret.api_token}} extra"}
		}, wantError: true},
		{name: "JSON wrapper rejects two templates", mutate: func(script *dsl.Script) {
			script.Actions[0].Headers = map[string]string{"Authorization": "Bearer {{secret.first}} {{secret.second}}"}
		}, wantError: true},
		{name: "JSON dsn_secret requires template", mutate: func(script *dsl.Script) {
			script.Config.Database.DSNSecret = "hunter2"
		}, wantError: true},
		{name: "JSON dsn_secret template", mutate: func(script *dsl.Script) {
			script.Config.Database.DSNSecret = "{{secret.database_dsn}}"
		}},
		{name: "JSON sasl_password_secret requires template", mutate: func(script *dsl.Script) {
			script.Config.Kafka.SASLPasswordSecret = "hunter2"
		}, wantError: true},
		{name: "JSON sasl_password_secret template", mutate: func(script *dsl.Script) {
			script.Config.Kafka.SASLPasswordSecret = "{{secret.kafka_password}}"
		}},
		{name: "JSON connection_string_secret requires template", mutate: func(script *dsl.Script) {
			script.Config.EventHubs.ConnectionStringSecret = "Endpoint=sb://ns.servicebus.windows.net/;SharedAccessKey=hunter2"
		}, wantError: true},
		{name: "JSON connection_string_secret template", mutate: func(script *dsl.Script) {
			script.Config.EventHubs.ConnectionStringSecret = "{{secret.eventhubs_connection_string}}"
		}},
		{name: "JSON governed non-credential references remain valid", mutate: func(script *dsl.Script) {
			script.DataSources = []dsl.DataSource{{Name: "payload", Type: "file", StorageRef: "asset://project/payload"}}
			script.Config.JMS.TLS = &dsl.JMSTLSConfig{CACertSecret: "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----", TrustStoreRef: "vault://truststore"}
			script.Actions[0].Params = map[string]any{"credential_ref": "credential://project/runtime"}
		}},
		{name: "form Authorization wrapper", mutate: func(script *dsl.Script) {
			script.Actions[0].Body = "authorization=Bearer+%7B%7Bsecret.api_token%7D%7D"
		}},
		{name: "form wrapper rejects two templates", mutate: func(script *dsl.Script) {
			script.Actions[0].Body = "authorization=Bearer+%7B%7Bsecret.first%7D%7D+%7B%7Bsecret.second%7D%7D"
		}, wantError: true},
		{name: "form dsn_secret requires template", mutate: func(script *dsl.Script) {
			script.Actions[0].Body = "dsn_secret=hunter2"
		}, wantError: true},
		{name: "form dsn_secret template", mutate: func(script *dsl.Script) {
			script.Actions[0].Body = "dsn_secret=%7B%7Bsecret.database_dsn%7D%7D"
		}},
		{name: "XML Authorization wrapper", mutate: func(script *dsl.Script) {
			script.Actions[0].Body = `<request><Authorization>Bearer {{secret.api_token}}</Authorization></request>`
		}},
		{name: "XML wrapper rejects unknown scheme", mutate: func(script *dsl.Script) {
			script.Actions[0].Body = `<request><Authorization>Digest {{secret.api_token}}</Authorization></request>`
		}, wantError: true},
		{name: "XML dsn_secret attribute requires template", mutate: func(script *dsl.Script) {
			script.Actions[0].Body = `<request dsn_secret="hunter2"/>`
		}, wantError: true},
		{name: "XML dsn_secret element template", mutate: func(script *dsl.Script) {
			script.Actions[0].Body = `<request><dsn_secret>{{ secret.database_dsn }}</dsn_secret></request>`
		}},
		{name: "XML storage_ref remains valid", mutate: func(script *dsl.Script) {
			script.Actions[0].Body = `<request><storage_ref>asset://project/payload</storage_ref></request>`
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := ciTestScript("https://api.example.test")
			test.mutate(script)
			err := validateCIScriptHasNoPlaintextSecrets(script)
			if test.wantError && (err == nil || !strings.Contains(err.Error(), "plaintext credential")) {
				t.Fatalf("validateCIScriptHasNoPlaintextSecrets error = %v, want plaintext credential rejection", err)
			}
			if !test.wantError && err != nil {
				t.Fatalf("validateCIScriptHasNoPlaintextSecrets returned error: %v", err)
			}
		})
	}
}

func TestCollectCITargetHostsCoversProtocolConnectionForms(t *testing.T) {
	tests := []struct {
		name     string
		script   *dsl.Script
		want     string
		deferred bool
		resolver engine.SecretResolver
	}{
		{
			name: "database multi-host DSN",
			script: &dsl.Script{Protocol: "database", Config: dsl.ScriptConfig{Database: dsl.DatabaseConfig{
				Driver: "postgres", DSN: "host=db-a.example.test,db-b.example.test port=5432 dbname=app",
			}}},
			want: "db-a.example.test,db-b.example.test",
		},
		{
			name:   "explicit gRPC resolver target",
			script: &dsl.Script{Protocol: "grpc", Config: dsl.ScriptConfig{BaseURL: "dns:///grpc.example.test:443"}},
			want:   "grpc.example.test",
		},
		{
			name: "Service Bus connection string",
			script: &dsl.Script{Protocol: "jms-servicebus", Config: dsl.ScriptConfig{JMS: dsl.JMSConfig{
				BrokerURL: "Endpoint=sb://orders.servicebus.windows.net/;SharedAccessKeyName=name;SharedAccessKey=secret",
			}}},
			want: "orders.servicebus.windows.net",
		},
		{
			name: "Kafka logical topic is not a target host",
			script: &dsl.Script{Protocol: "kafka", Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{
				Brokers: []string{"broker.example.test:9092"},
			}}, Actions: []dsl.Action{{Type: "kafka_produce", Target: "orders.created"}}},
			want: "broker.example.test",
		},
		{
			name: "controller-managed database DSN",
			script: &dsl.Script{Protocol: "database", Config: dsl.ScriptConfig{Database: dsl.DatabaseConfig{
				Driver: "postgres", DSN: "{{secret.database_dsn}}",
			}}},
			want: "db.example.test",
			resolver: engine.SecretResolverFunc(func(name string) (string, bool) {
				return "postgres://user:password@db.example.test/app", name == "database_dsn"
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hosts, deferred, err := collectCITargetHosts(test.script, "", test.resolver)
			if err != nil || strings.Join(hosts, ",") != test.want || deferred != test.deferred {
				t.Fatalf("collectCITargetHosts = %v, deferred=%t, err=%v; want %q, deferred=%t", hosts, deferred, err, test.want, test.deferred)
			}
		})
	}
}

func TestValidateCIRunAllowsControllerResolvedDatabaseTarget(t *testing.T) {
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_URL", "")
	t.Setenv(engine.ProjectSecretEnvironmentName("project-ci", "database_dsn"), "postgres://user:password@db.example.test/app")
	req := ciTestRunRequest()
	policy := ciRunSourceTestPolicy()
	policy.AllowedProtocols = []string{"database"}
	policy.AllowedTargetHosts = []string{"db.example.test"}
	script := &dsl.Script{
		Name: "database CI", Version: "1.0.0", Protocol: "database",
		Config:  dsl.ScriptConfig{Database: dsl.DatabaseConfig{Driver: "postgres", DSN: "{{secret.database_dsn}}"}},
		Actions: []dsl.Action{{Name: "health", Type: "db_health"}},
	}
	if err := validateCIRunRequest(req, policy, script, engine.NewProjectSecretResolver(req.ProjectID)); err != nil {
		t.Fatalf("controller-resolved database target rejected before runtime enforcement: %v", err)
	}
	policy.AllowedTargetHosts = []string{"other.example.test"}
	if err := validateCIRunRequest(req, policy, script, engine.NewProjectSecretResolver(req.ProjectID)); err == nil ||
		!strings.Contains(err.Error(), "resolved target host") || strings.Contains(err.Error(), "db.example.test") {
		t.Fatalf("policy-disallowed resolved target error = %v", err)
	}
}

func TestValidateCIRunRequiresAllReferencedProjectSecrets(t *testing.T) {
	req := ciTestRunRequest()
	policy := ciRunSourceTestPolicy()
	policy.AllowedProtocols = []string{"http"}
	policy.AllowedTargetHosts = []string{"api.example.test"}
	script := ciTestScript("https://api.example.test")
	script.Actions[0].Headers = map[string]string{
		"Authorization": "Bearer {{secret.api_token}}",
	}
	if err := validateCIRunRequest(req, policy, script, engine.SecretResolverFunc(func(string) (string, bool) {
		return "", false
	})); err == nil || !strings.Contains(err.Error(), "api_token") {
		t.Fatalf("missing project secret error = %v", err)
	}
	if err := validateCIRunRequest(req, policy, script, engine.SecretResolverFunc(func(name string) (string, bool) {
		return "configured", name == "api_token"
	})); err != nil {
		t.Fatalf("configured project secret rejected: %v", err)
	}
}

func TestCollectCITargetHostsRejectsUnresolvedAndInvalidSecretTargetsWithoutDisclosure(t *testing.T) {
	script := &dsl.Script{
		Protocol: "database",
		Config: dsl.ScriptConfig{Database: dsl.DatabaseConfig{
			Driver: "postgres", DSNSecret: "{{ secret.database_dsn }}",
		}},
	}
	if _, _, err := collectCITargetHosts(script, ""); err == nil || !strings.Contains(err.Error(), "could not be resolved") {
		t.Fatalf("unresolved target secret error = %v", err)
	}

	invalid := engine.SecretResolverFunc(func(string) (string, bool) {
		return "://user:supersecret@invalid target", true
	})
	if _, _, err := collectCITargetHosts(script, "", invalid); err == nil || !strings.Contains(err.Error(), "not a valid concrete DSN") || strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("invalid resolved target error = %v", err)
	}

	script.Config.Database.DSNSecret = "secret://project/database-dsn"
	if _, _, err := collectCITargetHosts(script, "", invalid); err == nil || !strings.Contains(err.Error(), "must use a controller-managed") {
		t.Fatalf("non-template secret binding error = %v", err)
	}
}

func TestTrustedGitHubAPIURLGitHubComAndGHES(t *testing.T) {
	tests := []struct {
		name, serverURL, apiURL string
		want                    bool
	}{
		{name: "github dot com API", serverURL: "https://github.com", apiURL: "https://api.github.com", want: true},
		{name: "github same origin", serverURL: "https://github.com", apiURL: "https://github.com/api/v3", want: true},
		{name: "github API cleartext", serverURL: "https://github.com", apiURL: "http://api.github.com", want: false},
		{name: "github API alternate port", serverURL: "https://github.com", apiURL: "https://api.github.com:8443", want: false},
		{name: "github lookalike", serverURL: "https://github.com", apiURL: "https://api.github.com.evil.test", want: false},
		{name: "ghes API path", serverURL: "https://github.corp.example", apiURL: "https://github.corp.example/api/v3", want: true},
		{name: "ghes alternate host", serverURL: "https://github.corp.example", apiURL: "https://api.github.corp.example", want: false},
		{name: "ghes cleartext", serverURL: "https://github.corp.example", apiURL: "http://github.corp.example/api/v3", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := trustedGitHubAPIURL(test.serverURL, test.apiURL); got != test.want {
				t.Fatalf("trustedGitHubAPIURL(%q, %q) = %t, want %t", test.serverURL, test.apiURL, got, test.want)
			}
		})
	}
}

func TestValidateCIRunSourceRejectsForksAndPullRequestTarget(t *testing.T) {
	policy := ciRunSourceTestPolicy()
	tests := []struct {
		name      string
		mutate    func(*ciRunSource)
		wantError string
	}{
		{name: "trusted push"},
		{name: "trusted same repository pull request", mutate: func(source *ciRunSource) {
			source.EventName = "pull_request"
			source.HeadRepository, source.HeadRepositoryID = source.Repository, source.RepositoryID
		}},
		{name: "fork repository name", mutate: func(source *ciRunSource) {
			source.EventName = "pull_request"
			source.HeadRepository, source.HeadRepositoryID = "contributor/apex", source.RepositoryID
		}, wantError: "fork pull requests"},
		{name: "fork repository id", mutate: func(source *ciRunSource) {
			source.EventName = "pull_request"
			source.HeadRepository, source.HeadRepositoryID = source.Repository, "99999"
		}, wantError: "fork pull requests"},
		{name: "missing PR head metadata", mutate: func(source *ciRunSource) { source.EventName = "pull_request" }, wantError: "fork pull requests"},
		{name: "pull request target", mutate: func(source *ciRunSource) { source.EventName = "pull_request_target" }, wantError: "pull_request_target"},
		{name: "untrusted API URL", mutate: func(source *ciRunSource) { source.APIURL = "https://api.github.com.evil.test" }, wantError: "source.api_url"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := ciTestRunSource("1")
			if test.mutate != nil {
				test.mutate(&source)
			}
			err := validateCIRunSource(policy, source)
			if test.wantError == "" && err != nil {
				t.Fatalf("validateCIRunSource returned error: %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("validateCIRunSource error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestValidateGitHubWorkflowRunSourceRejectsForgedMetadata(t *testing.T) {
	source := ciTestRunSource("1")
	baseRun := githubWorkflowRun{
		ID: 9001, Event: "push", HeadBranch: "main", HeadSHA: source.SHA,
		Path: ".github/workflows/performance.yml@main", RunAttempt: 1,
		Actor:          githubWorkflowRunActor{ID: 42, Login: "octocat"},
		Repository:     githubWorkflowRunRepository{ID: 12345, FullName: "octo/apex"},
		HeadRepository: githubWorkflowRunRepository{ID: 12345, FullName: "octo/apex"},
	}
	tests := []struct {
		name      string
		mutate    func(*githubWorkflowRun, *ciRunSource)
		wantError string
	}{
		{name: "verified push"},
		{name: "repository", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.Repository.ID = 999 }, wantError: "repository"},
		{name: "commit", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.HeadSHA = strings.Repeat("f", 40) }, wantError: "commit"},
		{name: "event", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.Event = "workflow_dispatch" }, wantError: "event"},
		{name: "attempt", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.RunAttempt = 2 }, wantError: "attempt"},
		{name: "actor", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.Actor.Login = "mallory" }, wantError: "actor"},
		{name: "workflow", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.Path = ".github/workflows/untrusted.yml@main" }, wantError: "workflow path"},
		{name: "workflow ref", mutate: func(_ *githubWorkflowRun, source *ciRunSource) {
			source.WorkflowRef = "octo/apex/.github/workflows/performance.yml@refs/heads/feature"
		}, wantError: "workflow ref"},
		{name: "ref", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.HeadBranch = "feature" }, wantError: "branch or tag"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run, candidate := baseRun, source
			if test.mutate != nil {
				test.mutate(&run, &candidate)
			}
			err := validateGitHubWorkflowRunSource(&run, candidate)
			if test.wantError == "" && err != nil {
				t.Fatalf("validateGitHubWorkflowRunSource returned error: %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("validateGitHubWorkflowRunSource error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestValidateGitHubWorkflowRunSourceRejectsForkPullRequest(t *testing.T) {
	source := ciTestRunSource("1")
	source.EventName = "pull_request"
	source.Ref = "refs/pull/17/merge"
	source.WorkflowRef = "octo/apex/.github/workflows/performance.yml@refs/pull/17/merge"
	source.HeadRepository, source.HeadRepositoryID = source.Repository, source.RepositoryID
	source.HeadSHA = strings.Repeat("a", 40)
	run := &githubWorkflowRun{
		ID: 9001, Event: "pull_request", HeadBranch: "feature", HeadSHA: source.HeadSHA,
		Path: ".github/workflows/performance.yml@main", RunAttempt: 1,
		Actor:          githubWorkflowRunActor{Login: source.Actor},
		Repository:     githubWorkflowRunRepository{ID: 12345, FullName: "octo/apex"},
		HeadRepository: githubWorkflowRunRepository{ID: 999, FullName: "contributor/apex"},
		PullRequests: []githubWorkflowRunPullRequest{{
			Number: 17, MergeCommitSHA: source.SHA,
			Head: githubWorkflowRunPullRequestRef{SHA: source.HeadSHA, Repo: githubWorkflowRunRepository{ID: 999, FullName: "contributor/apex"}},
			Base: githubWorkflowRunPullRequestRef{Repo: githubWorkflowRunRepository{ID: 12345, FullName: "octo/apex"}},
		}},
	}
	if err := validateGitHubWorkflowRunSource(run, source); err == nil || !strings.Contains(err.Error(), "fork pull requests") {
		t.Fatalf("fork validation error = %v", err)
	}
	run.HeadRepository = run.Repository
	run.PullRequests[0].Head.Repo = run.Repository
	if err := validateGitHubWorkflowRunSource(run, source); err != nil {
		t.Fatalf("same-repository pull request rejected: %v", err)
	}
}

func TestGitHubClientListsPullRequestsForCommit(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	sha := strings.Repeat("a", 40)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/octo/apex/commits/"+sha+"/pulls" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, []map[string]any{{
			"number": 17, "merge_commit_sha": strings.Repeat("b", 40),
			"head": map[string]any{"sha": sha, "repo": map[string]any{"id": 12345, "full_name": "octo/apex"}},
			"base": map[string]any{"sha": strings.Repeat("c", 40), "repo": map[string]any{"id": 12345, "full_name": "octo/apex"}},
		}})
	}))
	defer stub.Close()
	client, err := newGitHubClient(GitHubSettings{APIBaseURL: stub.URL, WebBaseURL: stub.URL}, "token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	pulls, err := client.ListPullRequestsForCommit(context.Background(), "octo", "apex", sha)
	if err != nil || len(pulls) != 1 || pulls[0].Number != 17 || pulls[0].Head.SHA != sha || pulls[0].MergeCommitSHA != strings.Repeat("b", 40) {
		t.Fatalf("ListPullRequestsForCommit = %#v, %v", pulls, err)
	}
}

func TestAPIKeyFallbackVerifiesWorkflowRunWithoutPublishingCheck(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	privateKey, _ := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_CI_API_KEY_APP", privateKey)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()

	var workflowCalls int
	var githubStub *httptest.Server
	githubStub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/101/access_tokens":
			writeJSON(w, http.StatusCreated, map[string]any{"token": "installation-token", "expires_at": time.Now().UTC().Add(time.Hour)})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/octo/apex/actions/runs/9001":
			workflowCalls++
			writeJSON(w, http.StatusOK, map[string]any{
				"id": 9001, "event": "push", "head_branch": "main", "head_sha": ciTestRunSource("1").SHA,
				"path": ".github/workflows/performance.yml@main", "run_attempt": 1,
				"actor":           map[string]any{"id": 42, "login": "octocat"},
				"repository":      map[string]any{"id": 12345, "full_name": "octo/apex"},
				"head_repository": map[string]any{"id": 12345, "full_name": "octo/apex"},
			})
		default:
			http.Error(w, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.Path), http.StatusNotFound)
		}
	}))
	defer githubStub.Close()

	policy := ciTestActionsPolicy()
	policy.ServerURL = githubStub.URL
	policy.AllowedEnvironments = nil
	policy.RequiredRunnerEnvironment = ""
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	server, store := newCIContractTestServer(t, policy)
	project, err := store.GetProject("project-ci")
	if err != nil || project == nil {
		t.Fatalf("GetProject = %#v, %v", project, err)
	}
	project.Settings.GitHub.AppInstallationID = 101
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{Enabled: true, AppID: 1, AppPrivateKeyEnvVar: "APEXLOAD_CI_API_KEY_APP", APIBaseURL: githubStub.URL, WebBaseURL: githubStub.URL}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().Add(time.Hour)
	ciKey := &storage.ServiceAccount{
		ID: "ci-key", Name: "CI key", Active: true, KeyHash: "ci-key-hash",
		ProjectRoles: map[string][]string{"project-ci": {RoleCIRunner}},
		Scopes:       []string{"tests:run", "results:read"},
		ExpiresAt:    &expiresAt, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.SaveServiceAccount(ciKey); err != nil {
		t.Fatalf("SaveServiceAccount: %v", err)
	}

	publishCheck := false
	scriptRaw, _ := json.Marshal(ciTestScript(target.URL))
	reqBody := ciRunCreateRequest{
		ProjectID: "project-ci", Script: scriptRaw, ScriptPath: "tests/performance.json",
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"}, Goals: ciRunGoalsRequest{},
		Source: ciTestRunSource("1"), PublishCheck: &publishCheck,
	}
	reqBody.Source.ServerURL, reqBody.Source.APIURL = githubStub.URL, githubStub.URL
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
	req.Header.Set("Idempotency-Key", "api-key-fallback-workflow-run-verification")
	req = withAuthenticatedContext(req, authResolution{
		User:           authenticatedUserFromServiceAccount(ciKey),
		ServiceAccount: &AuthenticatedServiceAccount{ID: "ci-key", Scopes: []string{"tests:run", "results:read"}},
	})
	recorder := httptest.NewRecorder()
	server.handleCreateCIRun(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("API-key CI create status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if workflowCalls != 1 {
		t.Fatalf("workflow-run verification calls = %d, want 1", workflowCalls)
	}
}

func TestAPIKeyFallbackRequiresOIDCForJobScopedPolicy(t *testing.T) {
	policy := ciTestActionsPolicy()
	server, _ := newCIContractTestServer(t, policy)
	if _, err := server.verifyAPIKeyCISource(context.Background(), storage.GitHubProjectPolicy{AppInstallationID: 101, Actions: policy}, ciTestRunSource("1")); err == nil || !strings.Contains(err.Error(), "use GitHub OIDC") {
		t.Fatalf("verifyAPIKeyCISource error = %v", err)
	}
}

func TestCIRunGetAndAbortRevalidateAPIKeyFallbackWithoutBreakingOIDC(t *testing.T) {
	endpoints := []struct {
		name   string
		method string
		handle func(*Server, http.ResponseWriter, *http.Request)
	}{
		{name: "get", method: http.MethodGet, handle: func(server *Server, w http.ResponseWriter, r *http.Request) { server.handleGetCIRun(w, r) }},
		{name: "abort", method: http.MethodPost, handle: func(server *Server, w http.ResponseWriter, r *http.Request) { server.handleAbortCIRun(w, r) }},
	}
	identities := []struct {
		name         string
		oidc         bool
		expires      bool
		mutateClaims func(*ciBearerClaims)
		wantStatus   int
	}{
		{name: "valid expiring ci_runner API key", expires: true, wantStatus: http.StatusOK},
		{name: "non-expiring ci_runner API key", wantStatus: http.StatusForbidden},
		{name: "OIDC exchanged identity", oidc: true, wantStatus: http.StatusOK},
		{name: "OIDC identity from another environment", oidc: true, mutateClaims: func(claims *ciBearerClaims) {
			claims.Environment = "staging"
		}, wantStatus: http.StatusForbidden},
		{name: "OIDC identity from another runner environment", oidc: true, mutateClaims: func(claims *ciBearerClaims) {
			claims.RunnerEnvironment = "self-hosted"
		}, wantStatus: http.StatusForbidden},
	}
	for _, endpoint := range endpoints {
		for _, identity := range identities {
			t.Run(endpoint.name+"/"+identity.name, func(t *testing.T) {
				server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
				script := ciTestScript("https://api.example.test")
				script.ID = "ci-handler-auth-script"
				script.ProjectID = "project-ci"
				storedScript, err := server.runner.StoreScriptDurable(script)
				if err != nil {
					t.Fatalf("StoreScriptDurable: %v", err)
				}
				source := ciTestRunSource("1")
				metadata := ciRunMetadata{
					SchemaVersion: ciRunSchemaVersion, Source: source, ScriptIdentity: "script_id:" + storedScript.ID,
					PolicyRevision: ciRunSourceTestPolicy().Revision, IdempotencySHA: sha256String("handler-api-key-revalidation"),
				}
				materializationKey, err := encodeCIRunMetadata(metadata)
				if err != nil {
					t.Fatalf("encodeCIRunMetadata: %v", err)
				}
				testRun, err := server.runner.CreateTest(CreateTestRequest{
					RequestedTestID: "286dfe0b-e856-4137-a072-26b58bdfe91f", Name: "CI handler auth", ProjectID: "project-ci",
					ScriptID: storedScript.ID, VUsers: 1, Duration: "1m", MaterializationKey: materializationKey,
				})
				if err != nil {
					t.Fatalf("CreateTest: %v", err)
				}

				req := httptest.NewRequest(endpoint.method, "/api/v1/ci/runs/"+testRun.ID, nil)
				req.SetPathValue("id", testRun.ID)
				if identity.oidc {
					claims := &ciBearerClaims{
						ProjectID: "project-ci", Repository: source.Repository, RepositoryID: source.RepositoryID,
						Ref: source.Ref, SHA: source.SHA, WorkflowRef: source.WorkflowRef, RunID: source.RunID,
						RunAttempt: source.RunAttempt, Actor: source.Actor, EventName: source.EventName,
						Environment: source.Environment, RunnerEnvironment: source.RunnerEnvironment,
					}
					if identity.mutateClaims != nil {
						identity.mutateClaims(claims)
					}
					req = withAuthenticatedContext(req, authResolution{ServiceAccount: &AuthenticatedServiceAccount{ID: "github-actions:12345:9001", Scopes: []string{"tests:run", "results:read"}}})
					req = withCIClaims(req, claims)
				} else {
					now := time.Now().UTC()
					account := &storage.ServiceAccount{
						ID: "ci-handler-key", Name: "CI handler key", KeyHash: "ci-handler-key-hash", Active: true,
						ProjectRoles: map[string][]string{"project-ci": {RoleCIRunner}}, Scopes: []string{"tests:run", "results:read"},
						CreatedAt: now, UpdatedAt: now,
					}
					if identity.expires {
						expiresAt := now.Add(time.Hour)
						account.ExpiresAt = &expiresAt
					}
					if err := store.SaveServiceAccount(account); err != nil {
						t.Fatalf("SaveServiceAccount: %v", err)
					}
					req = withAuthenticatedContext(req, authResolution{ServiceAccount: &AuthenticatedServiceAccount{ID: account.ID, Scopes: append([]string(nil), account.Scopes...)}})
				}

				recorder := httptest.NewRecorder()
				endpoint.handle(server, recorder, req)
				if recorder.Code != identity.wantStatus {
					t.Fatalf("status = %d, body=%s; want %d", recorder.Code, recorder.Body.String(), identity.wantStatus)
				}
				if endpoint.name == "abort" && identity.wantStatus == http.StatusForbidden {
					latest, ok := server.runner.GetTest(testRun.ID)
					if !ok || latest.Status != "QUEUED" {
						t.Fatalf("denied abort changed run state: %#v", latest)
					}
				}
			})
		}
	}
}

func TestAbortCIRunRejectsMalformedBody(t *testing.T) {
	server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
	script := ciTestScript("https://api.example.test")
	script.ID = "ci-abort-malformed-body-script"
	script.ProjectID = "project-ci"
	stored, err := server.runner.StoreScriptDurable(script)
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}

	source := ciTestRunSource("1")
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: source, ScriptIdentity: "script_id:" + stored.ID,
		PolicyRevision: ciRunSourceTestPolicy().Revision, IdempotencySHA: sha256String("abort-malformed-body"),
	}
	materializationKey, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatalf("encodeCIRunMetadata: %v", err)
	}

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: "c8ecf9b5-3a1b-4b4d-97a8-8c6f9f7aef31", Name: "CI abort malformed body", ProjectID: "project-ci",
		ScriptID: stored.ID, VUsers: 1, Duration: "1m", MaterializationKey: materializationKey,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}

	expiresAt := time.Now().Add(time.Hour)
	account := &storage.ServiceAccount{
		ID: "ci-abort-malformed-body-key", Name: "CI abort malformed body key", Active: true, KeyHash: "ci-abort-malformed-body-key-hash",
		ProjectRoles: map[string][]string{"project-ci": {RoleCIRunner}},
		Scopes:       []string{"tests:run", "results:read"}, ExpiresAt: &expiresAt,
	}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatalf("SaveServiceAccount: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs/"+testRun.ID+"/abort", strings.NewReader(`{`))
	req = withAuthenticatedContext(req, authResolution{ServiceAccount: &AuthenticatedServiceAccount{ID: account.ID, Scopes: append([]string(nil), account.Scopes...)}})
	req.SetPathValue("id", testRun.ID)
	resp := httptest.NewRecorder()
	server.handleAbortCIRun(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("bad request status = %d body=%s", resp.Code, resp.Body.String())
	}
	var parsed map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("malformed abort response was not JSON: %v", err)
	}
	if !strings.Contains(parsed["error"], "invalid request body") {
		t.Fatalf("bad abort response = %#v", parsed)
	}

	latest, ok := server.runner.GetTest(testRun.ID)
	if !ok || latest.Status != "QUEUED" {
		t.Fatalf("malformed abort changed run state: %#v", latest)
	}
}

func TestAbortCIRunRejectsOversizeBody(t *testing.T) {
	server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
	script := ciTestScript("https://api.example.test")
	script.ID = "ci-abort-oversize-body-script"
	script.ProjectID = "project-ci"
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}

	source := ciTestRunSource("1")
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: source, ScriptIdentity: "script_id:" + script.ID,
		PolicyRevision: ciRunSourceTestPolicy().Revision, IdempotencySHA: sha256String("abort-oversize-body"),
	}
	materializationKey, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatalf("encodeCIRunMetadata: %v", err)
	}
	runID := "c8ecf9b5-3a1b-4b4d-97a8-8c6f9f7aef32"
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: runID, Name: "CI abort oversize body", ProjectID: "project-ci",
		ScriptID: script.ID, VUsers: 1, Duration: "1m", MaterializationKey: materializationKey,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}

	expiresAt := time.Now().Add(time.Hour)
	account := &storage.ServiceAccount{
		ID: "ci-abort-oversize-body-key", Name: "CI abort oversize body key", Active: true, KeyHash: "ci-abort-oversize-body-key-hash",
		ProjectRoles: map[string][]string{"project-ci": {RoleCIRunner}},
		Scopes:       []string{"tests:run", "results:read"}, ExpiresAt: &expiresAt,
	}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatalf("SaveServiceAccount: %v", err)
	}

	reason := strings.Repeat("x", ciAbortMaxBodyBytes+1)
	body := fmt.Sprintf(`{"reason":"%s"}`, reason)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs/"+testRun.ID+"/abort", strings.NewReader(body))
	req = withAuthenticatedContext(req, authResolution{ServiceAccount: &AuthenticatedServiceAccount{ID: account.ID, Scopes: append([]string(nil), account.Scopes...)}})
	req.SetPathValue("id", testRun.ID)
	resp := httptest.NewRecorder()
	server.handleAbortCIRun(resp, req)

	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusRequestEntityTooLarge, resp.Body.String())
	}
	var parsed map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("oversize abort response was not JSON: %v", err)
	}
	if !strings.Contains(parsed["error"], "request body exceeds the configured size limit") {
		t.Fatalf("unexpected oversize abort response = %#v", parsed)
	}
}

func TestCreateCIRunIdempotencyReusesAttemptAndSeparatesNewAttempt(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	server, _ := newCIContractTestServer(t, policy)
	publishCheck := false
	scriptRaw, err := json.Marshal(ciTestScript(target.URL))
	if err != nil {
		t.Fatal(err)
	}
	request := ciRunCreateRequest{
		ProjectID: "project-ci", ScriptPath: "tests/performance.json", Script: scriptRaw,
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"},
		Goals:     ciRunGoalsRequest{RequireSLA: false, RequireResults: false}, Source: ciTestRunSource("1"),
		PublishCheck: &publishCheck,
	}

	invoke := func(key string, req ciRunCreateRequest) (int, ciRunResponse) {
		t.Helper()
		body, marshalErr := json.Marshal(req)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		server.handleCreateCIRun(recorder, httpReq)
		var response ciRunResponse
		if recorder.Code == http.StatusAccepted {
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode CI run response: %v; body=%s", err, recorder.Body.String())
			}
		}
		return recorder.Code, response
	}

	const firstKey = "octo/apex:performance:9001:1:script-sha256-aaaaaaaa"
	status, first := invoke(firstKey, request)
	if status != http.StatusAccepted || first.TestID == "" {
		t.Fatalf("first create = %d, %#v", status, first)
	}
	status, retry := invoke(firstKey, request)
	if status != http.StatusAccepted || retry.TestID != first.TestID {
		t.Fatalf("retry = %d, id %q; want %q", status, retry.TestID, first.TestID)
	}
	changedSettingsSameKey := request
	changedSettingsSameKey.Execution.VUsers = 2
	if status, _ := invoke(firstKey, changedSettingsSameKey); status != http.StatusConflict {
		t.Fatalf("changed settings with reused key status = %d, want 409", status)
	}

	changedAttemptSameKey := request
	changedAttemptSameKey.Source.RunAttempt = "2"
	if status, _ := invoke(firstKey, changedAttemptSameKey); status != http.StatusConflict {
		t.Fatalf("changed attempt with reused key status = %d, want 409", status)
	}

	const secondKey = "octo/apex:performance:9001:2:script-sha256-aaaaaaaa"
	status, second := invoke(secondKey, changedAttemptSameKey)
	if status != http.StatusAccepted || second.TestID == "" || second.TestID == first.TestID {
		t.Fatalf("new attempt = %d, %#v; first id=%q", status, second, first.TestID)
	}
}

func TestCreateCIRunAdoptsConcurrentRunningMaterializationWithoutRestartingIt(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	server, store := newCIContractTestServer(t, policy)
	publishCheck := false
	scriptRaw, err := json.Marshal(ciTestScript(target.URL))
	if err != nil {
		t.Fatal(err)
	}
	request := ciRunCreateRequest{
		ProjectID: "project-ci", ScriptPath: "tests/concurrent.json", Script: scriptRaw,
		Execution:    ciRunExecutionRequest{VUsers: 1, Duration: "50ms"},
		Goals:        ciRunGoalsRequest{},
		Source:       ciTestRunSource("1"),
		PublishCheck: &publishCheck,
	}

	var winningRuntime *ManagedTest
	var winningContext context.Context
	server.beforeCIRunMaterialize = func(create CreateTestRequest) {
		winner, createErr := server.runner.CreateTest(create)
		if createErr != nil {
			t.Fatalf("materialize concurrent winner: %v", createErr)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		startedAt := time.Now().UTC()
		server.runner.mu.Lock()
		winningRuntime = server.runner.tests[winner.ID]
		winningRuntime.Status = "RUNNING"
		winningRuntime.StartedAt = &startedAt
		winningRuntime.executionCtx = ctx
		winningRuntime.cancel = cancel
		record := buildManagedTestRecord(winningRuntime)
		server.runner.mu.Unlock()
		winningContext = ctx
		if saveErr := store.SaveTest(record); saveErr != nil {
			t.Fatalf("persist concurrent RUNNING winner: %v", saveErr)
		}
	}

	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Idempotency-Key", "octo/apex:performance:9001:1:concurrent-running")
	recorder := httptest.NewRecorder()
	server.handleCreateCIRun(recorder, httpReq)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("concurrent idempotent create = %d %s", recorder.Code, recorder.Body.String())
	}
	var response ciRunResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.RLock()
	retained := server.runner.tests[response.TestID]
	server.runner.mu.RUnlock()
	if retained == nil {
		t.Fatalf("handler dropped the concurrent winner %p", winningRuntime)
	}
	if retained != winningRuntime || retained.Status != "RUNNING" || retained.executionCtx != winningContext || retained.cancel == nil {
		t.Fatalf("handler replaced or restarted concurrent winner: retained=%p winner=%p status=%q context_retained=%t cancel_retained=%t", retained, winningRuntime, retained.Status, retained.executionCtx == winningContext, retained.cancel != nil)
	}
	durable, err := store.GetTest(response.TestID)
	if err != nil || durable.Status != "RUNNING" {
		t.Fatalf("durable concurrent winner = %#v, %v", durable, err)
	}
}

func TestConcurrentCreateCIRunRequestsBothAdoptSingleStartedMaterialization(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	server, _ := newCIContractTestServer(t, policy)
	backend := &executionBackendSpy{called: make(chan string, 4)}
	server.runner.setExecutionBackend(backend)
	publishCheck := false
	scriptRaw, err := json.Marshal(ciTestScript(target.URL))
	if err != nil {
		t.Fatal(err)
	}
	request := ciRunCreateRequest{
		ProjectID: "project-ci", ScriptPath: "tests/concurrent-real.json", Script: scriptRaw,
		Execution:    ciRunExecutionRequest{VUsers: 1, Duration: "50ms"},
		Goals:        ciRunGoalsRequest{},
		Source:       ciTestRunSource("1"),
		PublishCheck: &publishCheck,
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}

	firstAtMaterialize := make(chan struct{})
	secondAtMaterialize := make(chan struct{})
	allowFirst := make(chan struct{})
	allowSecond := make(chan struct{})
	freshCreated := make(chan struct{})
	allowFreshToStart := make(chan struct{})
	var arrivals atomic.Int32
	server.beforeCIRunMaterialize = func(CreateTestRequest) {
		switch arrivals.Add(1) {
		case 1:
			close(firstAtMaterialize)
			<-allowFirst
		case 2:
			close(secondAtMaterialize)
			<-allowSecond
		}
	}
	server.afterCIRunMaterialize = func(test *ManagedTest) {
		if test != nil && !test.creationAdopted {
			close(freshCreated)
			<-allowFreshToStart
		}
	}

	invoke := func() *httptest.ResponseRecorder {
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Idempotency-Key", "octo/apex:performance:9001:1:actual-concurrent")
		recorder := httptest.NewRecorder()
		server.handleCreateCIRun(recorder, httpReq)
		return recorder
	}
	firstResponse := make(chan *httptest.ResponseRecorder, 1)
	secondResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstResponse <- invoke() }()
	<-firstAtMaterialize
	go func() { secondResponse <- invoke() }()
	<-secondAtMaterialize
	close(allowFirst)
	<-freshCreated
	close(allowSecond)

	second := <-secondResponse
	if second.Code != http.StatusAccepted {
		t.Fatalf("adopted concurrent response = %d %s", second.Code, second.Body.String())
	}
	select {
	case <-backend.called:
	case <-time.After(2 * time.Second):
		t.Fatal("adopted request did not start the materialized run")
	}
	close(allowFreshToStart)
	first := <-firstResponse
	if first.Code != http.StatusAccepted {
		t.Fatalf("fresh concurrent response = %d %s", first.Code, first.Body.String())
	}
	var firstRun, secondRun ciRunResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstRun); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondRun); err != nil {
		t.Fatal(err)
	}
	if firstRun.TestID == "" || firstRun.TestID != secondRun.TestID {
		t.Fatalf("concurrent run ids = %q and %q", firstRun.TestID, secondRun.TestID)
	}
	select {
	case duplicate := <-backend.called:
		t.Fatalf("concurrent retry started duplicate backend execution for %s", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCreateCIRunIdempotentLibraryRetryIgnoresLaterScriptMutationAndDeletion(t *testing.T) {
	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"api.example.test"}
	server, _ := newCIContractTestServer(t, policy)

	script := ciTestScript("https://api.example.test")
	script.ID = "mutable-library-ci-script"
	script.ProjectID = "project-ci"
	stored, err := server.runner.StoreScriptDurable(script)
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	publishCheck := false
	request := ciRunCreateRequest{
		ProjectID: "project-ci", ScriptID: stored.ID, ScriptPath: "tests/library-performance.json",
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"},
		Goals:     ciRunGoalsRequest{RequireSLA: false, RequireResults: false}, Source: ciTestRunSource("1"),
		PublishCheck: &publishCheck,
	}
	invoke := func() (int, ciRunResponse) {
		t.Helper()
		body, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Idempotency-Key", "octo/apex:performance:9001:1:library-script")
		recorder := httptest.NewRecorder()
		server.handleCreateCIRun(recorder, httpReq)
		var response ciRunResponse
		if recorder.Code == http.StatusAccepted {
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode CI run response: %v; body=%s", err, recorder.Body.String())
			}
		}
		return recorder.Code, response
	}

	status, first := invoke()
	if status != http.StatusAccepted || first.TestID == "" {
		t.Fatalf("first create = %d, %#v", status, first)
	}

	mutated := cloneScriptDefinition(stored.Script)
	mutated.Config.BaseURL = "https://blocked.example.invalid"
	if _, err := server.runner.StoreScriptDurable(mutated); err != nil {
		t.Fatalf("mutate source library script: %v", err)
	}
	status, retryAfterMutation := invoke()
	if status != http.StatusAccepted || retryAfterMutation.TestID != first.TestID {
		t.Fatalf("retry after mutation = %d, id %q; want %q", status, retryAfterMutation.TestID, first.TestID)
	}

	deleted, err := server.runner.DeleteScriptDurableInProject(stored.ID, "project-ci")
	if err != nil || !deleted {
		t.Fatalf("delete source library script = %t, %v", deleted, err)
	}
	status, retryAfterDelete := invoke()
	if status != http.StatusAccepted || retryAfterDelete.TestID != first.TestID {
		t.Fatalf("retry after delete = %d, id %q; want %q", status, retryAfterDelete.TestID, first.TestID)
	}
}

func TestCreateCIRunUsesGoalModeAndDoesNotFakeSLAEvaluation(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer target.Close()

	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	server, store := newCIContractTestServer(t, policy)
	now := time.Now().UTC()
	if err := store.SaveApplication(&storage.Application{ID: "app-1", ProjectID: "project-ci", Name: "APEX", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRelease(&storage.Release{ID: "rel-1", ApplicationID: "app-1", ProjectID: "project-ci", Name: "Release 1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	publishCheck := false
	p95 := 250.0
	scriptRaw, err := json.Marshal(ciTestScript(target.URL))
	if err != nil {
		t.Fatal(err)
	}
	request := ciRunCreateRequest{
		ProjectID: "project-ci", Name: "PR perf", ReleaseID: "rel-1",
		ScriptPath: "tests/performance.json", Script: scriptRaw,
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"},
		Goals:     ciRunGoalsRequest{P95LatencyMs: &p95, RequireSLA: true, RequireResults: false},
		Source:    ciTestRunSource("1"), PublishCheck: &publishCheck,
	}
	body, _ := json.Marshal(request)
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Idempotency-Key", "goal-mode-sla-evaluation")
	recorder := httptest.NewRecorder()
	server.handleCreateCIRun(recorder, httpReq)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response ciRunResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	testRun, ok := server.runner.GetTest(response.TestID)
	if !ok {
		t.Fatalf("created test %q not found", response.TestID)
	}
	if testRun.ScenarioMode != "goal_oriented" {
		t.Fatalf("scenario mode = %q, want goal_oriented", testRun.ScenarioMode)
	}
	if testRun.GoalConfig == nil || testRun.GoalConfig.P95LatencyMs == nil || *testRun.GoalConfig.P95LatencyMs != p95 {
		t.Fatalf("goal config = %#v", testRun.GoalConfig)
	}
	if response.SLAEvaluated {
		t.Fatalf("initial response marked SLA evaluated before goal telemetry existed: %#v", response)
	}
	metadata, err := decodeCIRunMetadata(testRun.MaterializationKey)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "PR perf" || metadata.ReleaseID != "rel-1" {
		t.Fatalf("metadata name/release = %q/%q", metadata.Name, metadata.ReleaseID)
	}
}

func TestQueuedCIRunRecoversAtControllerStartup(t *testing.T) {
	policy := ciRunSourceTestPolicy()
	server, store := newCIContractTestServer(t, policy)
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-startup-recovery-script", ProjectID: "project-ci", Name: "Recovery", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Method: "GET", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("1"), ScriptIdentity: "script_id:" + script.ID,
		PolicyRevision: policy.Revision, IdempotencySHA: sha256String("startup-recovery"),
	}
	key, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: "7a5c0d2a-d9da-487d-a472-45040c915a9e", Name: "CI startup recovery", ProjectID: "project-ci", ScriptID: script.ID,
		VUsers: 1, Duration: "1m", MaterializationKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	normal, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: "5107fa01-343f-45fd-8cab-e8fdcf82df70", Name: "Ordinary queued run", ProjectID: "project-ci", ScriptID: script.ID,
		VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}
	spy := &executionBackendSpy{called: make(chan string, 1)}
	restarted := NewTestRunner(4, NewHub(), store, WithExecutionBackend(spy))
	select {
	case startedID := <-spy.called:
		if startedID != queued.ID {
			t.Fatalf("recovered run id = %q, want %q", startedID, queued.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued CI run was not recovered at startup")
	}
	ordinary, ok := restarted.GetTest(normal.ID)
	if !ok || ordinary.Status != "QUEUED" {
		t.Fatalf("ordinary queued run was unexpectedly started: %#v", ordinary)
	}
}

func TestQueuedCIRunRecoveryRejectsRevokedPolicy(t *testing.T) {
	policy := ciRunSourceTestPolicy()
	server, store := newCIContractTestServer(t, policy)
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-revoked-startup-script", ProjectID: "project-ci", Name: "Revoked recovery", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Method: "GET", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("1"), ScriptIdentity: "script_id:" + script.ID,
		PolicyRevision: policy.Revision, IdempotencySHA: sha256String("revoked-startup-recovery"),
	}
	key, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: "0b6cf34b-4f9c-409b-930c-959ca8b3f50c", Name: "Revoked CI startup recovery", ProjectID: "project-ci", ScriptID: script.ID,
		VUsers: 1, Duration: "1m", MaterializationKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.GetProject("project-ci")
	if err != nil || project == nil {
		t.Fatalf("GetProject = %#v, %v", project, err)
	}
	project.Settings.GitHub.Actions.Revision++
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}

	spy := &executionBackendSpy{called: make(chan string, 1)}
	restarted := NewTestRunner(4, NewHub(), store, WithExecutionBackend(spy))
	failed := waitForTestStatusWithin(t, restarted, queued.ID, 2*time.Second, "FAILED")
	if !strings.Contains(failed.Error, "policy revision changed") {
		t.Fatalf("recovered run error = %q", failed.Error)
	}
	select {
	case startedID := <-spy.called:
		t.Fatalf("revoked queued CI run reached execution backend: %s", startedID)
	default:
	}
}

func TestPendingCIRunPromotionRejectsDisabledPolicy(t *testing.T) {
	policy := ciRunSourceTestPolicy()
	server, store := newCIContractTestServer(t, policy)
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-disabled-pending-script", ProjectID: "project-ci", Name: "Disabled pending", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Method: "GET", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("1"), ScriptIdentity: "script_id:" + script.ID,
		PolicyRevision: policy.Revision, IdempotencySHA: sha256String("disabled-pending-promotion"),
	}
	key, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: "c75fbebd-72c2-4aa5-a8b8-cb91b8491b29", Name: "Disabled pending CI run", ProjectID: "project-ci", ScriptID: script.ID,
		VUsers: 1, Duration: "1m", MaterializationKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	server.runner.mu.Lock()
	managed := server.runner.tests[pending.ID]
	managed.Status = "PENDING"
	managed.PendingSince = &now
	managed.PendingReason = "awaiting capacity"
	server.runner.mu.Unlock()
	if err := server.runner.saveManagedTestRecord(managed); err != nil {
		t.Fatal(err)
	}
	project, err := store.GetProject("project-ci")
	if err != nil || project == nil {
		t.Fatalf("GetProject = %#v, %v", project, err)
	}
	project.Settings.GitHub.Actions.Enabled = false
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	spy := &executionBackendSpy{called: make(chan string, 1)}
	server.runner.setExecutionBackend(spy)

	server.runner.promotePendingTests()
	failed := waitForTestStatusWithin(t, server.runner, pending.ID, 2*time.Second, "FAILED")
	if !strings.Contains(failed.Error, "execution was disabled") {
		t.Fatalf("pending run error = %q", failed.Error)
	}
	select {
	case startedID := <-spy.called:
		t.Fatalf("disabled pending CI run reached execution backend: %s", startedID)
	default:
	}
}

func TestFailCIRunDuringPromotionDoesNotReenterPendingQueue(t *testing.T) {
	runner, ids := newRevokedPendingCIRunner(t, 2)
	if err := runner.failCIRunBeforeExecutionDuringPromotion(ids[0], "policy revoked"); err != nil {
		t.Fatal(err)
	}
	first, ok := runner.GetTest(ids[0])
	if !ok || first.Status != "FAILED" {
		t.Fatalf("first run = %#v, want FAILED", first)
	}
	second, ok := runner.GetTest(ids[1])
	if !ok || second.Status != "PENDING" {
		t.Fatalf("second run = %#v, want untouched PENDING row", second)
	}
}

func TestFailCIRunBeforeExecutionDoesNotTerminalizeStartedRun(t *testing.T) {
	runner, ids := newRevokedPendingCIRunner(t, 1)
	runner.mu.Lock()
	started := time.Now().UTC()
	run := runner.tests[ids[0]]
	run.Status = "RUNNING"
	run.StartedAt = &started
	cancelled := false
	run.cancel = func() { cancelled = true }
	runner.mu.Unlock()

	if err := runner.failCIRunBeforeExecution(ids[0], "stale recovery failure"); err != nil {
		t.Fatal(err)
	}
	got, ok := runner.GetTest(ids[0])
	if !ok || got.Status != "RUNNING" {
		t.Fatalf("started run = %#v, want RUNNING", got)
	}
	if cancelled {
		t.Fatal("stale recovery failure cancelled a started run")
	}
}

func TestValidateExistingCIRunIgnoresControllerStampedPolicyMetadata(t *testing.T) {
	expected := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, ScriptIdentity: "script_id:one",
		PolicyRevision: 2, CheckInstallationID: 20, IdempotencySHA: "same",
	}
	actual := expected
	actual.PolicyRevision = 1
	actual.CheckInstallationID = 10
	encoded, err := encodeCIRunMetadata(actual)
	if err != nil {
		t.Fatal(err)
	}
	test := &ManagedTest{ProjectID: "project-ci", MaterializationKey: encoded}
	if err := validateExistingCIRun(test, "project-ci", expected); err != nil {
		t.Fatalf("controller-stamped metadata broke idempotent retry: %v", err)
	}
}

func TestPendingCIRunPromotionDrainsRevokedQueueIteratively(t *testing.T) {
	const rows = 128
	runner, ids := newRevokedPendingCIRunner(t, rows)
	done := make(chan struct{})
	go func() {
		runner.promotePendingTests()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Minute):
		t.Fatal("revoked pending CI queue did not drain")
	}
	for _, id := range ids {
		test, ok := runner.GetTest(id)
		if !ok || test.Status != "FAILED" {
			t.Fatalf("run %s = %#v, want FAILED", id, test)
		}
		if !strings.Contains(test.Error, "execution was disabled") {
			t.Fatalf("run %s error = %q, want disabled-policy failure", id, test.Error)
		}
	}
}

func TestFailedCIRunStartIsExecutionFailureNotCancellation(t *testing.T) {
	policy := ciRunSourceTestPolicy()
	server, _ := newCIContractTestServer(t, policy)
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-start-failure-script", ProjectID: "project-ci", Name: "Start failure", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Method: "GET", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("1"), ScriptIdentity: "script_id:" + script.ID,
		PolicyRevision: policy.Revision, IdempotencySHA: sha256String("start-failure"),
	}
	key, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: "c894544e-b7df-4b21-a764-5af47b7d72dd", Name: "CI start failure", ProjectID: "project-ci", ScriptID: script.ID,
		VUsers: 1, Duration: "1m", MaterializationKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.runner.beginProjectDeletion("project-ci"); err != nil {
		t.Fatal(err)
	}
	defer server.runner.endProjectDeletion("project-ci")
	startErr := server.runner.StartTest(testRun.ID)
	if startErr == nil || !strings.Contains(startErr.Error(), "being deleted") {
		t.Fatalf("StartTest error = %v", startErr)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", nil)
	server.respondFailedCIRunStart(recorder, request, testRun, "controller could not start CI run", startErr)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("response status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response ciRunResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "FAILED" || response.Outcome != "execution_error" {
		t.Fatalf("start failure response = %#v", response)
	}
	if conclusion := ciGitHubCheckConclusion(response); conclusion != "failure" {
		t.Fatalf("start failure Check conclusion = %q, want failure", conclusion)
	}
}

func TestPrivateCIScriptSnapshotCompilesWithoutLibraryPersistence(t *testing.T) {
	server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
	snapshot := &dsl.Script{
		ID: "github-actions-ci-private-snapshot", ProjectID: "project-ci", Name: "Private CI snapshot", Version: "1.0.0", Protocol: "http",
		GeneratedBy: ciInlineScriptGenerator,
		Config:      dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions:     []dsl.Action{{Name: "health", Type: "request", Method: "GET", Target: "/health"}},
	}
	stored, err := server.runner.acquirePrivateCIScriptSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.ID != snapshot.ID {
		t.Fatalf("private snapshot = %#v", stored)
	}
	if _, ok := server.runner.GetScript(snapshot.ID); ok {
		t.Fatal("private CI snapshot was exposed through GetScript")
	}
	if scripts := server.runner.ListScriptsFiltered("project-ci", nil); len(scripts) != 0 {
		t.Fatalf("private CI snapshot was exposed through ListScriptsFiltered: %#v", scripts)
	}
	durable, err := store.ListScripts("project-ci", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(durable) != 0 {
		t.Fatalf("private CI snapshot was persisted in the library: %#v", durable)
	}
	if _, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: snapshot.ID, ProjectID: "project-ci", Name: "Collision", Protocol: "http",
	}); err == nil {
		t.Fatal("library write replaced an active private CI snapshot")
	}
	if _, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "reserved-ci-generator", ProjectID: "project-ci", Name: "Reserved", Protocol: "http", GeneratedBy: ciInlineScriptGenerator,
	}); err == nil {
		t.Fatal("library write accepted the reserved CI snapshot marker")
	}
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Private snapshot compile", ProjectID: "project-ci", ScriptID: snapshot.ID, VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatalf("compile private CI snapshot into test: %v", err)
	}
	if len(testRun.Groups) != 1 || testRun.Groups[0].ScriptID != snapshot.ID || testRun.Groups[0].script == nil {
		t.Fatalf("compiled test groups = %#v", testRun.Groups)
	}
	server.runner.releasePrivateCIScriptSnapshot(snapshot.ID)
	server.runner.mu.RLock()
	_, cached := server.runner.scripts[snapshot.ID]
	_, referenced := server.runner.privateCISnapshots[snapshot.ID]
	server.runner.mu.RUnlock()
	if cached || referenced {
		t.Fatalf("released private snapshot remained cached: cached=%t referenced=%t", cached, referenced)
	}
}

func TestLeakedCIScriptSnapshotCleanupPreservesQueuedRunRecovery(t *testing.T) {
	policy := ciRunSourceTestPolicy()
	server, store := newCIContractTestServer(t, policy)
	userOwned, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "github-actions-ci-user-owned-script", ProjectID: "project-ci", Name: "User-owned lookalike", Version: "1.0.0", Protocol: "http",
		GeneratedBy: "user-import",
		Config:      dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions:     []dsl.Action{{Name: "health", Type: "request", Method: "GET", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	leaked := &dsl.Script{
		ID: "github-actions-ci-leaked-snapshot", ProjectID: "project-ci", Name: "Leaked CI snapshot", Version: "1.0.0", Protocol: "http",
		GeneratedBy: ciLibraryScriptGenerator,
		Config:      dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions:     []dsl.Action{{Name: "health", Type: "request", Method: "GET", Target: "/health"}},
	}
	if err := store.SaveScript(leaked); err != nil {
		t.Fatal(err)
	}
	if err := server.runner.reloadStoredScripts(); err != nil {
		t.Fatal(err)
	}
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("1"), ScriptIdentity: "script_id:" + leaked.ID,
		PolicyRevision: policy.Revision, IdempotencySHA: sha256String("leaked-snapshot-recovery"),
	}
	key, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: "db20ee09-c422-4ec6-b5c0-e22344dfa30a", Name: "Leaked snapshot recovery", ProjectID: "project-ci", ScriptID: leaked.ID,
		VUsers: 1, Duration: "1m", MaterializationKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	spy := &executionBackendSpy{called: make(chan string, 1)}
	restarted := NewTestRunner(4, NewHub(), store, WithExecutionBackend(spy))
	select {
	case startedID := <-spy.called:
		if startedID != queued.ID {
			t.Fatalf("recovered run id = %q, want %q", startedID, queued.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued CI run did not recover after leaked snapshot cleanup")
	}
	durable, err := store.ListScripts("project-ci", nil)
	if err != nil {
		t.Fatal(err)
	}
	foundUserOwned := false
	for _, script := range durable {
		if script != nil && script.ID == leaked.ID {
			t.Fatalf("leaked CI snapshot remained durable: %#v", script)
		}
		if script != nil && script.ID == userOwned.ID {
			foundUserOwned = true
		}
	}
	if !foundUserOwned {
		t.Fatal("legacy cleanup deleted a user-owned script whose id only resembled a CI snapshot")
	}
	recovered, ok := restarted.GetTest(queued.ID)
	if !ok || recovered.Status == "QUEUED" || len(recovered.Groups) != 1 || recovered.Groups[0].script == nil {
		t.Fatalf("recovered queued run = %#v", recovered)
	}
}

func TestCIRunOutcomeMapping(t *testing.T) {
	tests := []struct {
		name, status, want string
		breached, results  bool
		metadata           ciRunMetadata
		slaEvaluated       bool
		testError          string
	}{
		{name: "complete passed", status: "COMPLETE", results: true, want: "passed"},
		{name: "complete SLA passed", status: "COMPLETE", slaEvaluated: true, results: true, metadata: ciRunMetadata{RequireSLA: true}, want: "passed"},
		{name: "complete SLA breach", status: "COMPLETE", breached: true, slaEvaluated: true, results: true, want: "sla_breached"},
		{name: "complete required SLA without evaluation", status: "COMPLETE", results: true, metadata: ciRunMetadata{RequireSLA: true}, want: "execution_error"},
		{name: "complete required SLA without results", status: "COMPLETE", metadata: ciRunMetadata{RequireSLA: true}, want: "execution_error"},
		{name: "complete required results missing", status: "COMPLETE", metadata: ciRunMetadata{RequireResults: true}, want: "execution_error"},
		{name: "complete optional empty", status: "COMPLETE", want: "passed"},
		{name: "failed", status: "FAILED", want: "execution_error"},
		{name: "aborted", status: "ABORTED", want: "cancelled"},
		{name: "timed out abort", status: "ABORTED", testError: "ci abort reason: timed_out", want: "timed_out"},
		{name: "queued", status: "QUEUED", want: "queued"},
		{name: "running", status: "RUNNING", want: "running"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ciRunOutcome(test.status, test.breached, test.slaEvaluated, test.results, test.metadata, test.testError); got != test.want {
				t.Fatalf("ciRunOutcome = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCIRunCompleteRequiresValidatedDurableEvidence(t *testing.T) {
	tests := []struct {
		name          string
		prepare       func(*testing.T, storage.Store, *ManagedTest)
		wantOutcome   string
		wantReportURL bool
	}{
		{name: "missing", wantOutcome: "execution_error"},
		{name: "tampered artifact", prepare: func(t *testing.T, store storage.Store, test *ManagedTest) {
			record := persistValidCIRunArchive(t, store, test, 2, true, false, nil)
			manifest, err := loadRunArchiveManifest(record.ManifestPath)
			if err != nil {
				t.Fatalf("loadRunArchiveManifest: %v", err)
			}
			if err := os.WriteFile(filepath.Join(record.ArchivePath, manifest.TransactionsFile), []byte("tampered\n"), 0o644); err != nil {
				t.Fatalf("tamper archive artifact: %v", err)
			}
		}, wantOutcome: "execution_error"},
		{name: "cross-project archive identity", prepare: func(t *testing.T, store storage.Store, test *ManagedTest) {
			record := persistValidCIRunArchive(t, store, test, 2, true, false, nil)
			rewriteCIRunArchiveManifest(t, store, record, func(manifest *RunArchiveManifest) {
				manifest.Provenance.ProjectID = "another-project"
			})
		}, wantOutcome: "execution_error"},
		{name: "stale archive identity", prepare: func(t *testing.T, store storage.Store, test *ManagedTest) {
			record := persistValidCIRunArchive(t, store, test, 2, true, false, nil)
			rewriteCIRunArchiveManifest(t, store, record, func(manifest *RunArchiveManifest) {
				manifest.Provenance.RequestedAt = test.CreatedAt.Add(-time.Hour)
			})
		}, wantOutcome: "execution_error"},
		{name: "missing requested-at identity", prepare: func(t *testing.T, store storage.Store, test *ManagedTest) {
			record := persistValidCIRunArchive(t, store, test, 2, true, false, nil)
			rewriteCIRunArchiveManifest(t, store, record, func(manifest *RunArchiveManifest) {
				manifest.Provenance.RequestedAt = time.Time{}
			})
		}, wantOutcome: "execution_error"},
		{name: "valid", prepare: func(t *testing.T, store storage.Store, test *ManagedTest) {
			persistValidCIRunArchive(t, store, test, 2, true, false, nil)
		}, wantOutcome: "passed", wantReportURL: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
			testRun := createTerminalCIContractTest(t, server, store, "COMPLETE", "")
			if testCase.prepare != nil {
				testCase.prepare(t, store, testRun)
			}
			response, err := server.buildCIRunResponse(context.Background(), testRun)
			if err != nil {
				t.Fatalf("buildCIRunResponse: %v", err)
			}
			if response.Outcome != testCase.wantOutcome {
				t.Fatalf("outcome = %q, want %q; response=%#v", response.Outcome, testCase.wantOutcome, response)
			}
			if (response.ReportURL != "") != testCase.wantReportURL {
				t.Fatalf("report_url = %q, want present=%t", response.ReportURL, testCase.wantReportURL)
			}
			if testCase.wantOutcome == "execution_error" {
				if response.Error != ciTerminalEvidenceFailureReason {
					t.Fatalf("safe evidence error = %q, want %q", response.Error, ciTerminalEvidenceFailureReason)
				}
				summary := ciGitHubCheckSummary(testRun, response, ciRunMetadata{Source: ciTestRunSource("1")})
				if !strings.Contains(summary, "Execution error: "+ciTerminalEvidenceFailureReason) {
					t.Fatalf("Check summary omitted safe evidence reason: %s", summary)
				}
			} else if response.Error != "" || response.Transactions != 2 || !response.SLAEvaluated {
				t.Fatalf("valid durable evidence response = %#v", response)
			}
		})
	}
}

func TestCIRunArchiveRestoresTerminalErrorsAndTPSWithoutLiveMetrics(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
	testRun := createTerminalCIContractTest(t, server, store, "COMPLETE", "")

	writer, err := newRunArchiveWriter(testRun, RunArchiveProvenance{ProjectID: testRun.ProjectID, RequestedAt: testRun.CreatedAt})
	if err != nil {
		t.Fatalf("newRunArchiveWriter: %v", err)
	}
	started := time.Now().UTC()
	for index := 0; index < 7; index++ {
		if err := writer.RecordResult(&protocol.Result{ActionName: "request", StartTime: started.Add(time.Duration(index) * time.Millisecond), Duration: time.Millisecond, Success: index >= 3}); err != nil {
			t.Fatalf("RecordResult: %v", err)
		}
	}
	if err := writer.RecordIntervalSummary(&LiveMetrics{TotalTx: 7, TotalErrors: 3, TPS: 4.25}); err != nil {
		t.Fatalf("RecordIntervalSummary: %v", err)
	}
	record, err := writer.Finalize("COMPLETE", started.Add(time.Second), "", true, false, nil)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatalf("SaveRunArchive: %v", err)
	}

	server.runner.mu.Lock()
	server.runner.tests[testRun.ID].LiveMetrics = nil
	server.runner.mu.Unlock()
	testRun, _ = server.runner.GetTest(testRun.ID)
	response, err := server.buildCIRunResponse(context.Background(), testRun)
	if err != nil {
		t.Fatalf("buildCIRunResponse: %v", err)
	}
	if response.Outcome != "passed" || response.Transactions != 7 || response.Errors != 3 || response.TPS != 4.25 {
		t.Fatalf("durable terminal metrics were not restored: %#v", response)
	}
}

func TestCIRunArchiveMergePreservesCumulativeSLAEvidence(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
	testRun := createTerminalCIContractTest(t, server, store, "COMPLETE", "")
	server.runner.mu.Lock()
	managed := server.runner.tests[testRun.ID]
	managed.ciSLAEvaluated = true
	managed.ciSLABreached = true
	managed.ciSLADetails = []string{"earlier P95 latency breach"}
	server.runner.mu.Unlock()
	testRun, _ = server.runner.GetTest(testRun.ID)
	persistValidCIRunArchive(t, store, testRun, 1, false, false, []string{"archive detail"})

	response, err := server.buildCIRunResponse(context.Background(), testRun)
	if err != nil {
		t.Fatalf("buildCIRunResponse: %v", err)
	}
	if response.Outcome != "sla_breached" || !response.SLAEvaluated || !response.SLABreached {
		t.Fatalf("cumulative SLA state was erased by archive merge: %#v", response)
	}
	for _, detail := range []string{"earlier P95 latency breach", "archive detail"} {
		if !slices.Contains(response.SLADetails, detail) {
			t.Fatalf("merged SLA details = %#v, missing %q", response.SLADetails, detail)
		}
	}
}

func TestCIRunNonCompleteTerminalMappingsSurviveMissingEvidence(t *testing.T) {
	for _, testCase := range []struct {
		status, runError, outcome string
	}{
		{status: "FAILED", runError: "generator failed", outcome: "execution_error"},
		{status: "ABORTED", runError: "ci abort reason: cancelled", outcome: "cancelled"},
		{status: "ABORTED", runError: "ci abort reason: timed_out", outcome: "timed_out"},
	} {
		t.Run(testCase.status+"/"+testCase.outcome, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
			testRun := createTerminalCIContractTest(t, server, store, testCase.status, testCase.runError)
			response, err := server.buildCIRunResponse(context.Background(), testRun)
			if err != nil {
				t.Fatalf("buildCIRunResponse: %v", err)
			}
			if response.Outcome != testCase.outcome || response.Error != testCase.runError {
				t.Fatalf("non-COMPLETE mapping changed: %#v", response)
			}
			if response.ReportURL != "" {
				t.Fatalf("missing terminal evidence exposed report_url %q", response.ReportURL)
			}
		})
	}
}

func TestCIRunExternalEvidenceScrubsAndBoundsBackendErrors(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
	rawError := "dial https://user:p4ssw0rd@example.test/load?access_token=hunter2: " + strings.Repeat("秘密", ciExternalErrorMaxBytes)
	testRun := createTerminalCIContractTest(t, server, store, "FAILED", rawError)

	response, err := server.buildCIRunResponse(context.Background(), testRun)
	if err != nil {
		t.Fatalf("buildCIRunResponse: %v", err)
	}
	if len(response.Error) > ciExternalErrorMaxBytes || !utf8.ValidString(response.Error) {
		t.Fatalf("external error is not bounded valid UTF-8: %d bytes", len(response.Error))
	}
	for _, secret := range []string{"p4ssw0rd", "hunter2"} {
		if strings.Contains(response.Error, secret) {
			t.Fatalf("external CI error leaked credential %q: %s", secret, response.Error)
		}
	}
	if !strings.Contains(response.Error, scrub.Redacted) || !strings.HasSuffix(response.Error, ciExternalTruncatedSuffix) {
		t.Fatalf("external CI error was not scrubbed and bounded: %q", response.Error)
	}
	summary := ciGitHubCheckSummary(testRun, response, ciRunMetadata{Source: ciTestRunSource("1")})
	for _, secret := range []string{"p4ssw0rd", "hunter2"} {
		if strings.Contains(summary, secret) {
			t.Fatalf("GitHub Check summary leaked credential %q", secret)
		}
	}
}

func TestAbortCIRunPersistsReasonAtomicallyAndPreservesTerminalEvidence(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	script := ciTestScript("https://api.example.test")
	script.ID = "ci-abort-reason-script"
	runner.StoreScript(script)
	queued, err := runner.CreateTest(CreateTestRequest{Name: "Timeout", ScriptID: script.ID, VUsers: 1, Duration: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.AbortCIRun(queued.ID, "timed_out"); err != nil {
		t.Fatal(err)
	}
	aborted, ok := runner.GetTest(queued.ID)
	if !ok || aborted.Status != "ABORTED" || aborted.Error != "ci abort reason: timed_out" {
		t.Fatalf("atomic CI abort state = %#v", aborted)
	}
	terminal, err := runner.CreateTest(CreateTestRequest{Name: "Already complete", ScriptID: script.ID, VUsers: 1, Duration: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.tests[terminal.ID].Status = "COMPLETE"
	runner.tests[terminal.ID].Error = "authoritative result"
	runner.mu.Unlock()
	if err := runner.AbortCIRun(terminal.ID, "timed_out"); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := runner.GetTest(terminal.ID)
	if unchanged.Status != "COMPLETE" || unchanged.Error != "authoritative result" {
		t.Fatalf("terminal CI evidence was overwritten: %#v", unchanged)
	}
}

func TestCISLAEvidenceRequiresEveryConfiguredMetric(t *testing.T) {
	p95 := 250.0
	errorPct := 5.0
	serverProcessing := 100.0
	transfer := 50.0
	errorCount := 10
	config := &GoalConfig{
		P95LatencyMs:             &p95,
		MaxErrorPct:              &errorPct,
		MaxAvgServerProcessingMs: &serverProcessing,
		MaxAvgTransferMs:         &transfer,
		MaxErrorCount:            &errorCount,
	}
	live := &LiveMetrics{
		TotalTx:     1,
		TotalErrors: 0,
		Transactions: map[string]*TxMetrics{
			"checkout": {ServerTelemetry: &ServerTelemetrySummary{Samples: 0, AvgServerProcessingMs: 20, AvgTransferMs: 5}},
		},
	}
	if ciSLAEvidenceComplete(config, live) {
		t.Fatal("zero-sample server telemetry satisfied configured SLA evidence")
	}
	live.Transactions["checkout"].ServerTelemetry.Samples = 1
	if !ciSLAEvidenceComplete(config, live) {
		t.Fatal("complete transaction and server telemetry did not satisfy configured SLA evidence")
	}
	live.TotalTx = 0
	live.TotalErrors = 2
	if ciSLAEvidenceComplete(config, live) {
		t.Fatal("error-only results satisfied a configured P95 latency threshold")
	}
}

func TestRecordCISLAStateRetainsEarlierBreach(t *testing.T) {
	p95 := 200.0
	testRun := &ManagedTest{GoalConfig: &GoalConfig{P95LatencyMs: &p95}}
	recordCISLAStateLocked(testRun, &LiveMetrics{
		TotalTx: 1,
		GoalStatus: &GoalStatus{
			LatencyBreached: true,
		},
	})
	recordCISLAStateLocked(testRun, &LiveMetrics{TotalTx: 2, GoalStatus: &GoalStatus{}})
	if !testRun.ciSLAEvaluated || !testRun.ciSLABreached {
		t.Fatalf("cumulative SLA state = evaluated:%t breached:%t", testRun.ciSLAEvaluated, testRun.ciSLABreached)
	}
	if len(testRun.ciSLADetails) != 1 || testRun.ciSLADetails[0] != "P95 latency breached threshold" {
		t.Fatalf("cumulative SLA details = %#v", testRun.ciSLADetails)
	}
}

func TestCIClaimsMatchRunAttemptAndImmutableSource(t *testing.T) {
	identity := ciTestIdentity()
	claims := &ciBearerClaims{
		ProjectID: "project-ci", Repository: identity.Repository, RepositoryID: identity.RepositoryID,
		Ref: identity.Ref, SHA: identity.SHA, WorkflowRef: identity.WorkflowRef,
		RunID: identity.RunID, RunAttempt: identity.RunAttempt, Actor: identity.Actor, EventName: identity.EventName,
		Environment: identity.Environment, RunnerEnvironment: identity.RunnerEnvironment,
	}
	source := ciTestRunSource("1")
	source.Repository = "Octo/Apex"
	if err := claims.matchCIRunSource("project-ci", source); err != nil {
		t.Fatalf("matchCIRunSource rejected matching source: %v", err)
	}
	source.Actor = "mallory"
	if err := claims.matchCIRunSource("project-ci", source); err == nil || !strings.Contains(err.Error(), "actor") {
		t.Fatalf("matchCIRunSource actor error = %v", err)
	}
	source.Actor = "octocat"
	source.RunAttempt = "2"
	if err := claims.matchCIRunSource("project-ci", source); err == nil || !strings.Contains(err.Error(), "run_attempt") {
		t.Fatalf("matchCIRunSource attempt error = %v", err)
	}
	source.RunAttempt = identity.RunAttempt
	source.Environment = "staging"
	if err := claims.matchCIRunSource("project-ci", source); err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("matchCIRunSource environment error = %v", err)
	}
	source.Environment = identity.Environment
	source.RunnerEnvironment = "self-hosted"
	if err := claims.matchCIRunSource("project-ci", source); err == nil || !strings.Contains(err.Error(), "runner_environment") {
		t.Fatalf("matchCIRunSource runner environment error = %v", err)
	}
	source.RunnerEnvironment = identity.RunnerEnvironment
	claims.JobWorkflowRef = "octo/automation/.github/workflows/performance.yml@refs/heads/main"
	source.JobWorkflowRef = claims.JobWorkflowRef
	if err := claims.matchCIRunSource("project-ci", source); err != nil {
		t.Fatalf("matchCIRunSource rejected matching reusable workflow: %v", err)
	}
	source.JobWorkflowRef = "octo/automation/.github/workflows/untrusted.yml@refs/heads/main"
	if err := claims.matchCIRunSource("project-ci", source); err == nil || !strings.Contains(err.Error(), "job_workflow_ref") {
		t.Fatalf("matchCIRunSource reusable workflow error = %v", err)
	}
}

func TestValidateCIRunIdempotencyKeyBoundary(t *testing.T) {
	if _, err := validateCIRunIdempotencyKey(strings.Repeat("a", 16)); err != nil {
		t.Fatalf("minimum valid key rejected: %v", err)
	}
	for _, raw := range []string{strings.Repeat("a", 15), strings.Repeat("a", 513), "valid-looking-key\nforged"} {
		if _, err := validateCIRunIdempotencyKey(raw); err == nil {
			t.Fatalf("invalid key accepted: %q", raw)
		}
	}
}

func TestCIRunDurationPolicyRejectsOverflowingPolicyLimit(t *testing.T) {
	req := ciTestRunRequest()
	req.Execution.Duration = time.Minute.String()
	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = int(^uint(0) >> 1)
	if err := validateCIRunRequest(req, policy, ciTestScript("https://api.example.test")); err == nil || !strings.Contains(err.Error(), "duration exceeds") {
		t.Fatalf("overflowing policy duration error = %v", err)
	}
}

func TestCollectCITargetHostsIncludesKafkaOAuth(t *testing.T) {
	script := &dsl.Script{Protocol: "kafka", Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{
		Brokers: []string{"broker.example.test:9093"},
		OAuth:   &dsl.KafkaOAuthConfig{TokenURL: "https://identity.example.test/token"},
	}}}
	hosts, deferred, err := collectCITargetHosts(script, "")
	if err != nil || deferred || len(hosts) != 2 {
		t.Fatalf("hosts=%v deferred=%v err=%v", hosts, deferred, err)
	}
	found := false
	for _, host := range hosts {
		if host == "identity.example.test" {
			found = true
		}
	}
	if !found {
		t.Fatal("token destination omitted from outbound policy", hosts)
	}
}
