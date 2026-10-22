package recordbridge

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

type retainedHTTPFixture struct {
	session *BrowserReplaySession
	err     error
	closed  int
	project string
}

func (f *retainedHTTPFixture) Session(_ context.Context, id, project string) (*BrowserReplaySession, error) {
	f.project = project
	return f.session, f.err
}
func (f *retainedHTTPFixture) HAR(_ context.Context, id, project string) (*BrowserReplaySession, ReplayHAR, error) {
	f.project = project
	if f.err != nil {
		return nil, ReplayHAR{}, f.err
	}
	har, err := NewVerifiedReplayHAR(bytes.NewReader([]byte("native")), 6, func() { f.closed++ })
	return f.session, har, err
}
func TestRetainedReplayHTTPPortOwnershipAndErrors(t *testing.T) {
	now := time.Now().UTC()
	terminal, err := RestoreRetainedReplaySession(&BrowserReplaySession{ID: "retained", ProjectID: "project", Status: BrowserReplayCompleted, CompletedAt: &now, FinalReport: &engine.ReplayReport{ReplaySessionID: "retained"}}, "retained", "project")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &retainedHTTPFixture{session: terminal}
	service := &Service{RetainedReplay: fixture, AuthorizedProject: func(*http.Request, string) string { return "project" }}
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/retained", nil)
		r.SetPathValue("session_id", "retained")
		return r
	}
	response := httptest.NewRecorder()
	service.HandleGetBrowserReplaySession(response, request())
	if response.Code != http.StatusOK || response.Header().Get("X-APEXLoad-Replay-Durability") != "durable" || fixture.project != "project" {
		t.Fatal("status lost scoped durability", response.Code)
	}
	response = httptest.NewRecorder()
	service.HandleDownloadBrowserReplayHAR(response, request())
	if response.Code != http.StatusOK || response.Body.String() != "native" || fixture.closed != 1 || fixture.project != "project" {
		t.Fatal("download lost ownership", response.Code, fixture.closed)
	}
	for _, tc := range []struct {
		name  string
		err   error
		code  int
		retry string
	}{
		{"missing", ErrRetainedReplayNotFound, http.StatusNotFound, ""},
		{"busy", ErrRetainedReplayBusy, http.StatusServiceUnavailable, "5"},
		{"unavailable", ErrRetainedReplayUnavailable, http.StatusServiceUnavailable, ""},
		{"private", errors.New("private-credential-detail"), http.StatusServiceUnavailable, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.err = tc.err
			for _, handler := range []func(http.ResponseWriter, *http.Request){service.HandleGetBrowserReplaySession, service.HandleDownloadBrowserReplayHAR, service.HandleDownloadBrowserReplayEvidence} {
				response := httptest.NewRecorder()
				handler(response, request())
				if response.Code != tc.code || response.Header().Get("Retry-After") != tc.retry || strings.Contains(response.Body.String(), "private-credential-detail") {
					t.Fatalf("retained error response: %d %s", response.Code, response.Body.String())
				}
			}
		})
	}
	service.RetainedReplay = nil
	response = httptest.NewRecorder()
	service.HandleGetBrowserReplaySession(response, request())
	if response.Code != http.StatusNotFound {
		t.Fatal("missing local-only session changed behavior", response.Code)
	}
}
