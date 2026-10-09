package requestimport

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// Multipart bodies import as body_multipart (pkg/dsl): replay builds them
// from their parts with a fresh boundary, text fields stay editable and
// templated, and file fields read file data sources the tester can swap.

// DSLMultipartBody turns multipart parts into a body_multipart. A text part
// keeps its UTF-8 text as a templated value; a file part, or text that is
// not UTF-8, carries its bytes as a base64 value until HoistMultipartFiles
// makes the file a data source. It reports false when the parts cannot be
// represented exactly: a templated value that is not UTF-8, a repeated part
// header, or a part with no name.
func DSLMultipartBody(parts []MultipartPart) (*dsl.MultipartBody, bool) {
	if len(parts) == 0 || len(parts) > dsl.MaxMultipartParts {
		return nil, false
	}
	body := &dsl.MultipartBody{Parts: make([]dsl.MultipartPart, 0, len(parts))}
	for _, part := range parts {
		if strings.TrimSpace(part.Name) == "" || part.FileAssetRef != "" {
			return nil, false
		}
		out := dsl.MultipartPart{Name: part.Name, Filename: part.FileName, ContentType: strings.TrimSpace(part.ContentType)}
		for _, header := range part.Headers {
			if out.Headers == nil {
				out.Headers = map[string]string{}
			}
			if _, repeated := out.Headers[header.Name]; repeated {
				return nil, false
			}
			out.Headers[header.Name] = header.Value
		}
		switch {
		case part.FileName == "" && utf8.Valid(part.Data):
			out.Value = string(part.Data)
		case bytes.Contains(part.Data, []byte("{{")):
			// Templates expand in text only.
			return nil, false
		default:
			out.Value, out.ValueEncoding = base64.StdEncoding.EncodeToString(part.Data), "base64"
		}
		body.Parts = append(body.Parts, out)
	}
	return body, true
}

// ParseMultipartBody parses a recorded multipart/form-data body. It reports
// false when the content type is not multipart/form-data with a boundary or
// a part is not a named form field.
func ParseMultipartBody(contentType string, body []byte) ([]MultipartPart, bool) {
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || params["boundary"] == "" {
		return nil, false
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var parts []MultipartPart
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil || len(parts) >= dsl.MaxMultipartParts {
			return nil, false
		}
		disposition, dispositionParams, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil || !strings.EqualFold(disposition, "form-data") || dispositionParams["name"] == "" {
			return nil, false
		}
		for name := range dispositionParams {
			if name != "name" && name != "filename" {
				// filename* and other parameters would be lost.
				return nil, false
			}
		}
		data, err := io.ReadAll(io.LimitReader(part, int64(len(body))+1))
		if err != nil {
			return nil, false
		}
		out := MultipartPart{Name: dispositionParams["name"], FileName: dispositionParams["filename"], ContentType: part.Header.Get("Content-Type"), Data: data}
		for name, values := range part.Header {
			if strings.EqualFold(name, "Content-Disposition") || strings.EqualFold(name, "Content-Type") {
				continue
			}
			for _, value := range values {
				out.Headers = append(out.Headers, Header{Name: name, Value: value})
			}
		}
		parts = append(parts, out)
	}
	return parts, len(parts) > 0
}

var multipartAssetNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// HoistMultipartFiles moves the bytes of each file part with a literal
// value into a file data source and points the part at it ({{file.NAME}}),
// so the file can be swapped without editing the request. Steps nested in
// control blocks are included.
func HoistMultipartFiles(script *dsl.Script) {
	if script == nil {
		return
	}
	used := map[string]bool{}
	for _, source := range script.DataSources {
		used[strings.ToLower(strings.TrimSpace(source.Name))] = true
	}
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if ref.Action == nil || ref.Action.BodyMultipart == nil {
			return nil
		}
		body := ref.Action.BodyMultipart
		for partIndex := range body.Parts {
			part := &body.Parts[partIndex]
			base64Value := strings.EqualFold(strings.TrimSpace(part.ValueEncoding), "base64")
			literalText := part.ValueEncoding == "" && !strings.Contains(part.Value, "{{")
			if part.Filename == "" || !base64Value && !literalText {
				continue
			}
			content := part.Value
			if literalText {
				content = base64.StdEncoding.EncodeToString([]byte(part.Value))
			}
			name := claimMultipartAssetName(part.Name, used)
			source := dsl.DataSource{Name: name, Type: "file", ContentType: part.ContentType, InlineData: content, Encoding: "base64"}
			if !strings.Contains(part.Filename, "{{") {
				source.FileName = part.Filename
			}
			script.DataSources = append(script.DataSources, source)
			part.Value, part.ValueEncoding = "{{file."+name+"}}", ""
		}
		return nil
	})
}

func claimMultipartAssetName(field string, used map[string]bool) string {
	base := strings.Trim(multipartAssetNameUnsafe.ReplaceAllString(field, "_"), "_")
	if base == "" {
		base = "file"
	}
	base = "upload_" + strings.ToLower(base)
	name := base
	for suffix := 2; used[name]; suffix++ {
		name = fmt.Sprintf("%s_%d", base, suffix)
	}
	used[name] = true
	return name
}
