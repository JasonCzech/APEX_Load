package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestEnterpriseArchiveDraftExpirationRequiresExplicitOptIn(t *testing.T) {
	for _, test := range []struct {
		name string
		url  string
		want bool
	}{
		{name: "default disabled", url: "/api/v1/scripts/import/enterprise-script-archive"},
		{name: "explicit opt in", url: "/api/v1/scripts/import/enterprise-script-archive?expire_draft=true", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.url, bytes.NewReader([]byte{'P', 'K', 0x05, 0x06}))
			request.Header.Set("Content-Type", "application/zip")
			response := httptest.NewRecorder()
			decoded, ok := readEnterpriseArchiveImportRequest(response, request)
			if !ok {
				t.Fatalf("decode failed: status=%d body=%s", response.Code, response.Body.String())
			}
			if decoded.ExpireDraft != test.want {
				t.Fatalf("ExpireDraft = %v, want %v", decoded.ExpireDraft, test.want)
			}
		})
	}
}

func TestSourceReviewValuePreservesCompletenessEvidence(t *testing.T) {
	review := sourceReviewValue(map[string]any{
		"total_source_files":   float64(4),
		"omitted_source_files": []any{"Support.java", "Helper.java"},
	})
	if review == nil {
		t.Fatal("sourceReviewValue returned nil for completeness-only review evidence")
	}
	if review.TotalSourceFiles != 4 {
		t.Fatalf("TotalSourceFiles = %d, want 4", review.TotalSourceFiles)
	}
	wantOmitted := []string{"Helper.java", "Support.java"}
	if !reflect.DeepEqual(review.OmittedSourceFiles, wantOmitted) {
		t.Fatalf("OmittedSourceFiles = %#v, want %#v", review.OmittedSourceFiles, wantOmitted)
	}
}
