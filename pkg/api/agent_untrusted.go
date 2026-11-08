package api

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	// agentUntrustedMaxLines bounds untrusted text by lines as well as runes.
	agentUntrustedMaxLines = 12
	// agentSafeNameMaxRunes bounds a name a templated sentence carries.
	agentSafeNameMaxRunes = 80
	// agentTitleMaxRunes bounds a finding title.
	agentTitleMaxRunes = 160
)

// agentUntrustedText prepares text from the system under test (an error
// sample, a response body, server error text) for a brief: secrets are
// masked with the shared scrubber, control and bidirectional-override
// characters are dropped, blank-line runs collapse, and the result is capped
// by runes and lines. The text stays otherwise raw; only Markdown rendering
// fences it. A nil result means there was nothing to keep.
func agentUntrustedText(source, raw string, maxRunes int) *AgentUntrusted {
	if maxRunes <= 0 {
		return nil
	}
	text := scrub.Value(strings.ToValidUTF8(raw, "�"), scrub.Policy{})
	text = stripAgentUnsafeRunes(text, true)
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	truncated := false
	blank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			if blank || len(kept) == 0 {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		if len(kept) == agentUntrustedMaxLines {
			truncated = true
			break
		}
		kept = append(kept, line)
	}
	text = strings.TrimSpace(strings.Join(kept, "\n"))
	if utf8.RuneCountInString(text) > maxRunes {
		text = string([]rune(text)[:maxRunes])
		truncated = true
	}
	if text == "" {
		return nil
	}
	return &AgentUntrusted{Source: source, Text: text, Truncated: truncated}
}

// agentSafeName prepares a user-authored name (a transaction, script or
// group) for a templated sentence: it keeps letters, digits and a few
// separators, replaces anything else with "_", and caps the length, so a
// name cannot carry markup or prose aimed at a model.
func agentSafeName(name string) string {
	name = strings.Join(strings.Fields(stripAgentUnsafeRunes(name, false)), " ")
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), strings.ContainsRune(" _./:-#()", r):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if utf8.RuneCountInString(out) > agentSafeNameMaxRunes {
		out = string([]rune(out)[:agentSafeNameMaxRunes-1]) + "…"
	}
	return out
}

// agentSafeText prepares engine-generated prose (a hypothesis title, which
// may embed metric and node names) for a title: one line, no control or
// bidi characters, capped.
func agentSafeText(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(stripAgentUnsafeRunes(text, false)), " ")
	if utf8.RuneCountInString(text) > maxRunes {
		text = string([]rune(text)[:maxRunes-1]) + "…"
	}
	return text
}

// stripAgentUnsafeRunes drops control characters (Cc, keeping newlines when
// asked) and every format character (Cf: the bidirectional controls that can
// reorder displayed text, zero-width spaces and joiners, the soft hyphen, the
// BOM and tag characters), so an invisible character cannot hide the
// boundary tag's name from agentBoundaryNamePattern. The MCP server's
// sanitize_untrusted_text applies the same rule.
func stripAgentUnsafeRunes(text string, keepNewlines bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && keepNewlines:
			return r
		case r == '\t':
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), agentInvisibleRune(r):
			return -1
		}
		return r
	}, strings.ReplaceAll(text, "\r\n", "\n"))
}

// agentInvisibleRune reports the default-ignorable code points outside Cc/Cf
// that render as nothing: the combining grapheme joiner, Hangul fillers, Khmer
// inherent vowels, Mongolian and other variation selectors, the unassigned
// default-ignorables U+2065 and U+FFF0-U+FFF8, and the
// tag/variation-selector supplement. The MCP server's INVISIBLE_RANGES drops
// the same set.
func agentInvisibleRune(r rune) bool {
	switch {
	case r == 0x034F, r == 0x115F, r == 0x1160, r == 0x2065, r == 0x3164, r == 0xFFA0:
		return true
	case r >= 0x17B4 && r <= 0x17B5, r >= 0x180B && r <= 0x180F, r >= 0xFE00 && r <= 0xFE0F, r >= 0xFFF0 && r <= 0xFFF8, r >= 0xE0000 && r <= 0xE0FFF:
		return true
	}
	return false
}

// agentComparator renders an SLA rule comparator: one of the known ones as
// written, anything else reduced like a name.
func agentComparator(comparator string) string {
	switch comparator = strings.TrimSpace(comparator); comparator {
	case "<", "<=", ">", ">=", "==", "!=", "=":
		return comparator
	}
	return agentSafeName(comparator)
}
