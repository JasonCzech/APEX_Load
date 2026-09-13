package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestParameterPreviewEngineSemantics(t *testing.T) {
	for _, test := range []struct {
		name                        string
		parameter                   dsl.Parameter
		user, iteration, occurrence int
		want                        string
		masked                      bool
	}{
		{"iteration", dsl.Parameter{Name: "iteration", Type: "current_iteration"}, 1, 3, 1, "3", false},
		{"user", dsl.Parameter{Name: "user", Type: "vuser_id"}, 4, 1, 1, "4", false},
		{"once", dsl.Parameter{Name: "counter", Type: "counter", Start: 7, Step: 2, Update: "once"}, 1, 5, 1, "7", false},
		{"occurrence", dsl.Parameter{Name: "counter", Type: "counter", Start: 7, Step: 2, Update: "each_occurrence"}, 1, 1, 3, "11", false},
		{"credential", dsl.Parameter{Name: "password", Type: "list", Values: []string{"never-return-this"}}, 1, 1, 1, "[REDACTED]", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(parameterPreviewRequest{Parameters: []dsl.Parameter{test.parameter}, Name: test.parameter.Name, VUser: test.user, Iteration: test.iteration, Occurrence: test.occurrence})
			response := httptest.NewRecorder()
			(&Server{}).handlePreviewParameter(response, httptest.NewRequest("POST", "/api/v1/dsl/parameter-preview", bytes.NewReader(body)))
			if response.Code != 200 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var result parameterPreviewResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !result.Resolved || result.Value != test.want || result.Masked != test.masked {
				t.Fatalf("unexpected preview: %+v", result)
			}
		})
	}
}

func TestParameterPreviewInlineAllocationAndExternalBoundary(t *testing.T) {
	request := parameterPreviewRequest{Name: "user", VUser: 2, Iteration: 2, Occurrence: 1,
		Parameters:  []dsl.Parameter{{Name: "user", Type: "file", DataSource: "users", Column: "id", BlockSize: 2}},
		DataSources: []dsl.DataSource{{Name: "users", Type: "csv", FirstRowHeader: true, InlineData: "id\na\nb\nc\nd\n"}},
	}
	call := func() (int, parameterPreviewResponse) {
		body, _ := json.Marshal(request)
		response := httptest.NewRecorder()
		(&Server{}).handlePreviewParameter(response, httptest.NewRequest("POST", "/api/v1/dsl/parameter-preview", bytes.NewReader(body)))
		var result parameterPreviewResponse
		_ = json.Unmarshal(response.Body.Bytes(), &result)
		return response.Code, result
	}
	if status, result := call(); status != 200 || result.Value != "d" {
		t.Fatalf("allocation: %d %+v", status, result)
	}
	request.DataSources[0].InlineData = ""
	request.DataSources[0].Path = "/etc/passwd"
	if status, result := call(); status != 200 || result.Resolved || result.Value != "" {
		t.Fatalf("external source must not be opened: %d %+v", status, result)
	}
	request.VUser = 0
	if status, _ := call(); status != 400 {
		t.Fatalf("zero user accepted: %d", status)
	}
	request.VUser = 1
	request.Parameters[0].SameLineAs = "user"
	if status, _ := call(); status != 400 {
		t.Fatalf("cycle accepted: %d", status)
	}
}

func TestParameterPreviewRejectsInvalidAndOversizedGeneration(t *testing.T) {
	for _, parameter := range []dsl.Parameter{
		{Name: "value", Type: "random_string", Length: 4097},
		{Name: "value", Type: "counter", PadWidth: 4097},
		{Name: "value", Type: "random_string", Length: -1},
		{Name: "value", Type: "unsupported"},
		{Name: "value", Type: "date_time", Format: strings.Repeat("x", 4097)},
	} {
		body, _ := json.Marshal(parameterPreviewRequest{Parameters: []dsl.Parameter{parameter}, Name: "value", VUser: 1, Iteration: 1, Occurrence: 1})
		response := httptest.NewRecorder()
		(&Server{}).handlePreviewParameter(response, httptest.NewRequest("POST", "/api/v1/dsl/parameter-preview", bytes.NewReader(body)))
		if response.Code != 400 {
			t.Fatalf("accepted invalid parameter %+v: %s", parameter, response.Body.String())
		}
	}
}
