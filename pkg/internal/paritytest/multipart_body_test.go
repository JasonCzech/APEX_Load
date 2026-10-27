package paritytest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
	if report == nil || !report.Success {
		t.Fatalf("ReplayOnce report = %+v, want Success", report)
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
	winReport, err := ReplayOnce(context.Background(), script, map[string]string{"who": "José"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if winReport == nil || !winReport.Success {
		t.Fatalf("ReplayOnce report = %+v, want Success", winReport)
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

func TestBodyMultipartInHTTPSequenceReplay(t *testing.T) {
	fileBytes := []byte("report-bytes")
	for _, mode := range []string{"sequential", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			server := NewServer([]Response{
				{Method: "POST", Path: "/upload", Status: 200, Body: "uploaded"},
				{Method: "GET", Path: "/confirm", Status: 200, Body: "confirmed"},
			})
			defer server.Close()

			params := map[string]any{
				"requests": []map[string]any{
					{
						"name":   "upload",
						"type":   "POST",
						"method": "POST",
						"target": "/upload",
						"attribution": &dsl.AnalyticsAttribution{
							HierarchyPath: "Orders/Upload",
							PageName:      "UploadPage",
						},
						"body_multipart": &dsl.MultipartBody{Parts: []dsl.MultipartPart{
							{Name: "account", Value: "{{var.account}}"},
							{Name: "doc", Value: "{{file.upload_doc}}", Filename: "report.txt", ContentType: "text/plain"},
						}},
					},
					{
						"name":   "confirm",
						"type":   "GET",
						"method": "GET",
						"target": "/confirm",
					},
				},
			}
			if mode == "concurrent" {
				params["execution_mode"] = dsl.HTTPSequenceConcurrent
			} else {
				params["requests"].([]map[string]any)[0]["think_time_after"] = true
			}

			script := &dsl.Script{
				Name:     "seq-multipart-replay",
				Protocol: "http",
				Config:   dsl.ScriptConfig{BaseURL: server.URL},
				DataSources: []dsl.DataSource{{
					Name:       "upload_doc",
					Type:       "file",
					FileName:   "report.txt",
					InlineData: base64.StdEncoding.EncodeToString(fileBytes),
					Encoding:   "base64",
				}},
				Actions: []dsl.Action{{
					Name:   "Upload_Order",
					Type:   "http_sequence",
					Method: "SEQUENCE",
					Target: "/upload",
					Params: params,
				}},
			}
			if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
				t.Fatalf("ValidateScriptIssues: %v", issues)
			}
			rawReqs, err := json.Marshal(script.Actions[0].Params["requests"])
			if err != nil {
				t.Fatalf("json.Marshal(requests): %v", err)
			}
			var seqReqs []dsl.Action
			if err := json.Unmarshal(rawReqs, &seqReqs); err != nil {
				t.Fatalf("json.Unmarshal(requests): %v", err)
			}
			if len(seqReqs) != 2 {
				t.Fatalf("seqReqs = %+v, want 2 requests", seqReqs)
			}
			if seqReqs[0].Attribution == nil || seqReqs[0].Attribution.HierarchyPath != "Orders/Upload" || seqReqs[0].Attribution.PageName != "UploadPage" {
				t.Fatalf("seqReqs[0].Attribution = %+v, want HierarchyPath=Orders/Upload PageName=UploadPage", seqReqs[0].Attribution)
			}
			report, err := ReplayOnce(context.Background(), script, map[string]string{"account": "Acme"}, nil)
			if err != nil {
				t.Fatalf("ReplayOnce: %v", err)
			}
			if report == nil || !report.Success {
				t.Fatalf("ReplayOnce report = %+v, want Success", report)
			}
			recorded := server.Recorded()
			if len(recorded) != 2 {
				t.Fatalf("recorded requests = %d, want 2", len(recorded))
			}
			uploadIndex := -1
			for index := range recorded {
				if recorded[index].Method == "POST" && recorded[index].Path == "/upload" {
					uploadIndex = index
					break
				}
			}
			if uploadIndex < 0 {
				t.Fatalf("missing POST /upload in recorded requests: %+v", recorded)
			}
			uploadReq := recorded[uploadIndex]
			_, mediaParams, err := mime.ParseMediaType(uploadReq.Header.Get("Content-Type"))
			if err != nil {
				t.Fatalf("ParseMediaType: %v", err)
			}
			reader := multipart.NewReader(bytes.NewReader(uploadReq.Body), mediaParams["boundary"])
			part1, err := reader.NextRawPart()
			if err != nil {
				t.Fatalf("NextRawPart(1): %v", err)
			}
			data1, _ := io.ReadAll(part1)
			if part1.FormName() != "account" || string(data1) != "Acme" {
				t.Fatalf("part1 = (%q, %q), want (account, Acme)", part1.FormName(), string(data1))
			}
			part2, err := reader.NextRawPart()
			if err != nil {
				t.Fatalf("NextRawPart(2): %v", err)
			}
			data2, _ := io.ReadAll(part2)
			if part2.FormName() != "doc" || part2.FileName() != "report.txt" || string(data2) != string(fileBytes) {
				t.Fatalf("part2 = (%q, %q, %q), want (doc, report.txt, %q)", part2.FormName(), part2.FileName(), string(data2), string(fileBytes))
			}
		})
	}
}
