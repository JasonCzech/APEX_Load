package enterprisearchive

import (
	"bytes"
	"encoding/base64"
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
