package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONImportReadersPreserveAuthorizedQueryProject(t *testing.T) {
	const projectID = "query-authorized-project"
	tests := []struct {
		name string
		read func(http.ResponseWriter, *http.Request) (string, bool)
	}{
		{name: "k6", read: func(w http.ResponseWriter, r *http.Request) (string, bool) {
			req, ok := readK6ImportRequest(w, r)
			if !ok {
				return "", false
			}
			return req.ProjectID, true
		}},
		{name: "devweb", read: func(w http.ResponseWriter, r *http.Request) (string, bool) {
			req, ok := readDevWebImportRequest(w, r)
			if !ok {
				return "", false
			}
			return req.ProjectID, true
		}},
		{name: "har", read: func(w http.ResponseWriter, r *http.Request) (string, bool) {
			req, ok := readHARImportRequest(w, r)
			if !ok {
				return "", false
			}
			return req.ProjectID, true
		}},
		{name: "recorder", read: func(w http.ResponseWriter, r *http.Request) (string, bool) {
			req, ok := readRecorderImportRequest(w, r)
			if !ok {
				return "", false
			}
			return req.ProjectID, true
		}},
		{name: "asyncapi", read: func(w http.ResponseWriter, r *http.Request) (string, bool) {
			req, ok := readAsyncAPIImportRequest(w, r)
			if !ok {
				return "", false
			}
			return req.ProjectID, true
		}},
		{name: "openapi", read: func(w http.ResponseWriter, r *http.Request) (string, bool) {
			req, ok := readOpenAPIImportRequest(w, r)
			if !ok {
				return "", false
			}
			return req.ProjectID, true
		}},
		{name: "raml", read: func(w http.ResponseWriter, r *http.Request) (string, bool) {
			req, ok := readRAMLImportRequest(w, r)
			if !ok {
				return "", false
			}
			return req.ProjectID, true
		}},
		{name: "wsdl", read: func(w http.ResponseWriter, r *http.Request) (string, bool) {
			req, ok := readWSDLImportRequest(w, r)
			if !ok {
				return "", false
			}
			return req.ProjectID, true
		}},
		{name: "postman", read: func(w http.ResponseWriter, r *http.Request) (string, bool) {
			req, ok := readPostmanImportRequest(w, r)
			if !ok {
				return "", false
			}
			return req.ProjectID, true
		}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/import?project_id="+projectID, strings.NewReader(`{"content":"{}"}`))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			got, ok := tc.read(resp, req)
			if !ok {
				t.Fatalf("reader rejected JSON wrapper: status=%d body=%s", resp.Code, resp.Body.String())
			}
			if got != projectID {
				t.Fatalf("project_id = %q, want authorized query project %q", got, projectID)
			}
		})
	}
}
