package aurapayload

import (
	"sort"
	"unicode/utf8"
)

// Sparse spans follow only bounded authored template occurrences through
// existing decoding layers. No body-sized byte-offset table is allocated.
type templateSpan struct {
	surface    *templateSurface
	index      int
	start, end int
}

func originalExecutableOccurrences(p *payload, prepared *preparedTemplates) (map[*templateSurface]map[int]bool, error) {
	result := make(map[*templateSurface]map[int]bool)
	var spans []templateSpan
	message, err := documentTemplateSpans(p.doc, p.doc.root, prepared.message, p.budget)
	if err != nil {
		return nil, err
	}
	if p.codec == FormMessage {
		message, err = formTemplateSpans(p.wire[p.fields[p.messageIndex].valueStart:p.fields[p.messageIndex].valueEnd], message, p.budget)
		if err != nil {
			return nil, err
		}
		shiftTemplateSpans(message, p.fields[p.messageIndex].valueStart)
	}
	spans = append(spans, message...)
	for i, field := range p.fields {
		if i == p.messageIndex {
			continue
		}
		var fieldSpans []templateSpan
		if q := prepared.jsonFields[i]; q != nil {
			fieldSpans, err = documentTemplateSpans(q.doc, q.doc.root, prepared.jsonLeaves[i], p.budget)
			if err != nil {
				return nil, err
			}
		} else if surface := prepared.fields[i]; surface != nil {
			fieldSpans = surfaceTemplateSpans(surface)
		}
		fieldSpans, err = formTemplateSpans(p.wire[field.valueStart:field.valueEnd], fieldSpans, p.budget)
		if err != nil {
			return nil, err
		}
		shiftTemplateSpans(fieldSpans, field.valueStart)
		spans = append(spans, fieldSpans...)
	}
	executablePositions := make(map[int]int)
	for _, span := range spans {
		match := span.surface.matches[span.index]
		token := span.surface.value[match[0]:match[1]]
		if p.wire[span.start:span.end] == token {
			if result[span.surface] == nil {
				result[span.surface] = map[int]bool{}
			}
			result[span.surface][span.index] = true
			executablePositions[span.start] = span.end
		}
	}
	// Old executable key/syntax templates have no string-value consumer in the
	// structural codec. Refuse that unsupported promotion rather than silently
	// freezing or redirecting them.
	if err := p.budget.addWork(len(p.wire)); err != nil {
		return nil, err
	}
	rawMatches := authoredTemplatePattern.FindAllStringIndex(p.wire, MaxTemplateOccurrences+1)
	if len(rawMatches) > MaxTemplateOccurrences {
		return nil, fail(TokenLimit)
	}
	for _, match := range rawMatches {
		if executablePositions[match[0]] != match[1] {
			return nil, fail(InvalidTemplateContract)
		}
	}
	return result, nil
}

func surfaceTemplateSpans(surface *templateSurface) []templateSpan {
	spans := make([]templateSpan, len(surface.matches))
	for i, match := range surface.matches {
		spans[i] = templateSpan{surface: surface, index: i, start: match[0], end: match[1]}
	}
	return spans
}

func shiftTemplateSpans(spans []templateSpan, offset int) {
	for i := range spans {
		spans[i].start += offset
		spans[i].end += offset
	}
}

func documentTemplateSpans(doc *document, n *node, surfaces map[*node]*templateSurface, b *budget) ([]templateSpan, error) {
	var spans []templateSpan
	if n.inner != nil {
		var err error
		spans, err = documentTemplateSpans(n.inner, n.inner.root, surfaces, b)
		if err != nil {
			return nil, err
		}
	} else if surface := surfaces[n]; surface != nil {
		spans = surfaceTemplateSpans(surface)
	} else {
		for _, member := range n.members {
			child, err := documentTemplateSpans(doc, member.value, surfaces, b)
			if err != nil {
				return nil, err
			}
			spans = append(spans, child...)
		}
		for _, element := range n.elements {
			child, err := documentTemplateSpans(doc, element, surfaces, b)
			if err != nil {
				return nil, err
			}
			spans = append(spans, child...)
		}
		return spans, nil
	}
	if len(spans) == 0 {
		return nil, nil
	}
	spans, err := decodeTemplateSpans(doc.source[n.start:n.end], spans, true, b)
	if err != nil {
		return nil, err
	}
	shiftTemplateSpans(spans, n.start)
	return spans, nil
}

func formTemplateSpans(source string, spans []templateSpan, b *budget) ([]templateSpan, error) {
	return decodeTemplateSpans(source, spans, false, b)
}

func decodeTemplateSpans(source string, spans []templateSpan, jsonString bool, b *budget) ([]templateSpan, error) {
	if len(spans) == 0 {
		return spans, nil
	}
	if err := b.addWork(len(source)); err != nil {
		return nil, err
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	raw, decoded := 0, 0
	limit := len(source)
	if jsonString {
		raw, limit = 1, limit-1
	}
	advance := func(target int) (int, error) {
		for decoded < target && raw < limit {
			width, outputWidth := 1, 1
			if jsonString && source[raw] == '\\' {
				width = 2
				if raw+1 >= limit {
					return 0, fail(InvalidTemplateContract)
				}
				if source[raw+1] == 'u' {
					if raw+6 > limit {
						return 0, fail(InvalidTemplateContract)
					}
					value := 0
					for _, c := range source[raw+2 : raw+6] {
						value = value*16 + provenanceHex(byte(c))
					}
					width, outputWidth = 6, utf8.RuneLen(rune(value))
					if value >= 0xD800 && value <= 0xDBFF {
						width, outputWidth = 12, 4
					}
				}
			} else if !jsonString && source[raw] == '%' {
				width = 3
			}
			if width > limit-raw || outputWidth <= 0 {
				return 0, fail(InvalidTemplateContract)
			}
			raw, decoded = raw+width, decoded+outputWidth
		}
		if decoded != target {
			return 0, fail(InvalidTemplateContract)
		}
		return raw, nil
	}
	for i := range spans {
		start, err := advance(spans[i].start)
		if err != nil {
			return nil, err
		}
		end, err := advance(spans[i].end)
		if err != nil {
			return nil, err
		}
		spans[i].start, spans[i].end = start, end
	}
	return spans, nil
}

func provenanceHex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}
