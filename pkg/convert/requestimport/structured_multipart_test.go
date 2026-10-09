package requestimport

import (
	"encoding/base64"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Uploads inside control blocks become file data sources like top-level
// ones; templated text and text fields stay in the request.
func TestHoistMultipartFilesIncludesNestedSteps(t *testing.T) {
	upload := func(name string) dsl.Action {
		return dsl.Action{Name: name, Type: "request", Method: "POST", Target: "/upload", BodyMultipart: &dsl.MultipartBody{Parts: []dsl.MultipartPart{
			{Name: "title", Value: "Report {{var.q}}"},
			{Name: "doc", Filename: "a.bin", Value: base64.StdEncoding.EncodeToString([]byte{0, 1}), ValueEncoding: "base64"},
		}}}
	}
	script := &dsl.Script{Actions: []dsl.Action{
		upload("top"),
		{Name: "branch", Type: "control_if", Control: &dsl.ControlFlow{Steps: []dsl.Action{upload("nested")}}},
	}}
	HoistMultipartFiles(script)
	if len(script.DataSources) != 2 || script.DataSources[0].Name != "upload_doc" || script.DataSources[1].Name != "upload_doc_2" {
		t.Fatalf("data sources = %+v", script.DataSources)
	}
	nested := script.Actions[1].Control.Steps[0].BodyMultipart.Parts
	if nested[1].Value != "{{file.upload_doc_2}}" || nested[1].ValueEncoding != "" || nested[0].Value != "Report {{var.q}}" {
		t.Fatalf("nested parts = %+v", nested)
	}
}
