package enterprisearchive

import (
	"encoding/base64"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/bodyencoding"
)

// VuGen request steps can send their body through BodyBinary (C escapes for
// raw bytes), BodyUnicode (sent as UTF-16LE) or BodyFilePath (a file shipped
// with the script). They are rebuilt here; any form or mixture that cannot be
// rebuilt exactly keeps the request_body_variant_unsupported gate.
const (
	// requestBodyFileMissingReasonCode gates a BodyFilePath step whose file is
	// not in the archive.
	requestBodyFileMissingReasonCode = "request_body_file_missing"
	// maxInlinedBodyFileBytes bounds a BodyFilePath file inlined into the
	// script; a larger file keeps the body-variant gate.
	maxInlinedBodyFileBytes = 4 << 20
)

type sourceBodySegment struct {
	name  string // Body, BodyBinary, BodyUnicode or BodyFilePath
	value string // the attribute value, with its C literal escapes decoded once
}

var sourceBodySegmentNames = map[string]string{
	"body": "Body", "bodybinary": "BodyBinary", "bodyunicode": "BodyUnicode", "bodyfilepath": "BodyFilePath",
}

// sourceBodySegments returns a request step's body attributes in argument
// order, and whether it uses a RAW_BODY_START block.
func sourceBodySegments(fn ParsedFunction) ([]sourceBodySegment, bool) {
	var segments []sourceBodySegment
	for _, arg := range fn.CallArgs {
		if arg.Kind != "key_value" {
			if sourceArgTokenEquals(arg, "RAW_BODY_START") {
				return segments, true
			}
			if isRequestArgumentListEnd(arg) {
				break
			}
			continue
		}
		if name, ok := sourceBodySegmentNames[strings.ToLower(strings.TrimSpace(arg.Name))]; ok {
			segments = append(segments, sourceBodySegment{name: name, value: arg.Value})
		}
	}
	return segments, false
}

// sourceBodyRebuild is the outcome of rebuilding a step's body variants.
type sourceBodyRebuild struct {
	// rebuilt lists the body attributes now reproduced (or gated by a more
	// specific finding), so the generic body-variant gate skips them.
	rebuilt []string
	// findings are specific gates, such as a missing BodyFilePath file.
	findings []importreview.Finding
	// reason explains why a body variant could not be rebuilt.
	reason string
}

// rebuildSourceBodyVariants rebuilds the body of a request step that uses
// BodyBinary, BodyUnicode or BodyFilePath. files holds the archive's files by
// archive-relative name; sourceEncoding is the script's code page.
func rebuildSourceBodyVariants(action *dsl.Action, fn ParsedFunction, files map[string]string, sourceEncoding string) sourceBodyRebuild {
	segments, raw := sourceBodySegments(fn)
	counts := map[string]int{}
	for _, segment := range segments {
		counts[segment.name]++
	}
	variants := counts["BodyBinary"] + counts["BodyUnicode"] + counts["BodyFilePath"]
	if variants == 0 || raw || action == nil {
		return sourceBodyRebuild{}
	}
	switch {
	case counts["BodyFilePath"] > 0:
		if len(segments) != 1 {
			return sourceBodyRebuild{reason: "BodyFilePath is combined with other body attributes"}
		}
		return rebuildSourceBodyFile(action, fn, files, segments[0].value)
	case counts["BodyUnicode"] > 0:
		if counts["BodyUnicode"] != len(segments) {
			return sourceBodyRebuild{reason: "BodyUnicode is combined with other body attributes"}
		}
		var text strings.Builder
		for _, segment := range segments {
			text.WriteString(segment.value)
		}
		if !utf8.ValidString(text.String()) {
			return sourceBodyRebuild{reason: "BodyUnicode contains bytes that are not text"}
		}
		action.Body = convertParamReferences(text.String())
		action.BodyEncoding = bodyencoding.UTF16LE
		setSourceBodyForm(action, "BodyUnicode")
		return sourceBodyRebuild{rebuilt: []string{"BodyUnicode"}}
	default:
		return rebuildSourceBinaryBody(action, segments, sourceEncoding)
	}
}

// rebuildSourceBinaryBody joins Body and BodyBinary segments into one
// c_escaped text. Without parameters the bytes are fixed and are stored as
// base64; with parameters the body stays c_escaped so the runtime decodes it
// after interpolation, sending parameter values verbatim.
func rebuildSourceBinaryBody(action *dsl.Action, segments []sourceBodySegment, sourceEncoding string) sourceBodyRebuild {
	cp1252 := strings.EqualFold(sourceEncoding, "windows-1252")
	var escaped strings.Builder
	for _, segment := range segments {
		text := segment.value
		if segment.name == "Body" {
			// Body text is literal: its backslashes are data, not escapes.
			text = bodyencoding.EscapeCText(text)
		}
		text, err := escapeSourceBodyBytes(text, cp1252)
		if err != nil {
			return sourceBodyRebuild{reason: fmt.Sprintf("%s text cannot be represented in the script's Windows-1252 code page", segment.name)}
		}
		escaped.WriteString(text)
	}
	if _, err := bodyencoding.DecodeCEscapes(escaped.String()); err != nil {
		return sourceBodyRebuild{reason: fmt.Sprintf("BodyBinary has an escape the importer does not recognize (%v)", err)}
	}
	templated := convertParamReferences(escaped.String())
	if strings.Contains(templated, "{{") {
		action.Body = templated
		action.BodyEncoding = bodyencoding.CEscaped
	} else {
		decoded, _ := bodyencoding.DecodeCEscapes(templated)
		action.Body = base64.StdEncoding.EncodeToString(decoded)
		action.BodyEncoding = "base64"
	}
	setSourceBodyForm(action, "BodyBinary")
	return sourceBodyRebuild{rebuilt: []string{"BodyBinary"}}
}

// escapeSourceBodyBytes spells as a \xNN escape every byte that is not valid
// UTF-8 (a JSON string cannot carry it) and every control character other
// than tab, CR and LF (so the stored body stays readable). In a Windows-1252 script every non-ASCII character
// also becomes the escape of its code-page byte, so the decoded body carries
// code-page bytes as LoadRunner sends them, not UTF-8.
func escapeSourceBodyBytes(text string, cp1252 bool) (string, error) {
	var out strings.Builder
	for index := 0; index < len(text); {
		r, size := utf8.DecodeRuneInString(text[index:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&out, `\x%02X`, text[index])
		case r < 0x20 && r != '\t' && r != '\r' && r != '\n', r == 0x7f:
			fmt.Fprintf(&out, `\x%02X`, byte(r))
		case r < utf8.RuneSelf || !cp1252:
			out.WriteString(text[index : index+size])
		default:
			encoded, err := charmap.Windows1252.NewEncoder().String(string(r))
			if err != nil {
				return "", err
			}
			for offset := 0; offset < len(encoded); offset++ {
				fmt.Fprintf(&out, `\x%02X`, encoded[offset])
			}
		}
		index += size
	}
	return out.String(), nil
}

// rebuildSourceBodyFile inlines the file named by BodyFilePath= from the
// archive. Text stays readable; anything else is stored as base64.
func rebuildSourceBodyFile(action *dsl.Action, fn ParsedFunction, files map[string]string, requested string) sourceBodyRebuild {
	requested = strings.TrimSpace(requested)
	if strings.ContainsAny(requested, "{}") {
		return sourceBodyRebuild{reason: "BodyFilePath names a parameterized path"}
	}
	if files == nil {
		return sourceBodyRebuild{reason: "the archive's files are not available to this conversion"}
	}
	name, content, found := findSourceBodyFile(files, requested)
	if !found {
		action.Body = ""
		action.BodyEncoding = ""
		return sourceBodyRebuild{rebuilt: []string{"BodyFilePath"}, findings: []importreview.Finding{{
			Code: requestBodyFileMissingReasonCode, ReasonCode: requestBodyFileMissingReasonCode, Severity: diag.Blocker, Gate: importreview.GateExecution,
			Source: "enterprise_script_archive", SourceRef: fmt.Sprintf("%s:%d", fn.Filename, fn.LineNumber), Resolution: "add_body_file",
			Detail: fmt.Sprintf("%s %q reads its body from %q, which is not in the archive (or matches more than one file); without it the request would be sent with an empty body.",
				fn.Name, convertutil.FirstNonEmpty(fn.StepName, fn.Name), requested),
		}}}
	}
	if len(content) > maxInlinedBodyFileBytes {
		return sourceBodyRebuild{reason: fmt.Sprintf("the BodyFilePath file %s is %d bytes, over the %d-byte inline limit", name, len(content), maxInlinedBodyFileBytes)}
	}
	if isPlainTextBody(content) {
		action.Body = content
		action.BodyEncoding = ""
	} else {
		action.Body = base64.StdEncoding.EncodeToString([]byte(content))
		action.BodyEncoding = "base64"
	}
	setSourceBodyForm(action, "BodyFilePath")
	action.Metadata["source_body_file"] = name
	return sourceBodyRebuild{rebuilt: []string{"BodyFilePath"}}
}

// findSourceBodyFile resolves a BodyFilePath value against the archive. VuGen
// writes paths relative to the script folder, but recorded scripts often keep
// an absolute path from the recording machine; that falls back to a unique
// base-name match.
func findSourceBodyFile(files map[string]string, requested string) (string, string, bool) {
	if name, content, ambiguous := findSourceArchiveFile(files, requested); name != "" {
		return name, content, true
	} else if len(ambiguous) > 0 {
		return "", "", false
	}
	normalized := normalizeRuntimeArchivePath(requested)
	if !strings.Contains(normalized, ":") && !strings.HasPrefix(normalized, "/") {
		return "", "", false
	}
	if name, content, _ := findSourceArchiveFile(files, path.Base(normalized)); name != "" {
		return name, content, true
	}
	return "", "", false
}

// isPlainTextBody reports whether content is UTF-8 text without control
// characters other than tab, CR and LF, so it can be stored as a plain body.
func isPlainTextBody(content string) bool {
	if !utf8.ValidString(content) {
		return false
	}
	for index := 0; index < len(content); index++ {
		c := content[index]
		if (c < 0x20 && c != '\t' && c != '\r' && c != '\n') || c == 0x7f {
			return false
		}
	}
	return true
}

func setSourceBodyForm(action *dsl.Action, form string) {
	if action.Metadata == nil {
		action.Metadata = make(map[string]any)
	}
	action.Metadata["source_body_form"] = form
}
