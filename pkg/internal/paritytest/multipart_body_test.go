package paritytest

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// body_multipart is built at replay: templates expand, a file part reads
// its file data source, and every request gets a fresh boundary.
func TestBodyMultipartReplay(t *testing.T) {
	server := NewServer([]Response{{Method: "POST", Path: "/upload", Status: 200, Body: "ok"}})
	defer server.Close()
	fileBytes := []byte{0x89, 'P', 'N', 'G', 0x00, 0xff}
	upload := dsl.Action{
		Name: "upload", Type: "request", Method: "POST", Target: "/upload",
		Headers: map[string]string{"Content-Type": "multipart/form-data; boundary=recorded"},
		BodyMultipart: &dsl.MultipartBody{Parts: []dsl.MultipartPart{
			{Name: "title", Value: "Report for {{var.customer}}"},
			{Name: "doc", Filename: "logo.png", ContentType: "image/png", Value: "{{file.logo}}", Headers: map[string]string{"Content-Transfer-Encoding": "binary"}},
			{Name: "raw", Filename: "raw.bin", Value: base64.StdEncoding.EncodeToString([]byte{1, 2, 3}), ValueEncoding: "base64"},
		}},
	}
	second := upload
	second.Name = "upload again"
	script := &dsl.Script{
		Name: "multipart", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL},
		DataSources: []dsl.DataSource{{Name: "logo", Type: "file", FileName: "logo.png", InlineData: base64.StdEncoding.EncodeToString(fileBytes), Encoding: "base64"}},
		Actions:     []dsl.Action{upload, second},
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("validate: %v", issues)
	}
	report, err := ReplayOnce(context.Background(), script, map[string]string{"customer": "Acme"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report == nil {
		t.Fatal("no replay report")
	}
	recorded := server.Recorded()
	if len(recorded) != 2 {
		t.Fatalf("requests = %d", len(recorded))
	}
	boundaries := map[string]bool{}
	for _, request := range recorded {
		mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" || params["boundary"] == "recorded" {
			t.Fatalf("Content-Type = %q", request.Header.Get("Content-Type"))
		}
		boundaries[params["boundary"]] = true
		reader := multipart.NewReader(bytes.NewReader(request.Body), params["boundary"])
		type got struct{ name, file, ctype, cte, body string }
		var parts []got
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(part)
			parts = append(parts, got{part.FormName(), part.FileName(), part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), string(data)})
		}
		want := []got{
			{"title", "", "", "", "Report for Acme"},
			{"doc", "logo.png", "image/png", "binary", string(fileBytes)},
			{"raw", "raw.bin", "application/octet-stream", "", string([]byte{1, 2, 3})},
		}
		if len(parts) != len(want) {
			t.Fatalf("parts = %+v", parts)
		}
		for index := range want {
			if parts[index] != want[index] {
				t.Fatalf("part %d = %+v, want %+v", index, parts[index], want[index])
			}
		}
	}
	if len(boundaries) != 2 {
		t.Fatalf("each request needs its own boundary: %v", boundaries)
	}
}

func TestBodyMultipartValidation(t *testing.T) {
	base := func(mutate func(*dsl.Action)) []string {
		action := dsl.Action{Name: "upload", Type: "request", Method: "POST", Target: "/upload", BodyMultipart: &dsl.MultipartBody{Parts: []dsl.MultipartPart{{Name: "a", Value: "b"}}}}
		mutate(&action)
		return dsl.ValidateScriptIssues(&dsl.Script{Name: "s", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://x.test"}, Actions: []dsl.Action{action}})
	}
	if issues := base(func(*dsl.Action) {}); len(issues) != 0 {
		t.Fatalf("valid body_multipart: %v", issues)
	}
	for name, mutate := range map[string]func(*dsl.Action){
		"with body":    func(a *dsl.Action) { a.Body = "x" },
		"no parts":     func(a *dsl.Action) { a.BodyMultipart.Parts = nil },
		"no name":      func(a *dsl.Action) { a.BodyMultipart.Parts[0].Name = " " },
		"bad encoding": func(a *dsl.Action) { a.BodyMultipart.Parts[0].ValueEncoding = "hex" },
		"bad base64": func(a *dsl.Action) {
			a.BodyMultipart.Parts[0].ValueEncoding = "base64"
			a.BodyMultipart.Parts[0].Value = "!!"
		},
		"template base64": func(a *dsl.Action) {
			a.BodyMultipart.Parts[0].ValueEncoding = "base64"
			a.BodyMultipart.Parts[0].Value = "{{var.x}}"
		},
		"disposition":  func(a *dsl.Action) { a.BodyMultipart.Parts[0].Headers = map[string]string{"Content-Disposition": "x"} },
		"unknown file": func(a *dsl.Action) { a.BodyMultipart.Parts[0].Value = "{{file.missing}}" },
	} {
		if issues := base(mutate); len(issues) == 0 {
			t.Errorf("%s: want a validation issue", name)
		}
	}
}

// A windows-1252 part sends its expanded text, parameter values included,
// as code-page bytes, as a LoadRunner code-page script does.
func TestBodyMultipartWindows1252Part(t *testing.T) {
	server := NewServer([]Response{{Method: "POST", Path: "/upload", Status: 200, Body: "ok"}})
	defer server.Close()
	script := &dsl.Script{
		Name: "multipart", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL},
		Actions: []dsl.Action{{
			Name: "upload", Type: "request", Method: "POST", Target: "/upload",
			BodyMultipart: &dsl.MultipartBody{Parts: []dsl.MultipartPart{
				{Name: "city", Value: "Málaga {{var.who}}", ValueEncoding: "windows-1252"},
			}},
		}},
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("validate: %v", issues)
	}
	if _, err := ReplayOnce(context.Background(), script, map[string]string{"who": "José"}, nil); err != nil {
		t.Fatal(err)
	}
	recorded := server.Recorded()
	if len(recorded) != 1 {
		t.Fatalf("requests = %d", len(recorded))
	}
	_, params, err := mime.ParseMediaType(recorded[0].Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	part, err := multipart.NewReader(bytes.NewReader(recorded[0].Body), params["boundary"]).NextRawPart()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(part)
	if string(data) != "M\xe1laga Jos\xe9" {
		t.Fatalf("part bytes = %q, want Windows-1252", data)
	}
}
