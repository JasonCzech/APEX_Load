package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/importsession"
)

func TestWriteProtectedImportStoreErrorIsActionableAndDisclosureSafe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		err       error
		code      string
		retryable bool
	}{
		{name: "unsupported Windows volume", err: importartifact.ErrUnsupportedFilesystem, code: "protected_import_store_unsupported_filesystem"},
		{name: "temporary storage failure", err: errors.New(`open C:\\Users\\operator\\secret: access denied`), code: "protected_import_store_unavailable", retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeProtectedImportStoreError(recorder, test.err)
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d", recorder.Code)
			}
			var payload struct {
				Error     string `json:"error"`
				Code      string `json:"code"`
				Retryable bool   `json:"retryable"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Code != test.code || payload.Retryable != test.retryable {
				t.Fatalf("payload = %#v", payload)
			}
			if payload.Error == "" || containsInsensitive(payload.Error, `C:\\Users`) {
				t.Fatalf("unsafe payload = %#v", payload)
			}
		})
	}
}

func TestProtectedImportStoreMarkerPreservesSemanticSessionErrors(t *testing.T) {
	t.Parallel()
	for _, semantic := range []error{
		context.Canceled,
		context.DeadlineExceeded,
		importsession.ErrSessionCapacity,
		importsession.ErrProjectDeleted,
		importsession.ErrSessionClaimed,
		importsession.ErrSessionClaimLost,
		importsession.ErrSessionExpired,
		importsession.ErrSessionScope,
	} {
		if got := markProtectedImportStoreUnavailable(semantic); got != semantic {
			t.Fatalf("markProtectedImportStoreUnavailable(%v) = %v", semantic, got)
		}
	}
	operational := errors.New("disk unavailable")
	if got := markProtectedImportStoreUnavailable(operational); !errors.Is(got, errProtectedImportStoreUnavailable) {
		t.Fatalf("operational error was not marked: %v", got)
	}
}

func TestSharedImportWriterPreservesProtectedStoreFailureReason(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "unsupported filesystem", err: importartifact.ErrUnsupportedFilesystem, code: "protected_import_store_unsupported_filesystem"},
		{name: "operational failure", err: markProtectedImportStoreUnavailable(errors.New("disk unavailable")), code: "protected_import_store_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeScriptImportResponse(recorder, http.StatusOK, map[string]any{
				importDraftResponseFailureKey:      true,
				importDraftResponseFailureErrorKey: test.err,
			})
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d", recorder.Code)
			}
			var payload map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["code"] != test.code {
				t.Fatalf("payload = %#v", payload)
			}
		})
	}
}

func TestSharedImportWriterPreservesSemanticSessionFailureStatus(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "project deleted", err: importsession.ErrProjectDeleted, status: http.StatusNotFound, code: "protected_import_session_unavailable"},
		{name: "expired", err: importsession.ErrSessionExpired, status: http.StatusNotFound, code: "protected_import_session_unavailable"},
		{name: "scope", err: importsession.ErrSessionScope, status: http.StatusNotFound, code: "protected_import_session_unavailable"},
		{name: "claim changed", err: importsession.ErrSessionClaimLost, status: http.StatusConflict, code: "protected_import_session_conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeScriptImportResponse(recorder, http.StatusOK, map[string]any{
				importDraftResponseFailureKey:      true,
				importDraftResponseFailureErrorKey: test.err,
			})
			if recorder.Code != test.status {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["code"] != test.code {
				t.Fatalf("payload = %#v", payload)
			}
		})
	}
}

func containsInsensitive(value, fragment string) bool {
	return len(fragment) > 0 && len(value) >= len(fragment) &&
		strings.Contains(strings.ToLower(value), strings.ToLower(fragment))
}
