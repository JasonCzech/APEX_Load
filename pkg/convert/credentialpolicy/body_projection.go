package credentialpolicy

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/credential"
)

// ProjectRequestBody applies the shared structured credential policy to wire
// bytes without trusting Content-Type as the only signal. Recognizable JSON,
// XML, form, multipart, and GraphQL bodies are projected by their dedicated
// byte-preserving codecs. Unrecognized raw/file bytes remain byte-identical.
func ProjectRequestBody(payload []byte, contentType string, options Options) ([]byte, Result, error) {
	return projectRequestBody(payload, contentType, options, nil)
}

// projectRequestBody is ProjectRequestBody. A JSON body's projection also
// stores in spans the byte spans of payload it replaced, so a caller holding
// the body in another encoding can apply them there; spans stays nil for
// other bodies and when nothing was replaced.
func projectRequestBody(payload []byte, contentType string, options Options, spans *[]replacement) ([]byte, Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, Result{}, err
	}
	if len(payload) > options.Limits.MaxBytes {
		if options.Mode == Preserve {
			return append([]byte(nil), payload...),
				preservedOpaquePayloadResult(
					payload,
					options,
					"import_credential_review_incomplete",
				), nil
		}
		return nil, rejected(options.Source, "credential_body_limit_exceeded"), nil
	}
	mediaType, parameters := requestBodyMediaType(contentType)
	if strings.HasPrefix(mediaType, "multipart/") {
		return ProjectMultipartWire(payload, mediaType, parameters["boundary"], options)
	}

	trimmed := bytes.TrimSpace(payload)
	kind := ""
	switch {
	case mediaType == "application/json", strings.HasSuffix(mediaType, "+json"):
		kind = "json"
	case mediaType == "application/xml", mediaType == "text/xml", strings.HasSuffix(mediaType, "+xml"):
		kind = "xml"
	case mediaType == "application/x-www-form-urlencoded":
		kind = "form"
	case mediaType == "application/graphql":
		kind = "graphql"
	}
	// A declared structured media type is authoritative even when malformed.
	// Only content without a recognized structured declaration may be inferred
	// as another wire format.
	if kind == "" {
		if inferredType, boundary, ok := inferMultipartWireType(payload); ok {
			return ProjectMultipartWire(payload, inferredType, boundary, options)
		}
	}
	switch {
	case kind != "":
		// Keep the declared structured type selected above.
	case len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '['):
		kind = "json"
	case len(trimmed) > 0 && trimmed[0] == '<':
		kind = "xml"
	case looksLikeGraphQLWire(trimmed):
		kind = "graphql"
	case looksLikeFormWire(trimmed):
		kind = "form"
	}

	switch kind {
	case "json":
		projected, replaced, result, queries, err := projectJSONSpans(payload, options)
		// Refine the diagnosis without changing the declared codec, retained
		// bytes, credential disclosure handling, or execution disposition.
		for index := range result.Findings {
			if result.Findings[index].Code == "credential_json_malformed" &&
				(mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")) &&
				requestBodyIsXML(trimmed, options.Limits) {
				result.Findings[index].Code = "request_body_xml_json_content_type_mismatch"
			}
		}
		if err != nil || result.Disposition == Rejected || projected == nil {
			return projected, result, err
		}
		for _, query := range queries {
			_, queryResult, queryErr := ProjectGraphQL(GraphQL{Query: []byte(query)}, options)
			if queryErr != nil {
				return nil, Result{}, queryErr
			}
			result.Merge(queryResult)
		}
		if result.Disposition == Rejected {
			return nil, result, nil
		}
		if spans != nil {
			*spans = replaced
		}
		return projected, result, nil
	case "xml":
		return ProjectXML(payload, options)
	case "form":
		projected, _, result, err := ProjectFormURLEncoded(string(payload), options)
		if result.Disposition == Rejected {
			return nil, result, err
		}
		return []byte(projected), result, err
	case "graphql":
		projected, result, err := ProjectGraphQL(GraphQL{Query: payload}, options)
		if result.Disposition == Rejected {
			return nil, result, err
		}
		return projected.Query, result, err
	default:
		if containsSensitiveNameHintWithOptions(payload, options) {
			if !utf8.Valid(payload) {
				if options.Mode == Preserve {
					return append([]byte(nil), payload...),
						preservedOpaquePayloadResult(
							payload,
							options,
							"credential_raw_sensitive_binary_unsupported",
						), nil
				}
				return nil, rejected(options.Source, "credential_raw_sensitive_binary_unsupported"), nil
			}
			projected, result, err := projectSensitiveScalar(
				string(payload),
				"raw_body",
				options.Source,
				TargetRef{RequestKey: options.RequestKey, Field: firstNonEmpty(options.TargetField, "body")},
				0,
				options,
			)
			if err != nil {
				return nil, Result{}, err
			}
			return []byte(projected), result, nil
		}
		if knownCredentialScalarBytesMatch(options, payload) {
			return []byte(credential.Redacted), exactResult(), nil
		}
		return append([]byte(nil), payload...), exactResult(), nil
	}
}

// requestBodyIsXML only identifies complete XML documents within the existing
// inspection limits. A leading '<' alone is not enough to diagnose a mismatch.
func requestBodyIsXML(payload []byte, limits Limits) bool {
	if len(payload) == 0 || payload[0] != '<' || len(payload) > limits.MaxBytes {
		return false
	}
	decoder := xml.NewDecoder(bytes.NewReader(payload))
	depth, values := 0, 0
	seenRoot := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return seenRoot && depth == 0
		}
		if err != nil {
			return false
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 && seenRoot {
				return false
			}
			seenRoot = true
			depth++
			values += 1 + len(token.Attr)
			if depth > limits.MaxDepth || values > limits.MaxValues {
				return false
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(token)) > 0 {
				return false
			}
		}
	}
}

func requestBodyMediaType(contentType string) (string, map[string]string) {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return "", nil
	}
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])), nil
	}
	return strings.ToLower(strings.TrimSpace(mediaType)), parameters
}

func looksLikeFormWire(payload []byte) bool {
	if len(payload) == 0 || !bytes.Contains(payload, []byte("=")) ||
		bytes.ContainsAny(payload, "\x00\r\n") {
		return false
	}
	if bytes.Count(payload, []byte("&"))+1 > DefaultLimits().MaxValues {
		return false
	}
	segments := bytes.Split(payload, []byte("&"))
	for _, segment := range segments {
		name, _, found := bytes.Cut(segment, []byte("="))
		if !found || len(bytes.TrimSpace(name)) == 0 {
			return false
		}
	}
	return true
}

func looksLikeGraphQLWire(payload []byte) bool {
	lower := strings.ToLower(strings.TrimSpace(string(payload)))
	for _, prefix := range []string{"query ", "query{", "mutation ", "mutation{", "subscription ", "subscription{", "fragment "} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func inferMultipartWireType(payload []byte) (string, string, bool) {
	lineEnd := bytes.IndexByte(payload, '\n')
	line := payload
	if lineEnd >= 0 {
		line = payload[:lineEnd]
	}
	line = bytes.TrimSuffix(bytes.TrimSpace(line), []byte("\r"))
	if !bytes.HasPrefix(line, []byte("--")) || len(line) <= 2 {
		return "", "", false
	}
	boundary := string(bytes.TrimSuffix(line[2:], []byte("--")))
	if strings.TrimSpace(boundary) == "" ||
		!bytes.Contains(bytes.ToLower(payload), []byte("content-disposition:")) {
		return "", "", false
	}
	return "multipart/form-data", boundary, true
}

type multipartWirePart struct {
	part        MultipartPart
	headerSpans []multipartWireHeaderSpan
	bodyStart   int
	bodyEnd     int
}

type multipartWireHeaderSpan struct {
	name       string
	valueStart int
	valueEnd   int
}

var errMultipartWireValueLimit = errors.New("multipart value limit exceeded")

// ProjectMultipartWire projects multipart wire bytes by applying the
// structured multipart policy and then patching only changed header-value and
// part-body spans. Boundary lines, header spelling/order, preamble, epilogue,
// line endings, and every nonsensitive byte remain untouched.
func ProjectMultipartWire(
	payload []byte,
	mediaType string,
	boundary string,
	options Options,
) ([]byte, Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, Result{}, err
	}
	if len(payload) > options.Limits.MaxBytes {
		if options.Mode == Preserve {
			return append([]byte(nil), payload...),
				preservedOpaquePayloadResult(
					payload,
					options,
					"import_credential_review_incomplete",
				), nil
		}
		return nil, rejected(options.Source, "credential_multipart_size_limit_exceeded"), nil
	}
	if options.multipartDepth >= options.Limits.MaxDepth {
		if options.Mode == Preserve {
			return append([]byte(nil), payload...),
				preservedOpaquePayloadResult(
					payload,
					options,
					"import_credential_review_incomplete",
				), nil
		}
		return nil, rejected(options.Source, "credential_multipart_depth_limit_exceeded"), nil
	}
	if strings.TrimSpace(boundary) == "" {
		if options.Mode == Preserve {
			return append([]byte(nil), payload...),
				preservedOpaquePayloadResult(
					payload,
					options,
					"credential_multipart_boundary_missing",
				), nil
		}
		return nil, rejected(options.Source, "credential_multipart_boundary_missing"), nil
	}
	if !validMultipartBoundary(boundary) {
		if options.Mode == Preserve {
			return append([]byte(nil), payload...),
				preservedOpaquePayloadResult(
					payload,
					options,
					"credential_multipart_boundary_invalid",
				), nil
		}
		return nil, rejected(options.Source, "credential_multipart_boundary_invalid"), nil
	}
	parts, parseErr := parseMultipartWire(payload, boundary, options)
	if parseErr != nil {
		if errors.Is(parseErr, errMultipartWireValueLimit) {
			if options.Mode == Preserve {
				return append([]byte(nil), payload...),
					preservedOpaquePayloadResult(
						payload,
						options,
						"import_credential_review_incomplete",
					), nil
			}
			return nil, rejected(options.Source, "credential_multipart_value_limit_exceeded"), nil
		}
		if options.Mode == Preserve {
			result := preservedOpaquePayloadResult(
				payload,
				options,
				"credential_multipart_malformed",
			)
			return append([]byte(nil), payload...), result, nil
		}
		if containsSensitiveNameHintWithOptions(payload, options) {
			return nil, rejected(options.Source, "credential_multipart_malformed_sensitive"), nil
		}
		if options.KnownCredentialMatcher != nil {
			return nil, rejected(options.Source, "credential_multipart_known_value_malformed"), nil
		}
		return append([]byte(nil), payload...), blocked(options.Source, "credential_multipart_malformed"), nil
	}
	input := Multipart{Boundary: boundary, Parts: make([]MultipartPart, len(parts))}
	for index := range parts {
		input.Parts[index] = parts[index].part
	}
	projected, result, err := ProjectMultipart(input, options)
	if err != nil {
		return nil, Result{}, err
	}
	if result.Disposition == Rejected {
		return nil, result, nil
	}
	if len(projected.Parts) != len(parts) {
		return nil, Result{}, errors.New("multipart projection changed part count")
	}
	var replacements []replacement
	preambleEnd, epilogueStart, envelopeOK := multipartEnvelopeSpans(
		payload,
		boundary,
	)
	if !envelopeOK {
		return nil, Result{}, errors.New("multipart projection lost envelope delimiters")
	}
	if options.KnownCredentialMatcher != nil {
		preamble := payload[:preambleEnd]
		if knownCredentialScalarBytesMatch(options, preamble) {
			replacements = append(replacements, replacement{
				start: 0,
				end:   preambleEnd,
				value: redactMultipartPreamble(preamble),
			})
		}
		epilogue := payload[epilogueStart:]
		if knownCredentialScalarBytesMatch(options, epilogue) {
			replacements = append(replacements, replacement{
				start: epilogueStart,
				end:   len(payload),
				value: []byte(credential.Redacted),
			})
		}
	}
	if knownCredentialScalarMatch(options, boundary) {
		disclosureBoundary := disclosureMultipartBoundaryReplacement(boundary)
		disclosureMarker := []byte("--" + disclosureBoundary)
		if bytes.Contains(payload, disclosureMarker) {
			return nil,
				rejected(options.Source, "credential_multipart_disclosure_boundary_collision"),
				nil
		}
		marker := []byte("--" + boundary)
		from := 0
		for {
			start, closing, lineEnd, ok := nextMultipartDelimiter(
				payload,
				marker,
				from,
			)
			if !ok {
				return nil,
					rejected(options.Source, "credential_multipart_disclosure_boundary_missing"),
					nil
			}
			replacements = append(replacements, replacement{
				start: start + 2,
				end:   start + 2 + len(boundary),
				value: []byte(disclosureBoundary),
			})
			if closing {
				break
			}
			from = lineEnd
		}
	}
	for partIndex := range parts {
		wirePart := parts[partIndex]
		projectedPart := projected.Parts[partIndex]
		if !bytes.Equal(wirePart.part.Body, projectedPart.Body) {
			replacements = append(replacements, replacement{
				start: wirePart.bodyStart,
				end:   wirePart.bodyEnd,
				value: projectedPart.Body,
			})
		}
		if len(wirePart.headerSpans) != len(projectedPart.Headers) {
			return nil, Result{}, errors.New("multipart projection changed header count")
		}
		for headerIndex := range wirePart.headerSpans {
			span := wirePart.headerSpans[headerIndex]
			header := projectedPart.Headers[headerIndex]
			if !strings.EqualFold(span.name, header.Name) {
				return nil, Result{}, errors.New("multipart projection changed header identity")
			}
			if string(payload[span.valueStart:span.valueEnd]) == header.Value {
				continue
			}
			replacements = append(replacements, replacement{
				start: span.valueStart,
				end:   span.valueEnd,
				value: []byte(header.Value),
			})
		}
	}
	return applyReplacements(payload, replacements), result, nil
}

func multipartEnvelopeSpans(
	payload []byte,
	boundary string,
) (preambleEnd int, epilogueStart int, ok bool) {
	marker := []byte("--" + boundary)
	start, closing, lineEnd, found := nextMultipartDelimiter(payload, marker, 0)
	if !found || closing {
		return 0, 0, false
	}
	preambleEnd = start
	from := lineEnd
	for {
		_, closing, nextLineEnd, found := nextMultipartDelimiter(
			payload,
			marker,
			from,
		)
		if !found {
			return 0, 0, false
		}
		if closing {
			return preambleEnd, nextLineEnd, true
		}
		from = nextLineEnd
	}
}

func redactMultipartPreamble(preamble []byte) []byte {
	switch {
	case bytes.HasSuffix(preamble, []byte("\r\n")):
		return []byte(credential.Redacted + "\r\n")
	case bytes.HasSuffix(preamble, []byte("\n")):
		return []byte(credential.Redacted + "\n")
	default:
		return []byte(credential.Redacted)
	}
}

func parseMultipartWire(payload []byte, boundary string, options Options) ([]multipartWirePart, error) {
	marker := []byte("--" + boundary)
	start, closing, lineEnd, ok := nextMultipartDelimiter(payload, marker, 0)
	if !ok || closing {
		return nil, errors.New("multipart opening boundary is missing")
	}
	_ = start // Preamble is retained by span-only rewriting.
	var parts []multipartWirePart
	values := 0
	cursor := lineEnd
	for {
		if len(parts) >= options.Limits.MaxParts {
			return nil, errors.New("multipart part limit exceeded")
		}
		nextStart, nextClosing, nextLineEnd, found := nextMultipartDelimiter(payload, marker, cursor)
		if !found {
			return nil, errors.New("multipart closing boundary is missing")
		}
		partEnd := nextStart
		if partEnd >= 2 && bytes.Equal(payload[partEnd-2:partEnd], []byte("\r\n")) {
			partEnd -= 2
		} else if partEnd >= 1 && payload[partEnd-1] == '\n' {
			partEnd--
		}
		part, err := parseMultipartWirePart(payload, cursor, partEnd, options, len(parts))
		if err != nil {
			return nil, err
		}
		values += 1 + len(part.part.Headers)
		if values > options.Limits.MaxValues {
			return nil, errMultipartWireValueLimit
		}
		parts = append(parts, part)
		if nextClosing {
			return parts, nil
		}
		cursor = nextLineEnd
	}
}

func nextMultipartDelimiter(
	payload []byte,
	marker []byte,
	from int,
) (start int, closing bool, lineEnd int, ok bool) {
	for from <= len(payload)-len(marker) {
		relative := bytes.Index(payload[from:], marker)
		if relative < 0 {
			return 0, false, 0, false
		}
		start = from + relative
		if start > 0 && payload[start-1] != '\n' {
			from = start + 1
			continue
		}
		cursor := start + len(marker)
		if cursor+2 <= len(payload) && bytes.Equal(payload[cursor:cursor+2], []byte("--")) {
			closing = true
			cursor += 2
		}
		for cursor < len(payload) && (payload[cursor] == ' ' || payload[cursor] == '\t') {
			cursor++
		}
		switch {
		case cursor == len(payload):
			return start, closing, cursor, true
		case cursor+2 <= len(payload) && bytes.Equal(payload[cursor:cursor+2], []byte("\r\n")):
			return start, closing, cursor + 2, true
		case payload[cursor] == '\n':
			return start, closing, cursor + 1, true
		default:
			from = start + 1
		}
	}
	return 0, false, 0, false
}

func parseMultipartWirePart(
	payload []byte,
	start int,
	end int,
	options Options,
	index int,
) (multipartWirePart, error) {
	if start < 0 || end < start || end > len(payload) {
		return multipartWirePart{}, errors.New("multipart part span is invalid")
	}
	headerEnd, bodyStart, ok := multipartHeaderSeparator(payload, start, end)
	if !ok {
		return multipartWirePart{}, errors.New("multipart part header separator is missing")
	}
	headers, spans, err := parseMultipartWireHeaders(payload, start, headerEnd, options, index)
	if err != nil {
		return multipartWirePart{}, err
	}
	part := MultipartPart{
		Headers: headers,
		Body:    append([]byte(nil), payload[bodyStart:end]...),
		Source: SourceRef{
			Format:  options.Source.Format,
			Path:    options.Source.Path,
			Pointer: appendSourcePointer(options.Source.Pointer, "/multipart/parts/"+strconv.Itoa(index)),
			ItemID:  options.Source.ItemID,
		},
	}
	contentDispositionCount := 0
	for _, header := range headers {
		switch {
		case strings.EqualFold(header.Name, "Content-Type"):
			part.ContentType = header.Value
		case strings.EqualFold(header.Name, "Content-Disposition"):
			contentDispositionCount++
			if contentDispositionCount > 1 {
				// Multiple disposition headers make the effective field name
				// ambiguous. Choosing the last occurrence could let a benign
				// name hide an earlier credential-bearing name while retaining
				// the literal part body.
				return multipartWirePart{}, errors.New("multipart Content-Disposition is duplicated")
			}
			disposition, parameters, parseErr := mime.ParseMediaType(header.Value)
			if parseErr != nil || !strings.EqualFold(disposition, "form-data") {
				return multipartWirePart{}, errors.New("multipart Content-Disposition is invalid")
			}
			part.Name = parameters["name"]
			part.FileName = parameters["filename"]
		}
	}
	return multipartWirePart{
		part:        part,
		headerSpans: spans,
		bodyStart:   bodyStart,
		bodyEnd:     end,
	}, nil
}

func multipartHeaderSeparator(payload []byte, start, end int) (headerEnd, bodyStart int, ok bool) {
	part := payload[start:end]
	crlf := bytes.Index(part, []byte("\r\n\r\n"))
	lf := bytes.Index(part, []byte("\n\n"))
	switch {
	case crlf >= 0 && (lf < 0 || crlf <= lf):
		return start + crlf, start + crlf + 4, true
	case lf >= 0:
		return start + lf, start + lf + 2, true
	default:
		return 0, 0, false
	}
}

func parseMultipartWireHeaders(
	payload []byte,
	start int,
	end int,
	options Options,
	partIndex int,
) ([]HeaderOccurrence, []multipartWireHeaderSpan, error) {
	var headers []HeaderOccurrence
	var spans []multipartWireHeaderSpan
	cursor := start
	for cursor < end {
		lineEnd := bytes.IndexByte(payload[cursor:end], '\n')
		if lineEnd < 0 {
			lineEnd = end
		} else {
			lineEnd += cursor
		}
		contentEnd := lineEnd
		if contentEnd > cursor && payload[contentEnd-1] == '\r' {
			contentEnd--
		}
		if contentEnd == cursor || payload[cursor] == ' ' || payload[cursor] == '\t' {
			return nil, nil, errors.New("multipart folded or empty header is unsupported")
		}
		colon := bytes.IndexByte(payload[cursor:contentEnd], ':')
		if colon <= 0 {
			return nil, nil, errors.New("multipart header lacks a name or colon")
		}
		colon += cursor
		name := strings.TrimSpace(string(payload[cursor:colon]))
		valueStart := colon + 1
		for valueStart < contentEnd && (payload[valueStart] == ' ' || payload[valueStart] == '\t') {
			valueStart++
		}
		valueEnd := contentEnd
		for valueEnd > valueStart && (payload[valueEnd-1] == ' ' || payload[valueEnd-1] == '\t') {
			valueEnd--
		}
		if name == "" {
			return nil, nil, errors.New("multipart header name is empty")
		}
		headers = append(headers, HeaderOccurrence{
			Name:  name,
			Value: string(payload[valueStart:valueEnd]),
			Source: SourceRef{
				Format:  options.Source.Format,
				Path:    options.Source.Path,
				Pointer: appendSourcePointer(options.Source.Pointer, fmt.Sprintf("/multipart/parts/%d/headers/%d", partIndex, len(headers))),
				ItemID:  options.Source.ItemID,
			},
		})
		spans = append(spans, multipartWireHeaderSpan{
			name:       name,
			valueStart: valueStart,
			valueEnd:   valueEnd,
		})
		if lineEnd == end {
			break
		}
		cursor = lineEnd + 1
	}
	return headers, spans, nil
}
