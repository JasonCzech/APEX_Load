package enterprisearchive

import (
	"encoding/base64"
	"fmt"
	"mime"
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/bodyencoding"
)

// defaultMultipartFileContentType is sent for a File=Yes item without
// ContentType=, as Go's and most browsers' form encoders do.
const defaultMultipartFileContentType = "application/octet-stream"

// sourceMultipartItem is one ITEMDATA item of a web_submit_data step.
type sourceMultipartItem struct {
	name, value      string
	file             bool   // File=Yes: Value (or FilePath) names a file to upload
	filePath         string // FilePath=: the file to read; Value is then the sent filename
	contentType      string // ContentType=
	transferEncoding string // ContentTransferEncoding=
}

// sourceItemDataIsMultipart reports whether a step's EncType is
// multipart/form-data.
func sourceItemDataIsMultipart(args map[string]string) bool {
	raw, present := sourceNamedArg(args, "EncType")
	if !present {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	return err == nil && strings.EqualFold(mediaType, "multipart/form-data")
}

// multipartTransferEncodings are the Content-Transfer-Encoding values that
// label the part without transforming its bytes.
var multipartTransferEncodings = map[string]bool{"binary": true, "8bit": true, "7bit": true}

// rebuildSourceMultipart serializes a multipart/form-data web_submit_data
// step. Text items keep their parameters as templates; file items carry the
// archived file's bytes. It returns false, with the reason, when the step
// cannot be serialized exactly, and a finding when a file is missing.
func rebuildSourceMultipart(action *dsl.Action, fn ParsedFunction, opts ConvertOptions) (bool, string, []importreview.Finding) {
	if len(fn.itemDataMultipart) == 0 {
		return false, "", nil
	}
	cp1252 := strings.EqualFold(opts.sourceEncoding, "windows-1252")
	parts := make([]requestimport.MultipartPart, 0, len(fn.itemDataMultipart))
	for index, item := range fn.itemDataMultipart {
		part := requestimport.MultipartPart{Name: convertParamReferences(item.name), ContentType: strings.TrimSpace(item.contentType)}
		if encoding := strings.TrimSpace(item.transferEncoding); encoding != "" {
			if !multipartTransferEncodings[strings.ToLower(encoding)] {
				return false, fmt.Sprintf("item %d uses ContentTransferEncoding=%s, which changes the part's bytes", index+1, encoding), nil
			}
			part.Headers = requestimport.HeaderList{{Name: "Content-Transfer-Encoding", Value: encoding}}
		}
		if !item.file {
			if item.filePath != "" {
				return false, fmt.Sprintf("item %d has FilePath= without File=Yes", index+1), nil
			}
			text := convertParamReferences(item.value)
			if cp1252 {
				if _, err := charmap.Windows1252.NewEncoder().String(text); err != nil {
					return false, fmt.Sprintf("item %d cannot be represented in the script's Windows-1252 code page", index+1), nil
				}
			}
			part.Data = []byte(text)
			parts = append(parts, part)
			continue
		}
		requested, filename := item.value, path.Base(normalizeRuntimeArchivePath(item.value))
		if strings.TrimSpace(item.filePath) != "" {
			requested, filename = item.filePath, item.value
		}
		if strings.ContainsAny(requested, "{}") {
			return false, fmt.Sprintf("item %d uploads a parameterized file path", index+1), nil
		}
		if opts.archiveFiles == nil {
			return false, "the archive's files are not available to this conversion", nil
		}
		name, content, found := findSourceBodyFile(opts.archiveFiles, requested)
		if !found {
			return false, "", []importreview.Finding{{
				Code: requestBodyFileMissingReasonCode, ReasonCode: requestBodyFileMissingReasonCode, Severity: diag.Blocker, Gate: importreview.GateExecution,
				Source: "enterprise_script_archive", SourceRef: fmt.Sprintf("%s:%d", fn.Filename, fn.LineNumber), Resolution: "add_body_file",
				Detail: fmt.Sprintf("%s %q uploads %q, which is not in the archive (or matches more than one file); without it the request would be sent without the file.",
					fn.Name, convertutil.FirstNonEmpty(fn.StepName, fn.Name), requested),
			}}
		}
		if len(content) > maxInlinedBodyFileBytes {
			return false, fmt.Sprintf("the uploaded file %s is %d bytes, over the %d-byte inline limit", name, len(content), maxInlinedBodyFileBytes), nil
		}
		part.FileName = convertParamReferences(filename)
		if part.ContentType == "" {
			part.ContentType = defaultMultipartFileContentType
		}
		part.Data = []byte(content)
		parts = append(parts, part)
	}
	encType, _ := sourceNamedArg(fn.Args, "EncType")
	if structured, ok := requestimport.DSLMultipartBody(parts); ok {
		// Replay builds the body from its parts with a fresh boundary; file
		// parts become file data sources when the script is finished.
		if cp1252 {
			// A code-page script sends its text, parameter values included,
			// as code-page bytes.
			for index, item := range fn.itemDataMultipart {
				part := &structured.Parts[index]
				if !item.file && part.ValueEncoding == "" && (!isASCIIText(part.Value) || strings.Contains(part.Value, "{{")) {
					part.ValueEncoding = "windows-1252"
				}
			}
		}
		action.BodyMultipart = structured
		action.Body, action.BodyEncoding = "", ""
		setSourceMultipartContentType(action, "multipart/form-data")
		if action.Metadata == nil {
			action.Metadata = map[string]any{}
		}
		action.Metadata["source_itemdata_encoding"] = "multipart"
		action.Metadata["source_multipart_parts"] = len(parts)
		return true, "", nil
	}
	if cp1252 {
		for index, item := range fn.itemDataMultipart {
			if !item.file {
				encoded, _ := charmap.Windows1252.NewEncoder().String(string(parts[index].Data))
				parts[index].Data = []byte(encoded)
			}
		}
	}
	payload, contentType, err := requestimport.MaterializeMultipart(strings.TrimSpace(encType), parts, fmt.Sprintf("%s:%d:%s", fn.Filename, fn.LineNumber, fn.StepName))
	if err != nil {
		return false, err.Error(), nil
	}
	body := string(payload)
	switch {
	case isPlainTextBody(body):
		action.Body, action.BodyEncoding = body, ""
	case strings.Contains(body, "{{"):
		escaped, _ := escapeSourceBodyBytes(bodyencoding.EscapeCText(body), false)
		action.Body, action.BodyEncoding = escaped, bodyencoding.CEscaped
	default:
		action.Body, action.BodyEncoding = base64.StdEncoding.EncodeToString(payload), "base64"
	}
	setSourceMultipartContentType(action, contentType)
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	action.Metadata["source_itemdata_encoding"] = "multipart"
	action.Metadata["source_multipart_parts"] = len(parts)
	return true, "", nil
}

// setSourceMultipartContentType replaces a multipart step's Content-Type.
func setSourceMultipartContentType(action *dsl.Action, contentType string) {
	if action.Headers == nil {
		action.Headers = map[string]string{}
	}
	deleteHeaderCaseInsensitive(action.Headers, "Content-Type")
	action.Headers["Content-Type"] = contentType
	if len(action.HeaderList) > 0 {
		action.HeaderList = append(filterHeaderListCaseInsensitive(action.HeaderList, "Content-Type"), dsl.HeaderEntry{Name: "Content-Type", Value: contentType})
	}
}

func isASCIIText(text string) bool {
	for index := 0; index < len(text); index++ {
		if text[index] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
