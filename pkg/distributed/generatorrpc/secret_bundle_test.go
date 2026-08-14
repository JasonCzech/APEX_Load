package generatorrpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestValidateSecretBundleBounds(t *testing.T) {
	if err := ValidateSecretBundle(map[string]string{"API_TOKEN": "value"}); err != nil {
		t.Fatalf("valid bundle: %v", err)
	}
	tests := []map[string]string{
		{"BAD NAME": "value"},
		{strings.Repeat("N", MaxAssignmentSecretNameBytes+1): "value"},
		{"API_TOKEN": ""},
		{"API_TOKEN": strings.Repeat("x", MaxAssignmentSecretValueBytes+1)},
	}
	for _, bundle := range tests {
		if err := ValidateSecretBundle(bundle); err == nil {
			t.Fatalf("ValidateSecretBundle(%v) returned nil", mapKeys(bundle))
		}
	}
	tooMany := make(map[string]string, MaxAssignmentSecrets+1)
	for index := 0; index <= MaxAssignmentSecrets; index++ {
		tooMany["SECRET_"+strings.Repeat("A", index/26)+string(rune('A'+index%26))] = "x"
	}
	if err := ValidateSecretBundle(tooMany); err == nil {
		t.Fatal("oversized secret count returned nil")
	}
	tooLarge := make(map[string]string)
	for index := 0; index < 17; index++ {
		tooLarge["SECRET_"+string(rune('A'+index))] = strings.Repeat("x", MaxAssignmentSecretValueBytes)
	}
	if err := ValidateSecretBundle(tooLarge); err == nil {
		t.Fatal("oversized aggregate secret material returned nil")
	}
}

func TestValidateAssignmentSecretReferencesRequiresExactBundle(t *testing.T) {
	tests := []struct {
		name       string
		referenced []string
		bundle     map[string]string
		wantError  string
	}{
		{name: "secret-free assignment"},
		{name: "referenced secret without bundle", referenced: []string{"API_TOKEN"}, wantError: "missing referenced logical secret"},
		{name: "complete bundle", referenced: []string{"API_TOKEN"}, bundle: map[string]string{"API_TOKEN": "value"}},
		{name: "unreferenced bundle entry", bundle: map[string]string{"API_TOKEN": "value"}, wantError: "unreferenced logical secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAssignmentSecretReferences(test.referenced, test.bundle)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("validateAssignmentSecretReferences: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validateAssignmentSecretReferences error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestGeneratorAdmissionRejectsSecretScriptWithoutBundleAndAllowsSecretFreeScript(t *testing.T) {
	service := newIsolatedService(t, 10, WithExecution())
	secretScript := []byte(`{"name":"requires-secret","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"request","type":"GET","target":"/?token={{secret.API_TOKEN}}"}]}`)
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:                "missing-secret-bundle",
		GroupName:             "group",
		ScriptId:              "secret-script",
		ScriptContent:         secretScript,
		VuserCount:            1,
		TargetDurationSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ack.GetAccepted() || !strings.Contains(ack.GetErrorMessage(), "missing referenced logical secret") {
		t.Fatalf("secret-bearing assignment acknowledgement = %#v", ack)
	}

	secretFreeScript := []byte(`{"name":"secret-free","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"request","type":"GET","target":"/"}]}`)
	ack, err = service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:                "secret-free-assignment",
		GroupName:             "group",
		ScriptId:              "secret-free-script",
		ScriptContent:         secretFreeScript,
		VuserCount:            1,
		TargetDurationSeconds: 1,
	})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("secret-free AssignWork = (%#v, %v)", ack, err)
	}
}

func TestWorkAssignmentFingerprintExcludesSecretBundleMetadataAndValues(t *testing.T) {
	first := &pb.WorkAssignment{
		TestId:         "test",
		GroupName:      "group",
		ScriptId:       "script",
		VuserCount:     1,
		SecretBundle:   map[string]string{"TOKEN": "first-value"},
		SecretBundleId: "first-bundle",
	}
	second := &pb.WorkAssignment{
		TestId:         "test",
		GroupName:      "group",
		ScriptId:       "script",
		VuserCount:     1,
		SecretBundle:   map[string]string{"TOKEN": "second-value"},
		SecretBundleId: "second-bundle",
	}
	if left, right := workAssignmentFingerprint(first), workAssignmentFingerprint(second); left != right {
		t.Fatal("assignment fingerprint changed with secret bundle metadata or values")
	}
}

func TestSecretBearingAssignmentRequiresAuthenticatedTLS(t *testing.T) {
	clearTransportEnv(t)
	service := newIsolatedService(t, 10)
	_, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:         "test",
		GroupName:      "group",
		ScriptId:       "script",
		SecretBundle:   map[string]string{"TOKEN": "value"},
		SecretBundleId: "bundle",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("AssignWork error = %v, want FailedPrecondition", err)
	}
}

func TestSecretBearingAssignmentLimitErrorDoesNotExposeCredentialLength(t *testing.T) {
	clearTransportEnv(t)
	t.Setenv(grpcTLSCAEnvVar, "configured-ca")
	t.Setenv(grpcAuthTokenEnvVar, strings.Repeat("t", minProductionAuthTokenBytes))
	service := newIsolatedService(t, 10, WithGeneratorScriptCacheByteLimit(1024))
	const secretValue = "credential-material-must-not-shape-the-error"
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:         "oversized-secret-assignment",
		GroupName:      "group",
		ScriptId:       "script",
		VuserCount:     1,
		SecretBundle:   map[string]string{"TOKEN": secretValue},
		SecretBundleId: "bundle",
		DataFiles: []*pb.DataFile{{
			Name:    "input.bin",
			Content: make([]byte, (1<<20)+2048),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ack.GetAccepted() || ack.GetErrorMessage() != "assignment data and metadata exceed the runtime limit" {
		t.Fatalf("oversized assignment acknowledgement = %#v", ack)
	}
	if strings.Contains(ack.GetErrorMessage(), secretValue) {
		t.Fatalf("assignment error exposed secret material: %q", ack.GetErrorMessage())
	}
}

func TestSecretBundleIdentityRejectsConflictingRetryWithoutFingerprintingSecrets(t *testing.T) {
	clearTransportEnv(t)
	t.Setenv(grpcTLSCAEnvVar, "configured-ca")
	t.Setenv(grpcAuthTokenEnvVar, strings.Repeat("t", minProductionAuthTokenBytes))
	service := newIsolatedService(t, 10)
	script := []byte(`{"name":"secret-script","version":"1.0.0","protocol":"http","actions":[{"name":"request","type":"GET","target":"/","headers":{"Authorization":"Bearer {{secret.TOKEN}}"}}]}`)
	first, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:         "test",
		GroupName:      "group",
		ScriptId:       "script",
		ScriptContent:  script,
		VuserCount:     1,
		SecretBundle:   map[string]string{"TOKEN": "first"},
		SecretBundleId: "bundle-one",
	})
	if err != nil || !first.GetAccepted() {
		t.Fatalf("first AssignWork = (%#v, %v)", first, err)
	}
	second, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:         "test",
		GroupName:      "group",
		ScriptId:       "script",
		ScriptContent:  script,
		VuserCount:     1,
		SecretBundle:   map[string]string{"TOKEN": "second"},
		SecretBundleId: "bundle-two",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.GetAccepted() || !strings.Contains(second.GetErrorMessage(), "already assigned") {
		t.Fatalf("conflicting retry = %#v", second)
	}
}

func TestGeneratorExecutesWithMemoryOnlySecretBundle(t *testing.T) {
	clearTransportEnv(t)
	t.Setenv(grpcTLSCAEnvVar, "configured-ca")
	t.Setenv(grpcAuthTokenEnvVar, strings.Repeat("t", minProductionAuthTokenBytes))
	const secretValue = "delivered-at-assignment"
	requests := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requests <- r.Header.Get("Authorization"):
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	script, err := json.Marshal(&dsl.Script{
		ID:       "script",
		Name:     "secret-runtime",
		Version:  "1.0.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: target.URL},
		Actions: []dsl.Action{{
			Name:    "request",
			Type:    "GET",
			Target:  "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.API_TOKEN}}"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := newIsolatedService(t, 10, WithExecution())
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:                "test",
		GroupName:             "group",
		TargetDurationSeconds: 1,
		ScriptId:              "script",
		ScriptContent:         script,
		VuserCount:            1,
		SecretBundle:          map[string]string{"API_TOKEN": secretValue},
		SecretBundleId:        "bundle",
	})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%#v, %v)", ack, err)
	}
	service.mu.RLock()
	runtimeRedaction := service.executions["test"]["group"].redactionConfig
	service.mu.RUnlock()
	if got := runtimeRedaction.ScrubText("remote echoed " + secretValue); strings.Contains(got, secretValue) {
		t.Fatalf("runtime terminal redaction exposed secret: %q", got)
	}
	commitTestWork(t, service, "test", 0)
	select {
	case got := <-requests:
		if got != "Bearer "+secretValue {
			t.Fatalf("Authorization = %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("generator did not execute secret-bearing assignment")
	}
}

func TestTransportSecurityAllowsSecretDeliveryOnlyWithAuthenticatedTLS(t *testing.T) {
	tests := []struct {
		name     string
		security TransportSecurity
		want     bool
	}{
		{name: "plaintext", security: TransportSecurity{}},
		{name: "plaintext token", security: TransportSecurity{Token: true}},
		{name: "server TLS only", security: TransportSecurity{TLS: true}},
		{name: "TLS token", security: TransportSecurity{TLS: true, Token: true}, want: true},
		{name: "mTLS", security: TransportSecurity{TLS: true, MutualTLS: true}, want: true},
		{name: "authenticated reverse tunnel", security: TransportSecurity{AuthenticatedTunnel: true}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.security.AllowsSecretDelivery(); got != test.want {
				t.Fatalf("AllowsSecretDelivery() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestGeneratorAdvertisesSecretBundleCapability(t *testing.T) {
	for _, feature := range compatibleFeatureNames() {
		if feature == FeatureSecretBundleV1 {
			return
		}
	}
	t.Fatalf("compatible features do not advertise %q", FeatureSecretBundleV1)
}

func mapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
