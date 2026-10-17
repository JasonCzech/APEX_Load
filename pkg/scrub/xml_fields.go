package scrub

import (
	"html"
	"regexp"
	"sort"
	"strings"
)

// These patterns deliberately recognize XML structure rather than credential
// vocabulary. Callers supply the authoritative name classifier so general
// scrubbing and correlation-preserving credential scrubbing cannot drift from
// their respective shared policies.
var (
	xmlMarkupPattern = regexp.MustCompile(`(?is)<!--.*?-->|<!\[CDATA\[.*?\]\]>|<\?.*?\?>|<(?:[^>"']|"[^"]*"|'[^']*')*>`)
	xmlOpenName      = regexp.MustCompile(`(?is)^<\s*([A-Za-z_][\w.-]*(?::[A-Za-z_][\w.-]*)?)(?:[[:space:]/>]|$)`)
	xmlCloseName     = regexp.MustCompile(`(?is)^<\s*/\s*([A-Za-z_][\w.-]*(?::[A-Za-z_][\w.-]*)?)\s*>`)
)

type xmlElementFrame struct {
	name         string
	contentStart int
	sensitive    bool
}

type xmlElementStack struct {
	frames    []xmlElementFrame
	positions map[string][]int
}

type redactionSpan struct {
	start int
	end   int
}

// redactXMLFields preserves the original XML byte layout while redacting
// classified element content and attribute values. It is intentionally a
// bounded structural fallback rather than an XML decoder: recorded/imported
// payloads can be fragments or have a malformed outer document, and re-encoding
// them would alter namespaces, comments, and request-significant whitespace.
func redactXMLFields(value string, sensitiveName func(string) bool) string {
	if value == "" || sensitiveName == nil || !strings.Contains(value, "<") {
		return value
	}

	stack := xmlElementStack{
		frames:    make([]xmlElementFrame, 0, 8),
		positions: make(map[string][]int),
	}
	spans := make([]redactionSpan, 0, 4)
	searchAt := 0
	foundMarkup := false
	for searchAt < len(value) {
		bounds := xmlMarkupPattern.FindStringIndex(value[searchAt:])
		if bounds == nil {
			break
		}
		start, end := searchAt+bounds[0], searchAt+bounds[1]
		// The complete-markup matcher deliberately skips a tag whose quoted
		// attribute never closes. Do not let a later valid tag move the cursor
		// past that malformed opening: a sensitive attribute in the skipped gap
		// is still untrusted and must fail closed.
		if gapSpans, found := malformedXMLOpeningTagSpans(value, searchAt, start, sensitiveName); found {
			spans = append(spans, gapSpans...)
		}
		foundMarkup = true
		searchAt = end
		tag := value[start:end]
		trimmed := strings.TrimSpace(tag)
		if strings.HasPrefix(trimmed, "<!--") ||
			strings.HasPrefix(trimmed, "<![CDATA[") ||
			strings.HasPrefix(trimmed, "<?") ||
			strings.HasPrefix(trimmed, "<!") {
			continue
		}

		if match := xmlCloseName.FindStringSubmatch(tag); len(match) == 2 {
			frameIndex := stack.matchingFrame(match[1])
			if frameIndex < 0 {
				continue
			}
			// A matching ancestor close also terminates any malformed unclosed
			// descendants above it. Redact every sensitive frame being removed;
			// otherwise <root><password>secret</root> leaks while recovering.
			for index := frameIndex; index < len(stack.frames); index++ {
				frame := stack.frames[index]
				if frame.sensitive && frame.contentStart <= start {
					spans = append(spans, redactionSpan{start: frame.contentStart, end: start})
				}
			}
			stack.truncate(frameIndex)
			continue
		}

		match := xmlOpenName.FindStringSubmatch(tag)
		if len(match) != 2 {
			continue
		}
		spans = append(spans, classifiedXMLAttributeSpans(tag, start, sensitiveName)...)
		if isSelfClosingXMLTag(tag) {
			continue
		}
		open := xmlOpenName.FindStringSubmatchIndex(tag)
		namedCredential := len(open) == 4 &&
			xmlTagHasNamedCredential(tag, open[3], sensitiveName)
		stack.push(xmlElementFrame{
			name:         match[1],
			contentStart: end,
			sensitive:    sensitiveName(match[1]) || namedCredential,
		})
	}
	if trailingSpans, ok := malformedXMLOpeningTagSpans(value, searchAt, len(value), sensitiveName); ok {
		foundMarkup = true
		spans = append(spans, trailingSpans...)
	}
	if !foundMarkup {
		return value
	}

	// Truncated XML is still untrusted input. Once a sensitive opening element
	// has been recognized, fail closed through EOF rather than retaining its
	// unterminated value.
	for _, frame := range stack.frames {
		if frame.sensitive && frame.contentStart <= len(value) {
			spans = append(spans, redactionSpan{start: frame.contentStart, end: len(value)})
		}
	}

	return applyRedactionSpans(value, spans)
}

// malformedXMLOpeningTagSpans recognizes incomplete opening tags in the
// bounded [searchAt,end) range. Complete-tag matching intentionally requires
// `>` so malformed quoted attributes cannot be mistaken for valid markup; this
// pass supplies the corresponding fail-closed behavior both at EOF and in gaps
// before a later recognized tag.
func malformedXMLOpeningTagSpans(value string, searchAt, end int, sensitiveName func(string) bool) ([]redactionSpan, bool) {
	if searchAt < 0 || end <= searchAt || end > len(value) {
		return nil, false
	}
	spans := make([]redactionSpan, 0, 1)
	found := false
	cursor := searchAt
	for cursor < end {
		relativeStart := strings.IndexByte(value[cursor:end], '<')
		if relativeStart < 0 {
			break
		}
		start := cursor + relativeStart
		tagEnd := end
		if relativeNext := strings.IndexByte(value[start+1:end], '<'); relativeNext >= 0 {
			tagEnd = start + 1 + relativeNext
		}
		tag := value[start:tagEnd]
		match := xmlOpenName.FindStringSubmatchIndex(tag)
		if len(match) != 4 {
			cursor = start + 1
			continue
		}
		found = true
		spans = append(spans, classifiedXMLAttributeSpans(tag, start, sensitiveName)...)
		if sensitiveName(tag[match[2]:match[3]]) ||
			xmlTagHasNamedCredential(tag, match[3], sensitiveName) {
			// An incomplete opening tag has no reliable content boundary. Once
			// its element identity is sensitive, everything after the name in
			// this bounded malformed segment is potential payload.
			payloadStart := match[3]
			for payloadStart < len(tag) && isXMLSpace(tag[payloadStart]) {
				payloadStart++
			}
			if payloadStart < len(tag) {
				spans = append(spans, redactionSpan{
					start: start + payloadStart,
					end:   tagEnd,
				})
			}
		}
		cursor = tagEnd
	}
	return spans, found
}

func (stack *xmlElementStack) push(frame xmlElementFrame) {
	index := len(stack.frames)
	stack.frames = append(stack.frames, frame)
	key := strings.ToLower(frame.name)
	stack.positions[key] = append(stack.positions[key], index)
}

func (stack *xmlElementStack) matchingFrame(name string) int {
	positions := stack.positions[strings.ToLower(name)]
	if len(positions) == 0 {
		return -1
	}
	return positions[len(positions)-1]
}

func (stack *xmlElementStack) truncate(index int) {
	if index < 0 || index > len(stack.frames) {
		return
	}
	// Each frame is removed from its name index exactly once. Unmatched close
	// tags are O(1), making malformed deeply nested input amortized linear.
	for frameIndex := len(stack.frames) - 1; frameIndex >= index; frameIndex-- {
		key := strings.ToLower(stack.frames[frameIndex].name)
		positions := stack.positions[key]
		if len(positions) <= 1 {
			delete(stack.positions, key)
			continue
		}
		stack.positions[key] = positions[:len(positions)-1]
	}
	stack.frames = stack.frames[:index]
}

func classifiedXMLAttributeSpans(tag string, offset int, sensitiveName func(string) bool) []redactionSpan {
	open := xmlOpenName.FindStringSubmatchIndex(tag)
	if len(open) != 4 {
		return nil
	}
	elementSensitive := sensitiveName(tag[open[2]:open[3]])
	namedCredential := xmlTagHasNamedCredential(tag, open[3], sensitiveName)
	spans := make([]redactionSpan, 0, 2)
	cursor := open[3]
	for cursor < len(tag) {
		for cursor < len(tag) && isXMLSpace(tag[cursor]) {
			cursor++
		}
		if cursor >= len(tag) || tag[cursor] == '>' {
			break
		}
		if tag[cursor] == '/' {
			cursor++
			continue
		}

		nameStart := cursor
		if !isXMLNameStart(tag[cursor]) {
			// Invalid token inside a sensitive element's opening tag is
			// indistinguishable from a bare credential payload. Redact the
			// bounded token while leaving surrounding markup untouched.
			for cursor < len(tag) && !isXMLSpace(tag[cursor]) && tag[cursor] != '>' {
				cursor++
			}
			if elementSensitive && cursor > nameStart {
				spans = append(spans, redactionSpan{
					start: offset + nameStart,
					end:   offset + cursor,
				})
			}
			continue
		}
		cursor++
		for cursor < len(tag) && isXMLNameContinue(tag[cursor]) {
			cursor++
		}
		nameEnd := cursor
		name := tag[nameStart:nameEnd]
		for cursor < len(tag) && isXMLSpace(tag[cursor]) {
			cursor++
		}
		if cursor >= len(tag) || tag[cursor] != '=' {
			if elementSensitive {
				spans = append(spans, redactionSpan{
					start: offset + nameStart,
					end:   offset + nameEnd,
				})
			}
			continue
		}
		cursor++
		for cursor < len(tag) && isXMLSpace(tag[cursor]) {
			cursor++
		}
		if cursor >= len(tag) || tag[cursor] == '>' {
			continue
		}

		valueStart := cursor
		var valueEnd int
		malformedSuffixStart := -1
		malformedSuffixEnd := -1
		switch tag[cursor] {
		case '"', '\'':
			quote := tag[cursor]
			cursor++
			valueStart = cursor
			for cursor < len(tag) && tag[cursor] != quote {
				cursor++
			}
			valueEnd = cursor
			if cursor < len(tag) {
				cursor++
				if cursor < len(tag) && !isXMLAttributeDelimiter(tag[cursor]) {
					// A quote only terminates an XML attribute when the next byte
					// is a legal attribute delimiter. Otherwise
					// password="first"second is one ambiguous sensitive value,
					// not a safe value followed by an unrelated attribute name.
					malformedSuffixStart = cursor
					for cursor < len(tag) && !isXMLAttributeDelimiter(tag[cursor]) {
						cursor++
					}
					malformedSuffixEnd = cursor
				}
			}
		default:
			for cursor < len(tag) && !isXMLSpace(tag[cursor]) && tag[cursor] != '>' {
				cursor++
			}
			valueEnd = cursor
			// In <root password=secret/>, the final slash is markup rather
			// than part of the invalid unquoted value.
			if valueEnd > valueStart && valueEnd < len(tag) &&
				tag[valueEnd] == '>' && tag[valueEnd-1] == '/' {
				valueEnd--
			}
		}

		if isXMLNamespaceDeclaration(name) ||
			(!elementSensitive &&
				!sensitiveName(name) &&
				!(namedCredential && isXMLCredentialValueAttribute(name))) {
			continue
		}
		if valueEnd >= valueStart {
			spans = append(spans, redactionSpan{
				start: offset + valueStart,
				end:   offset + valueEnd,
			})
		}
		if malformedSuffixEnd > malformedSuffixStart {
			spans = append(spans, redactionSpan{
				start: offset + malformedSuffixStart,
				end:   offset + malformedSuffixEnd,
			})
		}
	}
	return spans
}

func xmlTagHasNamedCredential(tag string, cursor int, sensitiveName func(string) bool) bool {
	for cursor < len(tag) {
		for cursor < len(tag) && isXMLSpace(tag[cursor]) {
			cursor++
		}
		if cursor >= len(tag) || tag[cursor] == '>' {
			return false
		}
		if tag[cursor] == '/' {
			cursor++
			continue
		}
		if !isXMLNameStart(tag[cursor]) {
			for cursor < len(tag) && !isXMLSpace(tag[cursor]) && tag[cursor] != '>' {
				cursor++
			}
			continue
		}
		nameStart := cursor
		cursor++
		for cursor < len(tag) && isXMLNameContinue(tag[cursor]) {
			cursor++
		}
		name := tag[nameStart:cursor]
		for cursor < len(tag) && isXMLSpace(tag[cursor]) {
			cursor++
		}
		if cursor >= len(tag) || tag[cursor] != '=' {
			continue
		}
		cursor++
		for cursor < len(tag) && isXMLSpace(tag[cursor]) {
			cursor++
		}
		if cursor >= len(tag) || tag[cursor] == '>' {
			continue
		}
		valueStart := cursor
		var valueEnd int
		switch tag[cursor] {
		case '"', '\'':
			quote := tag[cursor]
			cursor++
			valueStart = cursor
			for cursor < len(tag) && tag[cursor] != quote {
				cursor++
			}
			valueEnd = cursor
			if cursor < len(tag) {
				cursor++
			}
		default:
			for cursor < len(tag) && !isXMLSpace(tag[cursor]) && tag[cursor] != '>' {
				cursor++
			}
			valueEnd = cursor
			if valueEnd > valueStart && valueEnd < len(tag) &&
				tag[valueEnd] == '>' && tag[valueEnd-1] == '/' {
				valueEnd--
			}
		}
		if !isXMLCredentialNameAttribute(name) || valueEnd < valueStart {
			continue
		}
		if sensitiveName(html.UnescapeString(tag[valueStart:valueEnd])) {
			return true
		}
	}
	return false
}

func isXMLCredentialNameAttribute(name string) bool {
	if index := strings.LastIndexByte(name, ':'); index >= 0 {
		name = name[index+1:]
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "name", "key", "field", "property":
		return true
	default:
		return false
	}
}

func isXMLCredentialValueAttribute(name string) bool {
	if index := strings.LastIndexByte(name, ':'); index >= 0 {
		name = name[index+1:]
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "value", "val", "default", "fixed":
		return true
	default:
		return false
	}
}

func isXMLAttributeDelimiter(ch byte) bool {
	return isXMLSpace(ch) || ch == '/' || ch == '>'
}

func isXMLSpace(ch byte) bool {
	switch ch {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func isXMLNameStart(ch byte) bool {
	return ch == '_' ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= 'a' && ch <= 'z')
}

func isXMLNameContinue(ch byte) bool {
	return isXMLNameStart(ch) ||
		(ch >= '0' && ch <= '9') ||
		ch == '.' || ch == '-' || ch == ':'
}

func isXMLNamespaceDeclaration(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "xmlns" || strings.HasPrefix(name, "xmlns:")
}

func isSelfClosingXMLTag(tag string) bool {
	tag = strings.TrimSpace(tag)
	if !strings.HasSuffix(tag, ">") {
		return false
	}
	return strings.HasSuffix(strings.TrimSpace(strings.TrimSuffix(tag, ">")), "/")
}

func xmlLocalName(name string) string {
	if separator := strings.LastIndexByte(name, ':'); separator >= 0 {
		return name[separator+1:]
	}
	return name
}

func applyRedactionSpans(value string, spans []redactionSpan) string {
	return applyRedactionSpansWithReplacement(value, spans, Redacted)
}

func applyRedactionSpansWithReplacement(value string, spans []redactionSpan, replacement string) string {
	if len(spans) == 0 {
		return value
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start == spans[j].start {
			return spans[i].end > spans[j].end
		}
		return spans[i].start < spans[j].start
	})

	var out strings.Builder
	out.Grow(len(value))
	cursor := 0
	for _, span := range spans {
		if span.start < 0 || span.end < span.start || span.end > len(value) || span.end <= cursor {
			continue
		}
		if span.start < cursor {
			cursor = span.end
			continue
		}
		out.WriteString(value[cursor:span.start])
		out.WriteString(replacement)
		cursor = span.end
	}
	out.WriteString(value[cursor:])
	return out.String()
}
