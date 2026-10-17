package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func previewUniqueParameter(t *testing.T, parameter dsl.Parameter, vuser int) parameterPreviewResponse {
	t.Helper()
	body, _ := json.Marshal(parameterPreviewRequest{Parameters: []dsl.Parameter{parameter}, Name: parameter.Name, VUser: vuser, Iteration: 1, Occurrence: 1})
	response := httptest.NewRecorder()
	(&Server{}).handlePreviewParameter(response, httptest.NewRequest("POST", "/api/v1/dsl/parameter-preview", bytes.NewReader(body)))
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result parameterPreviewResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestParameterPreviewExplainsAutomaticUniqueAllocation(t *testing.T) {
	rows := []string{"A1", "A2", "A3", "A4"}
	for name, parameter := range map[string]dsl.Parameter{
		"unique without settings": {Name: "account", Type: "list", Selection: "unique", Values: rows},
		"explicit auto_allocate":  {Name: "account", Type: "list", Selection: "unique", AutoAllocate: true, Values: rows},
	} {
		t.Run(name, func(t *testing.T) {
			result := previewUniqueParameter(t, parameter, 2)
			if result.Resolved || result.Value != "" || !strings.Contains(result.Note, "allocated to each virtual user when a run starts") {
				t.Fatalf("preview = %+v, want the automatic-allocation note", result)
			}
		})
	}
	// A fixed block does not depend on the run, so it still previews: the
	// second virtual user's block starts at the third row.
	fixed := dsl.Parameter{Name: "account", Type: "list", Selection: "unique", BlockSize: 2, Values: rows}
	if result := previewUniqueParameter(t, fixed, 2); !result.Resolved || result.Value != "A3" {
		t.Fatalf("fixed block preview = %+v, want A3", result)
	}
}
