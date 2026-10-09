package scriptdisclosure

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A body_multipart field named like a credential is masked, as it is in a
// recorded multipart body; other fields stay readable.
func TestProjectMasksSensitiveMultipartParts(t *testing.T) {
	script := &dsl.Script{
		Name: "upload", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "upload", Type: "request", Method: "POST", Target: "/upload",
			BodyMultipart: &dsl.MultipartBody{Parts: []dsl.MultipartPart{
				{Name: "description", Value: "quarterly report"},
				{Name: "password", Value: "hunter2-recorded-value"},
				{Name: "api_key", Value: base64.StdEncoding.EncodeToString([]byte("binary-key-value-123")), ValueEncoding: "base64"},
			}},
		}},
	}
	projected, err := Project(script)
	if err != nil {
		t.Fatal(err)
	}
	parts := projected.Actions[0].BodyMultipart.Parts
	if parts[0].Value != "quarterly report" {
		t.Fatalf("an ordinary field must stay readable: %q", parts[0].Value)
	}
	if strings.Contains(parts[1].Value, "hunter2") {
		t.Fatalf("the password part leaked: %q", parts[1].Value)
	}
	if parts[2].Value == script.Actions[0].BodyMultipart.Parts[2].Value {
		t.Fatalf("the base64 api_key part leaked: %q", parts[2].Value)
	}
	if script.Actions[0].BodyMultipart.Parts[1].Value != "hunter2-recorded-value" {
		t.Fatal("projection must not change the source script")
	}
}
