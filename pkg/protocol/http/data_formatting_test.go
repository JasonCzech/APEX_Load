package http

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestApplyDataFormattingTransformExpandedTypes(t *testing.T) {
	tests := []struct {
		name      string
		transform dataFormattingTransform
		body      string
		want      string
	}{
		{
			name:      "url encode",
			transform: dataFormattingTransform{Type: "url_encode"},
			body:      "token=a b&role=admin",
			want:      "token%3Da+b%26role%3Dadmin",
		},
		{
			name:      "html escape",
			transform: dataFormattingTransform{Type: "html_escape"},
			body:      `<span title="a&b">x</span>`,
			want:      `&lt;span title=&#34;a&amp;b&#34;&gt;x&lt;/span&gt;`,
		},
		{
			name:      "html unescape",
			transform: dataFormattingTransform{Type: "html_unescape"},
			body:      `&lt;ok&gt;A &amp; B&lt;/ok&gt;`,
			want:      `<ok>A & B</ok>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyDataFormattingTransform([]byte(tt.body), tt.transform)
			if err != nil {
				t.Fatalf("applyDataFormattingTransform returned error: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("transformed body = %q, want %q", string(got), tt.want)
			}
		})
	}
}

func TestApplyDataFormattingTransformXMLToJSON(t *testing.T) {
	got, err := applyDataFormattingTransform([]byte(`<root type="auth"><access_token>abc123</access_token><scope>read</scope><scope>write</scope></root>`), dataFormattingTransform{Type: "xml_to_json"})
	if err != nil {
		t.Fatalf("applyDataFormattingTransform returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v body=%s", err, got)
	}
	root, ok := payload["root"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %#v, want root object", payload)
	}
	if root["access_token"] != "abc123" {
		t.Fatalf("access_token = %#v, want abc123", root["access_token"])
	}
	scopes, ok := root["scope"].([]any)
	if !ok || len(scopes) != 2 || scopes[0] != "read" || scopes[1] != "write" {
		t.Fatalf("scope = %#v, want repeated scope array", root["scope"])
	}
	attrs, ok := root["@attributes"].(map[string]any)
	if !ok || attrs["type"] != "auth" {
		t.Fatalf("@attributes = %#v, want type=auth", root["@attributes"])
	}
}

func TestApplyDataFormattingTransformXMLToJSONRejectsInvalidXML(t *testing.T) {
	if _, err := applyDataFormattingTransform([]byte(`<root><token>abc</root>`), dataFormattingTransform{Type: "xml_to_json"}); err == nil {
		t.Fatal("applyDataFormattingTransform returned nil error for invalid XML")
	}
}

func TestXMLToJSONBoundedRejectsMultipleRootsAndTrailingText(t *testing.T) {
	for _, body := range []string{
		`<first>1</first><second>2</second>`,
		`<root>ok</root>trailing`,
		`leading<root>ok</root>`,
	} {
		if _, err := xmlToJSONBounded([]byte(body), 1<<20); err == nil {
			t.Fatalf("xmlToJSONBounded(%q) returned nil error", body)
		}
	}
	if _, err := xmlToJSONBounded([]byte(" \n<root>ok</root>\t"), 1<<20); err != nil {
		t.Fatalf("whitespace outside root should be accepted: %v", err)
	}
}

func TestJSONToXMLBoundedRejectsExcessiveDepthAndWidth(t *testing.T) {
	deep := strings.Repeat("[", maxJSONXMLDepth+1) + "0" + strings.Repeat("]", maxJSONXMLDepth+1)
	if _, err := jsonToXMLBounded([]byte(deep), 1<<20); err == nil || !strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("deep JSON error = %v", err)
	}

	var flat strings.Builder
	flat.WriteByte('[')
	for i := 0; i < maxJSONXMLNodes; i++ {
		if i > 0 {
			flat.WriteByte(',')
		}
		flat.WriteString("null")
	}
	flat.WriteByte(']')
	if _, err := jsonToXMLBounded([]byte(flat.String()), maxJSONXMLRetainedBytes); err == nil || !strings.Contains(err.Error(), "node limit") {
		t.Fatalf("wide JSON error = %v", err)
	}
}

func TestJSONToXMLBoundedRejectsOversizedScalarWithoutTreeMaterialization(t *testing.T) {
	body := `{"value":"` + strings.Repeat("x", maxJSONXMLScalarBytes+1) + `"}`
	if _, err := jsonToXMLBounded([]byte(body), 2<<20); err == nil || !strings.Contains(err.Error(), "scalar") {
		t.Fatalf("oversized scalar error = %v", err)
	}
}

func TestXMLToJSONBoundedRejectsExcessiveDepth(t *testing.T) {
	body := strings.Repeat("<node>", maxXMLJSONDepth+1) + strings.Repeat("</node>", maxXMLJSONDepth+1)
	if _, err := xmlToJSONBounded([]byte(body), 1<<20); err == nil || !strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("deep XML error = %v", err)
	}
}

func TestXMLToJSONBoundedDerivesNodeLimitFromOutputBudget(t *testing.T) {
	body := "<root>" + strings.Repeat("<item/>", 32) + "</root>"
	if _, err := xmlToJSONBounded([]byte(body), 64); err == nil || !strings.Contains(err.Error(), "node limit") {
		t.Fatalf("high-node XML error = %v", err)
	}
}

func TestXMLToJSONBoundedRejectsOversizedElementText(t *testing.T) {
	body := "<root>" + strings.Repeat("x", maxXMLJSONTextSegmentBytes+1) + "</root>"
	if _, err := xmlToJSONBounded([]byte(body), 2<<20); err == nil || !strings.Contains(err.Error(), "text") {
		t.Fatalf("oversized XML text error = %v", err)
	}
}

func TestXMLToJSONBoundedRejectsExcessiveAttributes(t *testing.T) {
	var body strings.Builder
	body.WriteString("<root")
	for i := 0; i <= maxXMLJSONAttributesPerNode; i++ {
		body.WriteString(" a")
		body.WriteString(strconv.Itoa(i))
		body.WriteString(`="x"`)
	}
	body.WriteString("/>")
	if _, err := xmlToJSONBounded([]byte(body.String()), 0); err == nil || !strings.Contains(err.Error(), "attribute limit") {
		t.Fatalf("high-attribute XML error = %v", err)
	}
}

func TestApplyDataFormattingTransformBoundsExpandingOutputs(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		transform dataFormattingTransform
		limit     int64
	}{
		{name: "url encode", body: "&&&&", transform: dataFormattingTransform{Type: "url_encode"}, limit: 8},
		{name: "html escape", body: "<&&", transform: dataFormattingTransform{Type: "html_escape"}, limit: 8},
		{name: "json to xml", body: `{"value":"<&"}`, transform: dataFormattingTransform{Type: "json_to_xml"}, limit: 16},
		{name: "xml to json", body: `<root>value</root>`, transform: dataFormattingTransform{Type: "xml_to_json"}, limit: 8},
		{name: "gzip encode", body: "small payload", transform: dataFormattingTransform{Type: "gzip", Options: map[string]string{"mode": "encode"}}, limit: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := applyDataFormattingTransformBounded([]byte(tt.body), tt.transform, tt.limit); err == nil {
				t.Fatalf("bounded transform returned %d bytes without an error", len(got))
			}
		})
	}
}

func TestApplyDataFormattingChainAutoDetectsGzipPayloads(t *testing.T) {
	payload := []byte(`{"access_token":"abc123"}`)
	compressed := gzipTestPayload(t, payload)

	tests := []struct {
		name string
		body []byte
	}{
		{name: "raw gzip", body: compressed},
		{name: "base64 gzip", body: []byte(base64.StdEncoding.EncodeToString(compressed))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyDataFormattingChain(tt.body, true, nil, 1<<20)
			if err != nil {
				t.Fatalf("applyDataFormattingChain returned error: %v", err)
			}
			if string(got) != string(payload) {
				t.Fatalf("formatted body = %q, want %q", string(got), string(payload))
			}
		})
	}
}

func TestDataFormattingNoOpDoesNotCloneResponseBody(t *testing.T) {
	body := []byte("ordinary response payload")
	formatted, err := applyDataFormattingChain(body, false, nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(formatted) == 0 || &formatted[0] != &body[0] {
		t.Fatal("disabled data formatting cloned the response body")
	}

	undetected, err := applyDataFormattingChain(body, true, nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(undetected) == 0 || &undetected[0] != &body[0] {
		t.Fatal("auto-detection cloned a body that required no formatting")
	}
}

func gzipTestPayload(t *testing.T, payload []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("gzip write returned error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close returned error: %v", err)
	}
	return buf.Bytes()
}
