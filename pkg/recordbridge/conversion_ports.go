package recordbridge

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

// DraftPorts binds review operations to their exact candidate owner. A controller
// supplies durable claims; the standalone bridge supplies its memory owner.
type DraftPorts struct {
	Response        func(*http.Request, importreview.ScriptImportResponseOptions) map[string]any
	Claim           func(*http.Request, *dsl.Script) (*DraftClaim, error)
	WriteClaimError func(http.ResponseWriter, error) bool
}

type DraftClaim struct {
	Script   *dsl.Script
	Profile  string
	Complete func(*http.Request, *dsl.Script) (time.Time, error)
	Release  func()
}

// ConversionPorts keep controller admission, profile lookup and execution epochs
// outside the recorder conversion workflow.
type ConversionPorts struct {
	Drafts                  *DraftPorts
	RequireImportFeature    func(http.ResponseWriter, string, string) bool
	WriteImportResponse     func(http.ResponseWriter, int, map[string]any)
	ProjectRecordingProfile func(string, string) (*recordingprofile.Profile, error)
	SessionRecordingProfile func(*recorder.Session, string) (*recordingprofile.Profile, error)
	RequireExecutionLeader  func(http.ResponseWriter) bool
	ExecutionContext        func() (context.Context, context.CancelFunc)
	WriteReplayStartError   func(http.ResponseWriter, error)
}

func (s *Service) importResponse(r *http.Request, opts importreview.ScriptImportResponseOptions) map[string]any {
	return s.Conversion.Drafts.Response(r, opts)
}

func (s *Service) writeImportResponse(w http.ResponseWriter, status int, response map[string]any) {
	if s.Conversion.WriteImportResponse != nil {
		s.Conversion.WriteImportResponse(w, status, response)
		return
	}
	WriteJSON(w, status, response)
}

func (s *Service) writeImportResponseWithSourceSecrets(w http.ResponseWriter, r *http.Request, status int, opts importreview.ScriptImportResponseOptions) {
	s.writeImportResponse(w, status, s.importResponse(r, opts))
}

func (s *Service) requireImportFeature(w http.ResponseWriter, project, profile string) bool {
	return s.Conversion.RequireImportFeature == nil || s.Conversion.RequireImportFeature(w, project, profile)
}

func (s *Service) executionContext() (context.Context, context.CancelFunc) {
	if s.Conversion.ExecutionContext != nil {
		return s.Conversion.ExecutionContext()
	}
	return context.WithCancel(context.Background())
}

func (s *Service) validHTTPVersionMode(w http.ResponseWriter, mode string) bool {
	if _, err := requestimport.NormalizeHTTPVersionMode(mode); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func (s *Service) requireExecutionLeader(w http.ResponseWriter) bool {
	return s.Conversion.RequireExecutionLeader == nil || s.Conversion.RequireExecutionLeader(w)
}

func (s *Service) writeReplayStartError(w http.ResponseWriter, err error) {
	if s.Conversion.WriteReplayStartError != nil {
		s.Conversion.WriteReplayStartError(w, err)
		return
	}
	if errors.Is(err, ErrBrowserReplaySessionCapacity) || errors.Is(err, ErrBrowserReplayManagerClosed) || errors.Is(err, errLocalReplayCapacity) || errors.Is(err, errLocalReplayProjectCapacity) {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if errors.Is(err, importreview.ErrScriptReplayBlocked) {
		body := map[string]any{"error": err.Error(), "code": "script_replay_blocked"}
		if issues := importreview.ReplayBlockedIssues(err); issues != nil {
			body["issues"] = issues
		}
		WriteJSON(w, http.StatusConflict, body)
		return
	}
	if errors.Is(err, ErrBrowserReplaySessionIDConflict) || errors.Is(err, errLocalReplayIDConflict) {
		s.writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.writeError(w, http.StatusInternalServerError, "failed to start browser replay: "+err.Error())
}

func importProfile(script *dsl.Script) string {
	if script != nil && script.Metadata != nil && script.Metadata.Import != nil {
		return strings.TrimSpace(script.Metadata.Import.Source)
	}
	return ""
}
