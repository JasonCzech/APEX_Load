package dsl

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// Limits on a multipart body.
const (
	MaxMultipartParts       = 1024
	MaxMultipartPartHeaders = 32
)

// MultipartBody is a multipart/form-data request body: LoadRunner ITEMDATA
// and recorded uploads. Replay builds it from the parts with a fresh
// boundary and sets the request's Content-Type to match.
type MultipartBody struct {
	Parts []MultipartPart `json:"parts"`
}

// MultipartPart is one form field. A text field has a value; a file field
// also has a filename, and its value is usually {{file.NAME}}, the content
// of a file data source, so the file can be swapped without editing the
// script.
type MultipartPart struct {
	Name string `json:"name"`
	// Value is the part's content; template variables are expanded.
	Value string `json:"value,omitempty"`
	// ValueEncoding "base64" marks a literal binary value.
	ValueEncoding string `json:"value_encoding,omitempty"`
	// Filename makes the part a file upload; template variables are expanded.
	Filename string `json:"filename,omitempty"`
	// ContentType is the part's Content-Type. A file part without one is
	// sent as application/octet-stream.
	ContentType string `json:"content_type,omitempty"`
	// Headers are extra part headers, such as Content-Transfer-Encoding.
	Headers map[string]string `json:"headers,omitempty"`
}

// MultipartTemplateStrings lists the template-bearing strings of a
// multipart body: names, text values, filenames, content types and header
// values. A base64 value is literal bytes and is left out.
func (b *MultipartBody) MultipartTemplateStrings() []string {
	if b == nil {
		return nil
	}
	values := make([]string, 0, len(b.Parts)*3)
	for _, part := range b.Parts {
		values = append(values, part.Name, part.Filename, part.ContentType)
		if !isBase64ValueEncoding(part.ValueEncoding) {
			values = append(values, part.Value)
		}
		for name, value := range part.Headers {
			values = append(values, name, value)
		}
	}
	return values
}

// MultipartTextValues returns the editable text values of a multipart body
// (every value that is not base64), for passes that scan or rewrite request
// text the way they do body.
func (b *MultipartBody) MultipartTextValues() []*string {
	if b == nil {
		return nil
	}
	values := make([]*string, 0, len(b.Parts))
	for index := range b.Parts {
		if !isBase64ValueEncoding(b.Parts[index].ValueEncoding) {
			values = append(values, &b.Parts[index].Value)
		}
	}
	return values
}

func isBase64ValueEncoding(encoding string) bool {
	return strings.EqualFold(strings.TrimSpace(encoding), "base64")
}

// validateMultipartBodyIssues checks body_multipart: where it may appear,
// that it replaces the other body fields, and each part.
func validateMultipartBodyIssues(protocol, path string, action Action) []string {
	body := action.BodyMultipart
	if body == nil {
		return nil
	}
	label := actionLabel(path, action)
	var issues []string
	if protocol != "http" || isEngineActionType(action.Type) || strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") || isWebSocketStepType(action.Type) || CanonicalActionType(action.Type) == "http_oauth2_token" {
		issues = append(issues, label+": body_multipart is supported only on HTTP request steps")
	}
	if action.Body != "" || action.BodyFile != "" || action.BodyEncoding != "" {
		issues = append(issues, label+": body_multipart replaces body, body_file and body_encoding")
	}
	if len(body.Parts) == 0 || len(body.Parts) > MaxMultipartParts {
		issues = append(issues, fmt.Sprintf("%s: body_multipart needs 1 to %d parts", label, MaxMultipartParts))
	}
	for index, part := range body.Parts {
		partLabel := fmt.Sprintf("%s: body_multipart.parts[%d]", label, index)
		if strings.TrimSpace(part.Name) == "" {
			issues = append(issues, partLabel+" needs a name")
		}
		switch {
		case part.ValueEncoding == "":
		case isBase64ValueEncoding(part.ValueEncoding):
			if strings.Contains(part.Value, "{{") {
				issues = append(issues, partLabel+": a base64 value is literal and cannot hold templates")
			} else if _, err := base64.StdEncoding.DecodeString(part.Value); err != nil {
				issues = append(issues, fmt.Sprintf("%s: value is not valid base64: %v", partLabel, err))
			}
		default:
			issues = append(issues, partLabel+": value_encoding must be base64 or empty")
		}
		if strings.ContainsAny(part.Name+part.Filename+part.ContentType, "\r\n") {
			issues = append(issues, partLabel+": name, filename and content_type cannot contain line breaks")
		}
		if len(part.Headers) > MaxMultipartPartHeaders {
			issues = append(issues, fmt.Sprintf("%s: at most %d headers", partLabel, MaxMultipartPartHeaders))
		}
		for name, value := range part.Headers {
			lower := strings.ToLower(strings.TrimSpace(name))
			switch {
			case lower == "" || strings.ContainsAny(name, " :\r\n") || strings.ContainsAny(value, "\r\n"):
				issues = append(issues, fmt.Sprintf("%s: header %q is not a valid header", partLabel, name))
			case lower == "content-disposition" || lower == "content-type":
				issues = append(issues, fmt.Sprintf("%s: header %s comes from the part's name, filename and content_type", partLabel, name))
			}
		}
	}
	return issues
}
