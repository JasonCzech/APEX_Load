package sample

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// A recorded Content-Length is transport framing that Go regenerates after
// correlation and template expansion. It is dropped only when the captured
// request explains it: the exact bytes, an equivalent form encoding of the
// same postData.params, Chrome's one-byte-per-character storage of a binary
// body, or a body the recorder stored after removing its Content-Encoding.
// Any other mismatch stays in the shared fidelity pipeline, which requires
// explicit review.
func normalizeHARContentLength(ex *exchange, requestData map[string]any, diagnostics *diag.Collector, location string) {
	recorded, ok := harRecordedContentLength(ex.HeaderList)
	if !ok {
		return
	}
	length := int64(len(ex.Body))
	if ex.BodyEncoding == "base64" {
		decoded, err := decodeBase64Text(ex.Body)
		if err != nil {
			return
		}
		length = int64(len(decoded))
	}
	if recorded == length {
		dropHARRequestHeaders(ex, "Content-Length")
		setHARRequestMetadata(ex, "har.request.content_length", length)
		diagnostics.Add(diag.Info, "HAR Content-Length regenerated", location, "recorded Content-Length matched the captured request bytes; Go regenerates framing after template expansion")
		return
	}
	if ex.BodyEncoding != "" {
		return
	}
	if encoding := harEquivalentFormEncoding(requestData, recorded); encoding != "" {
		dropHARRequestHeaders(ex, "Content-Length")
		setHARRequestMetadata(ex, "har.request.content_length", length)
		setHARRequestMetadata(ex, "har.request.recorded_content_length", recorded)
		setHARRequestMetadata(ex, "har.request.recorded_form_encoding", encoding)
		diagnostics.Add(diag.Info, "HAR Content-Length regenerated", location, fmt.Sprintf("recorded Content-Length %d matches the %s encoding of the same postData.params; Go re-encodes them as standard form data (%d bytes), which servers decode to identical fields", recorded, encoding, length))
		return
	}
	if raw, ok := harByteText(ex.Body, recorded); ok {
		if utf8.Valid(raw) {
			ex.Body = string(raw)
		} else {
			ex.Body = base64.StdEncoding.EncodeToString(raw)
			ex.BodyEncoding = "base64"
		}
		dropHARRequestHeaders(ex, "Content-Length")
		setHARRequestMetadata(ex, "har.request.content_length", recorded)
		setHARRequestMetadata(ex, "har.request.body_storage", "latin1")
		diagnostics.Add(diag.Info, "HAR request body", location, fmt.Sprintf("request body was stored one byte per character, as Chrome exports binary postData; replay sends the exact %d recorded bytes", recorded))
		return
	}
	if codings, ok := harDecodedContentEncoding(ex); ok {
		dropHARRequestHeaders(ex, "Content-Length")
		setHARRequestMetadata(ex, "har.request.content_length", length)
		setHARRequestMetadata(ex, "har.request.recorded_content_length", recorded)
		replayHARDecodedRequestBody(ex, codings, diagnostics, location, fmt.Sprintf("Content-Encoding: %s; recorded Content-Length %d, captured %d bytes", codings, recorded, length))
		return
	}
	if strings.ContainsRune(ex.Body, utf8.RuneError) {
		diagnostics.Add(diag.Warning, "HAR request body", location, fmt.Sprintf("request body text contains Unicode replacement characters and has %d bytes against a recorded Content-Length of %d: binary bytes were lost when the HAR was written, so this request cannot be replayed as recorded. Re-capture with binary bodies preserved, or remove the step", length, recorded))
	}
}

// harRecordedContentLength returns the request's recorded Content-Length when
// every occurrence is a well-formed decimal and all occurrences agree.
func harRecordedContentLength(headers dsl.HeaderList) (int64, bool) {
	var recorded int64
	found := false
	for _, header := range headers {
		if !strings.EqualFold(header.Name, "Content-Length") {
			continue
		}
		value := strings.TrimSpace(header.Value)
		if value == "" {
			return 0, false
		}
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				return 0, false
			}
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || (found && parsed != recorded) {
			return 0, false
		}
		recorded = parsed
		found = true
	}
	return recorded, found
}

// harFormEncoding describes a serializer that clients use for
// application/x-www-form-urlencoded bodies. They differ only in which ASCII
// punctuation they leave unescaped and how they spell a space; form decoding
// maps every variant back to the same fields.
type harFormEncoding struct {
	name        string
	unescaped   string
	spaceAsPlus bool
}

// Go's url.QueryEscape, which harParamsBody uses, is checked first by the
// exact-length comparison and is not repeated here.
var harEquivalentFormEncodings = []harFormEncoding{
	{name: "WHATWG form", unescaped: "*-._", spaceAsPlus: true},
	{name: "encodeURIComponent", unescaped: "-_.!~*'()"},
	{name: "RFC 3986 query component", unescaped: "!$'()*,-./:;?@_~"},
}

// harEquivalentFormEncoding names the form serializer whose encoding of the
// entry's postData.params has exactly the recorded length. It applies only
// when the body was rebuilt from params, because only then were the recorded
// bytes never captured.
func harEquivalentFormEncoding(requestData map[string]any, recorded int64) string {
	postData, ok := requestData["postData"].(map[string]any)
	if !ok || bodyValue(postData["text"]) != "" {
		return ""
	}
	if strings.Contains(strings.ToLower(stringValue(postData["mimeType"])), "multipart/form-data") {
		return ""
	}
	params, ok := postData["params"].([]any)
	if !ok || len(params) == 0 {
		return ""
	}
	fields := make([][2]string, 0, len(params))
	for _, item := range params {
		param, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := stringValue(param["name"])
		if name == "" {
			continue
		}
		if stringValue(param["fileName"]) != "" {
			return ""
		}
		fields = append(fields, [2]string{name, stringValue(param["value"])})
	}
	if len(fields) == 0 {
		return ""
	}
	for _, encoding := range harEquivalentFormEncodings {
		if encoding.length(fields) == recorded {
			return encoding.name
		}
	}
	return ""
}

func (encoding harFormEncoding) length(fields [][2]string) int64 {
	var total int64
	for index, field := range fields {
		if index > 0 {
			total++
		}
		total += encoding.escapedLength(field[0]) + 1 + encoding.escapedLength(field[1])
	}
	return total
}

func (encoding harFormEncoding) escapedLength(value string) int64 {
	var total int64
	for index := 0; index < len(value); index++ {
		c := value[index]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
			total++
		case c == ' ' && encoding.spaceAsPlus:
			total++
		case strings.IndexByte(encoding.unescaped, c) >= 0:
			total++
		default:
			total += 3
		}
	}
	return total
}

// harByteText recovers a binary body that the HAR stores one byte per
// character (code points U+0000..U+00FF). It requires the character count to
// equal the recorded Content-Length, which the UTF-8 spelling cannot match
// once any byte is 0x80 or above.
func harByteText(body string, recorded int64) ([]byte, bool) {
	if body == "" || int64(utf8.RuneCountInString(body)) != recorded {
		return nil, false
	}
	raw := make([]byte, 0, recorded)
	for _, r := range body {
		if r > 0xFF || r == utf8.RuneError {
			return nil, false
		}
		raw = append(raw, byte(r))
	}
	return raw, true
}

// harDecodedContentEncoding reports a compressed request whose HAR body is the
// decoded text: the request declares only known compression codings while the
// stored body is clean text (whose length differs from the recorded framing,
// when there is one).
func harDecodedContentEncoding(ex *exchange) (string, bool) {
	codings, ok := harRequestContentCodings(ex.HeaderList)
	if !ok || codings == "" || ex.BodyEncoding != "" || !harLooksLikeText(ex.Body) {
		return "", false
	}
	return codings, true
}

// harRequestContentCodings lists a request's compression codings, or reports
// false when one of them is not a known compression.
func harRequestContentCodings(headers dsl.HeaderList) (string, bool) {
	var codings []string
	for _, header := range headers {
		if !strings.EqualFold(header.Name, "Content-Encoding") {
			continue
		}
		for _, token := range strings.Split(header.Value, ",") {
			coding := strings.ToLower(strings.TrimSpace(token))
			switch coding {
			case "", "identity":
			case "gzip", "x-gzip", "deflate", "br", "zstd":
				codings = append(codings, coding)
			default:
				return "", false
			}
		}
	}
	return strings.Join(codings, ", "), true
}

func harLooksLikeText(body string) bool {
	if body == "" {
		return false
	}
	for _, r := range body {
		if r == utf8.RuneError || r == 0x7f || (r < 0x20 && r != '\t' && r != '\n' && r != '\r') {
			return false
		}
	}
	return true
}

func dropHARRequestHeaders(ex *exchange, names ...string) {
	matches := func(name string) bool {
		for _, candidate := range names {
			if strings.EqualFold(name, candidate) {
				return true
			}
		}
		return false
	}
	kept := make(dsl.HeaderList, 0, len(ex.HeaderList))
	for _, header := range ex.HeaderList {
		if !matches(header.Name) {
			kept = append(kept, header)
		}
	}
	ex.HeaderList = kept
	for name := range ex.Headers {
		if matches(name) {
			delete(ex.Headers, name)
		}
	}
}

func setHARRequestMetadata(ex *exchange, key string, value any) {
	if ex.Metadata == nil {
		ex.Metadata = map[string]any{}
	}
	ex.Metadata[key] = value
}
