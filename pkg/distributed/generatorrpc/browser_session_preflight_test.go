package generatorrpc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
)

// Staging opens each observer's session on its preflight page before the
// commit, whose start lead is too short for browsers; an observer that cannot
// open it rejects the assignment, named. Traffic-free preparation opens none.
func TestAssignmentStagingChecksObserverSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("launches browsers")
	}
	clearTransportEnv(t)
	t.Setenv(grpcTLSCAEnvVar, "configured-ca")
	t.Setenv(grpcAuthTokenEnvVar, strings.Repeat("t", minProductionAuthTokenBytes))
	var preflights atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/lightning/page/home" {
			preflights.Add(1)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>home</body></html>"))
	}))
	defer target.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	state := `{"cookies":[{"name":"sid","value":"session-cookie-value","domain":"127.0.0.1","path":"/","expires":-1,"httpOnly":true,"secure":false,"sameSite":"Lax"}],"origins":[]}`
	assignment := func(testID, baseURL string, prepareOnly bool) *pb.WorkAssignment {
		script := fmt.Sprintf(`{"name":"observer","protocol":"browser","config":{"base_url":%q,"browser":{"session":{"strategy":"storage_state_pool","preflight_url":"/lightning/page/home","identities":[{"username":"perf1@acme.test","storage_state":"{{secret.STATE_1}}"},{"username":"perf2@acme.test","storage_state":"{{secret.STATE_2}}"}]}}},"actions":[{"name":"Open","type":"browser_navigate","target":"/"}]}`, baseURL)
		return &pb.WorkAssignment{TestId: testID, GroupName: "observers", ScriptId: "observer-script-" + testID, ScriptContent: []byte(script), VuserCount: 2,
			ParameterPopulation: 2, PrepareOnly: prepareOnly,
			SecretBundle: map[string]string{"STATE_1": state, "STATE_2": state}, SecretBundleId: "observer-secrets-" + testID}
	}
	service := newIsolatedService(t, 10, WithExecution())

	if ack, err := service.AssignWork(context.Background(), assignment("prepared-observers", target.URL, true)); err != nil || !ack.GetAccepted() {
		t.Fatalf("prepare: %v %v", ack, err)
	}
	if got := preflights.Load(); got != 0 {
		t.Fatalf("traffic-free preparation opened %d preflight pages", got)
	}
	if ack, err := service.AssignWork(context.Background(), assignment("signed-in-observers", target.URL, false)); err != nil || !ack.GetAccepted() {
		t.Fatalf("assignment with valid sessions: %v %v", ack, err)
	}
	if got := preflights.Load(); got != 2 {
		t.Fatalf("staging opened %d preflight pages, want one per observer", got)
	}

	ack, err := service.AssignWork(context.Background(), assignment("unreachable-observers", closed.URL, false))
	message := ack.GetErrorMessage()
	if err != nil || ack.GetAccepted() || !strings.Contains(message, "observer 1 (perf1@acme.test) could not open the preflight page") ||
		!strings.Contains(message, "observer 2 (perf2@acme.test)") || !strings.Contains(message, "ERR_CONNECTION_REFUSED") || strings.ContainsAny(message, "\r\n") ||
		strings.Contains(message, "session-cookie-value") || strings.Contains(message, "rpc error") {
		t.Fatalf("assignment = %v, %v; want a rejection naming both observers", ack, err)
	}
	service.mu.RLock()
	_, committed := service.assignments["unreachable-observers"]
	service.mu.RUnlock()
	if committed {
		t.Fatal("a rejected observer assignment was committed")
	}
}
