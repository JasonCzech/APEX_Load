package enterprisearchive

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

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
		if action.BodyEncoding != "" || action.Metadata["source_itemdata_encoding"] != "multipart" {
			t.Fatalf("body_encoding = %q, metadata = %v", action.BodyEncoding, action.Metadata["source_itemdata_encoding"])
		}
		parts := multipartWire(t, action)
		want := []wirePart{{"note", "", "", "", "hi"}, {"file", "report.txt", "text/plain", "binary", "numbers\n"}}
		if len(parts) != 2 || parts[0] != want[0] || parts[1] != want[1] {
			t.Fatalf("parts = %#v, want %#v", parts, want)
		}
	})
	t.Run("binary file without parameters is base64 with the default content type", func(t *testing.T) {
		action := convertMultipartSource(t, `"Name=logo", "Value=img\\logo.bin", "File=Yes", ENDITEM`, files, "")
		if action.BodyEncoding != "base64" {
			t.Fatalf("body_encoding = %q", action.BodyEncoding)
		}
		parts := multipartWire(t, action)
		if len(parts) != 1 || parts[0] != (wirePart{"logo", "logo.bin", "application/octet-stream", "", "\x00\xffPNG"}) {
			t.Fatalf("parts = %#v", parts)
		}
	})
	t.Run("binary file with a parameter stays c_escaped", func(t *testing.T) {
		action := convertMultipartSource(t, `"Name=who", "Value={User}", ENDITEM, "Name=logo", "Value=renamed.bin", "FilePath=img/logo.bin", "File=Yes", ENDITEM`, files, "")
		if action.BodyEncoding != bodyencoding.CEscaped || !strings.Contains(action.Body, "{{var.user}}") {
			t.Fatalf("body = %q (%s)", action.Body, action.BodyEncoding)
		}
		parts := multipartWire(t, action)
		if len(parts) != 2 || parts[0].body != "{{var.user}}" || parts[1] != (wirePart{"logo", "renamed.bin", "application/octet-stream", "", "\x00\xffPNG"}) {
			t.Fatalf("parts = %#v", parts)
		}
	})
	t.Run("code-page scripts send code-page bytes", func(t *testing.T) {
		action := convertMultipartSource(t, `"Name=city", "Value=Málaga", ENDITEM`, files, "windows-1252")
		parts := multipartWire(t, action)
		if len(parts) != 1 || parts[0].body != "M\xe1laga" {
			t.Fatalf("parts = %#v", parts)
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
