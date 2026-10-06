package generatorrpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/importartifact"
	_ "github.com/jasonczech/apexload/pkg/protocol/builtin"
)

func TestSupportedProtocolNamesHideBrunoWithoutArtifactDelivery(t *testing.T) {
	restore := importartifact.SetResolver(nil)
	t.Cleanup(restore)
	t.Setenv("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED", "true")
	t.Setenv("APEXLOAD_BRUNO_RUNNER_OWNER_LEASE_V1", "true")
	if containsProtocol(supportedProtocolNames(), "bruno") {
		t.Fatal("generator advertised Bruno without an artifact resolver")
	}
}

func TestSupportedProtocolNamesAdvertiseConfiguredBrunoRunner(t *testing.T) {
	runner := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/health":
			_ = json.NewEncoder(response).Encode(map[string]any{"compatibility_version": "3.5.2"})
		case "/v1/capabilities":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"compatibility_version": "3.5.2",
				"cleanup_failed":        false,
				"features":              []string{"runner_owner_lease_v1", "bruno_session_lease_v1"},
			})
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(runner.Close)
	restore := importartifact.SetResolver(importartifact.ResolverFunc(func(context.Context, importartifact.Request) ([]byte, error) {
		return []byte("bundle"), nil
	}))
	t.Cleanup(restore)
	t.Setenv("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED", "true")
	t.Setenv("APEXLOAD_BRUNO_RUNNER_OWNER_LEASE_V1", "true")
	t.Setenv("APEXLOAD_BRUNO_RUNNER_URL", runner.URL)
	t.Setenv("APEXLOAD_BRUNO_EGRESS_BROKER_URL", "http://egress-broker.internal:8080")
	if !containsProtocol(supportedProtocolNames(), "bruno") {
		t.Fatal("configured generator did not advertise Bruno")
	}
	features := compatibleFeatureNames()
	if !containsProtocol(features, "bruno_runner_3.5.2") ||
		!containsProtocol(features, "bruno_governed_egress") ||
		!containsProtocol(features, "bruno_shared_encrypted_artifacts_v1") {
		t.Fatalf("Bruno compatibility features = %#v", features)
	}
}

func TestAssignWorkRejectsBrunoWhenDistributedDeliveryIsUnavailable(t *testing.T) {
	restore := importartifact.SetResolver(nil)
	t.Cleanup(restore)
	t.Setenv("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED", "false")
	service := newIsolatedService(t, 10, WithExecution())
	script := []byte(`{
		"name":"bruno",
		"project_id":"project-a",
		"protocol":"bruno",
		"config":{"bruno":{
			"bundle_ref":"artifact://bundle",
			"bundle_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"compatibility_version":"3.5.2",
			"sandbox":"safe"
		}},
		"actions":[{"name":"Run","type":"bruno_run","target":".","params":{"selection":"collection"}}]
	}`)
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:        "bruno-run",
		GroupName:     "default",
		ScriptId:      "bruno-script",
		ScriptContent: script,
		VuserCount:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ack.GetAccepted() || !strings.Contains(strings.ToLower(ack.GetErrorMessage()), "bruno runtime") {
		t.Fatalf("Bruno assignment ack = %#v", ack)
	}
}

func TestAssignWorkRequiresBrunoRunnerLeaseContract(t *testing.T) {
	restore := importartifact.SetResolver(importartifact.ResolverFunc(func(context.Context, importartifact.Request) ([]byte, error) {
		return []byte("bundle"), nil
	}))
	t.Cleanup(restore)
	t.Setenv("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED", "true")
	t.Setenv("APEXLOAD_BRUNO_ALLOW_DIRECT_EGRESS", "true")
	script := []byte(`{
		"name":"bruno",
		"project_id":"project-a",
		"protocol":"bruno",
		"config":{"bruno":{
			"bundle_ref":"artifact://bundle",
			"bundle_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"compatibility_version":"3.5.2",
			"sandbox":"safe"
		}},
		"actions":[{"name":"Run","type":"bruno_run","target":".","params":{"selection":"collection"}}]
	}`)

	for _, test := range []struct {
		name              string
		ownerLeaseEnabled string
		features          []string
		wantAccepted      bool
	}{
		{name: "operator lease disabled", ownerLeaseEnabled: "false", features: []string{"runner_owner_lease_v1", "bruno_session_lease_v1"}},
		{name: "runner omits both leases", ownerLeaseEnabled: "true"},
		{name: "runner omits session lease", ownerLeaseEnabled: "true", features: []string{"runner_owner_lease_v1"}},
		{name: "runner omits owner lease", ownerLeaseEnabled: "true", features: []string{"bruno_session_lease_v1"}},
		{name: "runner requires both leases", ownerLeaseEnabled: "true", features: []string{"runner_owner_lease_v1", "bruno_session_lease_v1"}, wantAccepted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/v1/health":
					_ = json.NewEncoder(response).Encode(map[string]any{"compatibility_version": "3.5.2"})
				case "/v1/capabilities":
					_ = json.NewEncoder(response).Encode(map[string]any{
						"compatibility_version": "3.5.2",
						"cleanup_failed":        false,
						"features":              test.features,
					})
				default:
					http.NotFound(response, request)
				}
			}))
			defer runner.Close()
			t.Setenv("APEXLOAD_BRUNO_RUNNER_URL", runner.URL)
			t.Setenv("APEXLOAD_BRUNO_RUNNER_OWNER_LEASE_V1", test.ownerLeaseEnabled)

			service := newIsolatedService(t, 10, WithExecution())
			ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
				TestId:        "bruno-run-" + strings.ReplaceAll(test.name, " ", "-"),
				GroupName:     "default",
				ScriptId:      "bruno-script",
				ScriptContent: script,
				VuserCount:    1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if ack.GetAccepted() != test.wantAccepted {
				t.Fatalf("Bruno assignment accepted = %v, want %v (ack %#v)", ack.GetAccepted(), test.wantAccepted, ack)
			}
			if !test.wantAccepted && !strings.Contains(strings.ToLower(ack.GetErrorMessage()), "runner owner and session lease") {
				t.Fatalf("Bruno assignment error = %q, want lease contract rejection", ack.GetErrorMessage())
			}
		})
	}
}

func TestAssignWorkRejectsBrunoAboveRunnerSessionCapacity(t *testing.T) {
	restore := importartifact.SetResolver(importartifact.ResolverFunc(func(context.Context, importartifact.Request) ([]byte, error) {
		return []byte("bundle"), nil
	}))
	t.Cleanup(restore)
	t.Setenv("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED", "true")
	t.Setenv("APEXLOAD_BRUNO_EGRESS_BROKER_URL", "http://egress-broker.internal:8080")
	t.Setenv("APEXLOAD_BRUNO_MAX_SESSIONS", "1")
	service := newIsolatedService(t, 10)
	script := []byte(`{"name":"bruno","protocol":"bruno","actions":[{"name":"Run","type":"bruno_run"}]}`)
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:        "bruno-capacity",
		GroupName:     "default",
		ScriptId:      "bruno-script",
		ScriptContent: script,
		VuserCount:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ack.GetAccepted() || !strings.Contains(strings.ToLower(ack.GetErrorMessage()), "insufficient bruno runtime capacity") {
		t.Fatalf("Bruno capacity ack = %#v", ack)
	}
}

func containsProtocol(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), expected) {
			return true
		}
	}
	return false
}
