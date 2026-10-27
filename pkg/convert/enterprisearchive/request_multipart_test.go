package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/bodyencoding"
)

func convertMultipartSource(t *testing.T, items string, files map[string]string, encoding string) dsl.Action {
	t.Helper()
	parsed, err := ParseActionFile("Action.c", `Action() {
		web_submit_data("upload", "Action=https://x.test/upload", "Method=POST", "EncType=multipart/form-data", ITEMDATA,
			`+items+`, LAST);
	}`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{archiveFiles: files, sourceEncoding: encoding})
	if err != nil {
		t.Fatal(err)
	}
	return result.Script.Actions[0]
}

type wirePart struct{ name, filename, contentType, transfer, body string }

// multipartWire returns the parts the action sends, decoding its body
// representation as the runtime would (with no parameters to resolve).
func multipartWire(t *testing.T, action dsl.Action) []wirePart {
	t.Helper()
	if body := action.BodyMultipart; body != nil {
		if action.Body != "" || action.BodyEncoding != "" {
			t.Fatalf("body_multipart with body %q (%s)", action.Body, action.BodyEncoding)
		}
		var parts []wirePart
		for _, part := range body.Parts {
			value := part.Value
			switch part.ValueEncoding {
			case "base64":
				decoded, err := base64.StdEncoding.DecodeString(part.Value)
				if err != nil {
					t.Fatal(err)
				}
				value = string(decoded)
			case "windows-1252":
				encoded, err := charmap.Windows1252.NewEncoder().String(part.Value)
				if err != nil {
					t.Fatal(err)
				}
				value = encoded
			}
			contentType := part.ContentType
			if contentType == "" && part.Filename != "" {
				contentType = "application/octet-stream"
			}
			parts = append(parts, wirePart{part.Name, part.Filename, contentType, part.Headers["Content-Transfer-Encoding"], value})
		}
		return parts
	}
	var wire []byte
	switch action.BodyEncoding {
	case "":
		wire = []byte(action.Body)
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(action.Body)
		if err != nil {
			t.Fatal(err)
		}
		wire = decoded
	case bodyencoding.CEscaped:
		decoded, err := bodyencoding.DecodeCEscapes(action.Body)
		if err != nil {
			t.Fatal(err)
		}
		wire = decoded
	default:
		t.Fatalf("unexpected body_encoding %q", action.BodyEncoding)
	}
	_, params, err := mime.ParseMediaType(action.Headers["Content-Type"])
	if err != nil || params["boundary"] == "" {
		t.Fatalf("Content-Type = %q", action.Headers["Content-Type"])
	}
	reader := multipart.NewReader(bytes.NewReader(wire), params["boundary"])
	var parts []wirePart
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			return parts
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(part)
		parts = append(parts, wirePart{part.FormName(), part.FileName(), part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), string(data)})
	}
}

func TestMultipartItemDataIsSerialized(t *testing.T) {
	files := map[string]string{"report.txt": "numbers\n", "img/logo.bin": "\x00\xffPNG"}
	t.Run("text fields and a text file stay readable", func(t *testing.T) {
		action := convertMultipartSource(t, `"Name=note", "Value=hi", ENDITEM,
			"Name=file", "Value=report.txt", "File=Yes", "ContentType=text/plain", "ContentTransferEncoding=binary", ENDITEM`, files, "")
		if action.BodyMultipart == nil || action.Metadata["source_itemdata_encoding"] != "multipart" {
			t.Fatalf("body_multipart = %v, metadata = %v", action.BodyMultipart, action.Metadata["source_itemdata_encoding"])
		}
		parts := multipartWire(t, action)
		want := []wirePart{{"note", "", "", "", "hi"}, {"file", "report.txt", "text/plain", "binary", "numbers\n"}}
		if len(parts) != 2 || parts[0] != want[0] || parts[1] != want[1] {
			t.Fatalf("parts = %#v, want %#v", parts, want)
		}
	})
	t.Run("binary file without parameters is a base64 part with the default content type", func(t *testing.T) {
		action := convertMultipartSource(t, `"Name=logo", "Value=img\\logo.bin", "File=Yes", ENDITEM`, files, "")
		if action.BodyMultipart == nil || action.BodyMultipart.Parts[0].ValueEncoding != "base64" {
			t.Fatalf("body_multipart = %+v", action.BodyMultipart)
		}
		parts := multipartWire(t, action)
		if len(parts) != 1 || parts[0] != (wirePart{"logo", "logo.bin", "application/octet-stream", "", "\x00\xffPNG"}) {
			t.Fatalf("parts = %#v", parts)
		}
	})
	t.Run("a parameter and a binary file are separate parts", func(t *testing.T) {
		action := convertMultipartSource(t, `"Name=who", "Value={User}", ENDITEM, "Name=logo", "Value=renamed.bin", "FilePath=img/logo.bin", "File=Yes", ENDITEM`, files, "")
		if action.BodyMultipart == nil || action.BodyMultipart.Parts[0].Value != "{{var.user}}" {
			t.Fatalf("body_multipart = %+v", action.BodyMultipart)
		}
		parts := multipartWire(t, action)
		if len(parts) != 2 || parts[0].body != "{{var.user}}" || parts[1] != (wirePart{"logo", "renamed.bin", "application/octet-stream", "", "\x00\xffPNG"}) {
			t.Fatalf("parts = %#v", parts)
		}
	})
	t.Run("code-page scripts send code-page bytes", func(t *testing.T) {
		action := convertMultipartSource(t, `"Name=city", "Value=Málaga", ENDITEM, "Name=who", "Value={User}", ENDITEM, "Name=plain", "Value=x", ENDITEM`, files, "windows-1252")
		parts := multipartWire(t, action)
		if len(parts) != 3 || parts[0].body != "M\xe1laga" {
			t.Fatalf("parts = %#v", parts)
		}
		// Parameter values are code-page bytes too, so a templated text part
		// is encoded after interpolation; plain ASCII needs no encoding.
		got := []string{}
		for _, part := range action.BodyMultipart.Parts {
			got = append(got, part.ValueEncoding)
		}
		if strings.Join(got, ",") != "windows-1252,windows-1252," {
			t.Fatalf("value encodings = %q", got)
		}
		if issues := dsl.ValidateScriptIssues(&dsl.Script{Name: "s", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://x.test"}, Actions: []dsl.Action{action}}); len(issues) != 0 {
			t.Fatalf("issues = %v", issues)
		}
	})
	t.Run("a missing file is its own gate", func(t *testing.T) {
		action := convertMultipartSource(t, `"Name=file", "Value=absent.txt", "File=Yes", ENDITEM`, files, "")
		blockers, _ := action.Metadata["execution_blockers"].([]string)
		if action.Body != "" || len(blockers) != 1 || blockers[0] != requestBodyFileMissingReasonCode {
			t.Fatalf("body = %q, blockers = %v", action.Body, action.Metadata["execution_blockers"])
		}
		if _, ok := action.Headers["Content-Type"]; ok {
			t.Fatalf("a gated multipart step must not advertise its Content-Type: %v", action.Headers)
		}
	})
	t.Run("FilePath without File=Yes keeps the ITEMDATA gate", func(t *testing.T) {
		action := convertMultipartSource(t, `"Name=file", "Value=x", "FilePath=report.txt", ENDITEM`, files, "")
		review, _ := action.Metadata["import_review"].(map[string]any)
		if action.Body != "" || !strings.Contains(strings.Join(review["reason_codes"].([]string), ","), unsupportedItemDataEncodingReason) {
			t.Fatalf("body = %q, review = %v", action.Body, review)
		}
	})
}

func TestSourceSequenceChildClonesAttributionAndMultipartBody(t *testing.T) {
	original := dsl.Action{
		Name:           "upload",
		Type:           "POST",
		Method:         "POST",
		Target:         "/upload",
		ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1200},
		ThinkTimeAfter: true,
		Attribution: &dsl.AnalyticsAttribution{
			HierarchyPath: "Orders/Upload",
			PageName:      "UploadReceipt",
		},
		BodyMultipart: &dsl.MultipartBody{
			Parts: []dsl.MultipartPart{{
				Name:        "doc",
				Value:       "{{file.upload_doc}}",
				Filename:    "receipt.txt",
				ContentType: "text/plain",
				Headers:     map[string]string{"Content-Transfer-Encoding": "binary"},
			}},
		},
	}

	child := sourceSequenceChild(original)

	// Mutating the source Action after projection must not mutate the sequence child.
	original.Attribution.HierarchyPath = "Mutated"
	original.BodyMultipart.Parts[0].Name = "mutated"
	original.BodyMultipart.Parts[0].Headers["Content-Transfer-Encoding"] = "7bit"

	if got, ok := child["think_time_after"].(bool); !ok || !got {
		t.Fatalf("think_time_after = %#v, want true", child["think_time_after"])
	}
	attr, ok := child["attribution"].(*dsl.AnalyticsAttribution)
	if !ok || attr == nil || attr.HierarchyPath != "Orders/Upload" || attr.PageName != "UploadReceipt" {
		t.Fatalf("attribution = %#v, want deep-cloned Orders/Upload UploadReceipt", child["attribution"])
	}
	mp, ok := child["body_multipart"].(*dsl.MultipartBody)
	if !ok || mp == nil || len(mp.Parts) != 1 || mp.Parts[0].Name != "doc" || mp.Parts[0].Headers["Content-Transfer-Encoding"] != "binary" {
		t.Fatalf("body_multipart = %#v, want deep-cloned multipart body", child["body_multipart"])
	}
}

func TestMultipartPreservedInHTTPSequenceAndConcurrentGroups(t *testing.T) {
	files := map[string]string{"report.txt": "numbers\n"}

	t.Run("multi-request transaction folds multipart upload into http_sequence and validates", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		addFile := func(name, content string) {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatalf("Create(%q): %v", name, err)
			}
			if _, err := w.Write([]byte(content)); err != nil {
				t.Fatalf("Write(%q): %v", name, err)
			}
		}
		addFile("UploadFlow.usr", "[General]\nType=Web\n[Actions]\nvuser_init=vuser_init.c\nAction=Action.c\nvuser_end=vuser_end.c\n")
		addFile("vuser_init.c", "vuser_init() { return 0; }\n")
		addFile("vuser_end.c", "vuser_end() { return 0; }\n")
		addFile("report.txt", "numbers\n")
		addFile("UploadFlow.prm", "[parameter:Account]\nType=\"Table\"\nTable=\"account.dat\"\nColumnName=\"account\"\n")
		addFile("account.dat", "account\nacme\n")
		addFile("Action.c", `Action() {
			lr_start_transaction("Upload_Order");
			web_submit_data("upload",
				"Action=https://x.test/upload",
				"Method=POST",
				"EncType=multipart/form-data",
				ITEMDATA,
				"Name=account", "Value={Account}", ENDITEM,
				"Name=file", "Value=report.txt", "File=Yes", "ContentType=text/plain", "ContentTransferEncoding=binary", ENDITEM,
				LAST);
			lr_think_time(2);
			web_url("confirm",
				"URL=https://x.test/confirm",
				"Resource=0",
				LAST);
			lr_end_transaction("Upload_Order", LR_AUTO);
			return 0;
		}`)
		if err := zw.Close(); err != nil {
			t.Fatalf("zw.Close: %v", err)
		}

		result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
		if err != nil {
			t.Fatalf("ConvertZip: %v", err)
		}
		if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
			t.Fatalf("ValidateScriptIssues(in-memory) = %v, want none", issues)
		}
		if len(result.Script.Actions) != 3 || result.Script.Actions[1].Type != "http_sequence" {
			t.Fatalf("Actions = %#v, want start marker, http_sequence, and end marker", result.Script.Actions)
		}
		encoded, err := json.Marshal(result.Script)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		var roundTripped dsl.Script
		if err := json.Unmarshal(encoded, &roundTripped); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if issues := dsl.ValidateScriptIssues(&roundTripped); len(issues) != 0 {
			t.Fatalf("ValidateScriptIssues(round-trip) = %v, want none", issues)
		}
		children := decodeHTTPSequenceChildrenForTest(t, roundTripped.Actions[1])
		if len(children) != 2 {
			t.Fatalf("children = %#v, want 2 children", children)
		}
		first := children[0]
		if !first.ThinkTimeAfter || first.ThinkTime.MeanMs != 2000 {
			t.Fatalf("first child think_time = (%+v, after=%t), want 2000ms after=true", first.ThinkTime, first.ThinkTimeAfter)
		}
		if first.BodyMultipart == nil || len(first.BodyMultipart.Parts) != 2 {
			t.Fatalf("first child body_multipart = %#v, want 2 parts", first.BodyMultipart)
		}
		if first.BodyMultipart.Parts[0].Value != "{{param.account}}" {
			t.Fatalf("part[0].Value = %q, want rewritten {{param.account}}", first.BodyMultipart.Parts[0].Value)
		}
		if first.BodyMultipart.Parts[1].Value != "{{file.upload_file}}" || first.BodyMultipart.Parts[1].Filename != "report.txt" {
			t.Fatalf("part[1] = %+v, want hoisted {{file.upload_file}} with filename report.txt", first.BodyMultipart.Parts[1])
		}
	})

	t.Run("concurrent group and nested control flow preserve child body_multipart", func(t *testing.T) {
		parsed, err := ParseActionFile("Action.c", `Action() {
			web_concurrent_start(NULL);
			web_submit_data("upload_concurrent",
				"Action=https://x.test/upload",
				"Method=POST",
				"EncType=multipart/form-data",
				ITEMDATA,
				"Name=note", "Value=concurrent", ENDITEM,
				"Name=file", "Value=report.txt", "File=Yes", "ContentType=text/plain", ENDITEM,
				LAST);
			web_url("status_concurrent",
				"URL=https://x.test/status",
				"Resource=0",
				LAST);
			web_concurrent_end(NULL);

			if (atoi(lr_eval_string("{Flag}")) == 1) {
				lr_start_transaction("Branch_Upload");
				web_submit_data("upload_branch",
					"Action=https://x.test/branch-upload",
					"Method=POST",
					"EncType=multipart/form-data",
					ITEMDATA,
					"Name=note", "Value=branch", ENDITEM,
					LAST);
				web_url("confirm_branch",
					"URL=https://x.test/branch-confirm",
					"Resource=0",
					LAST);
				lr_end_transaction("Branch_Upload", LR_AUTO);
			}
			return 0;
		}`)
		if err != nil {
			t.Fatalf("ParseActionFile: %v", err)
		}
		result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{archiveFiles: files})
		if err != nil {
			t.Fatalf("Convert: %v", err)
		}
		if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
			t.Fatalf("ValidateScriptIssues = %v, want none", issues)
		}
		if len(result.Script.Actions) != 2 {
			t.Fatalf("Actions = %#v, want concurrent http_sequence and control_if", result.Script.Actions)
		}
		concurrentGroup := result.Script.Actions[0]
		if concurrentGroup.Type != "http_sequence" || concurrentGroup.Params["execution_mode"] != dsl.HTTPSequenceConcurrent {
			t.Fatalf("concurrentGroup = %#v, want concurrent http_sequence", concurrentGroup)
		}
		concurrentChildren := decodeHTTPSequenceChildrenForTest(t, concurrentGroup)
		if len(concurrentChildren) != 2 || concurrentChildren[0].BodyMultipart == nil {
			t.Fatalf("concurrentChildren = %#v, want 2 children with BodyMultipart on first", concurrentChildren)
		}
		parts := multipartWire(t, concurrentChildren[0])
		want := []wirePart{{"note", "", "", "", "concurrent"}, {"file", "report.txt", "text/plain", "", "numbers\n"}}
		if len(parts) != 2 || parts[0] != want[0] || parts[1] != want[1] {
			t.Fatalf("concurrent multipart wire = %#v, want %#v", parts, want)
		}

		branchIf := result.Script.Actions[1]
		if branchIf.Type != "control_if" || branchIf.Control == nil || len(branchIf.Control.Steps) != 3 {
			t.Fatalf("branchIf = %#v, want control_if with start marker, http_sequence, and end marker", branchIf)
		}
		branchSeq := branchIf.Control.Steps[1]
		if branchSeq.Type != "http_sequence" {
			t.Fatalf("branchSeq = %#v, want http_sequence", branchSeq)
		}
		branchChildren := decodeHTTPSequenceChildrenForTest(t, branchSeq)
		if len(branchChildren) != 2 || branchChildren[0].BodyMultipart == nil {
			t.Fatalf("branchChildren = %#v, want 2 children with BodyMultipart on first", branchChildren)
		}
	})
}

func decodeHTTPSequenceChildrenForTest(t *testing.T, seq dsl.Action) []dsl.Action {
	t.Helper()
	rawRequests, err := json.Marshal(seq.Params["requests"])
	if err != nil {
		t.Fatalf("json.Marshal(requests): %v", err)
	}
	var children []dsl.Action
	if err := json.Unmarshal(rawRequests, &children); err != nil {
		t.Fatalf("json.Unmarshal(requests): %v", err)
	}
	return children
}
