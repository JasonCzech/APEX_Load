package engine

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"sort"
	"strings"

	"golang.org/x/text/encoding/charmap"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// multipartQuoteEscaper escapes a Content-Disposition parameter the way
// mime/multipart does.
var multipartQuoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

// applyMultipartBody builds a body_multipart request body with a fresh
// boundary: each part's templates are expanded (a {{file.NAME}} value reads
// the file data source), the redacted copy for evidence uses the same
// boundary, and the request's Content-Type is replaced to match.
func (vu *VUser) applyMultipartBody(body *dsl.MultipartBody, resolved *protocol.Action) {
	var wire, redacted bytes.Buffer
	writer := multipart.NewWriter(&wire)
	redactedWriter := multipart.NewWriter(&redacted)
	if err := redactedWriter.SetBoundary(writer.Boundary()); err != nil {
		vu.bodyResolutionError = err
		return
	}
	for index, part := range body.Parts {
		name, redactedName := vu.interpolateWithSecrets(part.Name)
		filename, redactedFilename := vu.interpolateWithSecrets(part.Filename)
		contentType, redactedContentType := vu.interpolateWithSecrets(part.ContentType)
		value, redactedValue := vu.interpolateWithSecrets(part.Value)
		if strings.EqualFold(strings.TrimSpace(part.ValueEncoding), "base64") {
			decoded, err := base64.StdEncoding.DecodeString(part.Value)
			if err != nil {
				vu.bodyResolutionError = fmt.Errorf("body_multipart.parts[%d]: value is not valid base64: %w", index, err)
				return
			}
			value, redactedValue = string(decoded), string(decoded)
		} else if dsl.IsWindows1252ValueEncoding(part.ValueEncoding) {
			encoded, err := charmap.Windows1252.NewEncoder().String(value)
			if err != nil {
				vu.bodyResolutionError = fmt.Errorf("body_multipart.parts[%d]: value is not representable in Windows-1252: %w", index, err)
				return
			}
			// The redacted evidence copy follows the wire bytes when it can.
			if redactedEncoded, err := charmap.Windows1252.NewEncoder().String(redactedValue); err == nil {
				redactedValue = redactedEncoded
			}
			value = encoded
		}
		headers, redactedHeaders := vu.multipartPartHeaders(part.Headers)
		if err := writeMultipartPart(writer, name, filename, part.Filename != "", contentType, headers, value); err != nil {
			vu.bodyResolutionError = fmt.Errorf("body_multipart.parts[%d]: %w", index, err)
			return
		}
		if err := writeMultipartPart(redactedWriter, redactedName, redactedFilename, part.Filename != "", redactedContentType, redactedHeaders, redactedValue); err != nil {
			vu.bodyResolutionError = fmt.Errorf("body_multipart.parts[%d]: %w", index, err)
			return
		}
	}
	if err := writer.Close(); err != nil {
		vu.bodyResolutionError = err
		return
	}
	_ = redactedWriter.Close()
	contentType := writer.FormDataContentType()
	resolved.Body = wire.Bytes()
	resolved.RedactedBody = redacted.Bytes()
	resolved.BodyEncoding = ""
	resolved.Headers, resolved.HeaderList = withMultipartContentType(resolved.Headers, resolved.HeaderList, contentType)
	resolved.RedactedHeaders, resolved.RedactedHeaderList = withMultipartContentType(resolved.RedactedHeaders, resolved.RedactedHeaderList, contentType)
}

func (vu *VUser) multipartPartHeaders(headers map[string]string) (map[string]string, map[string]string) {
	if len(headers) == 0 {
		return nil, nil
	}
	resolved := make(map[string]string, len(headers))
	redacted := make(map[string]string, len(headers))
	for name, value := range headers {
		resolved[name], redacted[name] = vu.interpolateWithSecrets(value)
	}
	return resolved, redacted
}

func writeMultipartPart(writer *multipart.Writer, name, filename string, file bool, contentType string, headers map[string]string, value string) error {
	disposition := `form-data; name="` + multipartQuoteEscaper.Replace(name) + `"`
	if file {
		disposition += `; filename="` + multipartQuoteEscaper.Replace(filename) + `"`
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", disposition)
	if contentType == "" && file {
		contentType = "application/octet-stream"
	}
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		header.Set(name, headers[name])
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = part.Write([]byte(value))
	return err
}

// withMultipartContentType replaces every Content-Type the request carries
// with the multipart one, whose boundary is new on every request.
func withMultipartContentType(headers map[string]string, list protocol.HeaderList, contentType string) (map[string]string, protocol.HeaderList) {
	updated := make(map[string]string, len(headers)+1)
	for name, value := range headers {
		if !strings.EqualFold(name, "Content-Type") {
			updated[name] = value
		}
	}
	updated["Content-Type"] = contentType
	if len(list) == 0 {
		return updated, list
	}
	kept := make(protocol.HeaderList, 0, len(list))
	for _, entry := range list {
		if !strings.EqualFold(entry.Name, "Content-Type") {
			kept = append(kept, entry)
		}
	}
	return updated, kept
}
