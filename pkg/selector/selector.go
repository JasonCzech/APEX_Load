// Package selector provides the shared element-selector model used across APEX Load:
// ranked selector candidates with confidence scores and reasons. It serves three
// callers — design-time importers (TruClient archives, Chrome DevTools Recorder),
// the embedded browser recorder, and replay-time selector self-heal — so that a
// candidate produced by any capture surface is usable by any replay surface.
package selector

import (
	"fmt"
	"sort"
	"strings"
)

// Candidate is one ranked way to locate an element. Kind is the locator family
// ("css", "xpath", "role", "text"), Value is the locator payload, Confidence is a
// 0–100 stability score, and Reason explains the score for review UIs.
type Candidate struct {
	Kind       string `json:"kind"`
	Value      string `json:"value"`
	Confidence int    `json:"confidence"`
	Reason     string `json:"reason"`
}

// Confidence ladder shared by all capture surfaces. Values match the established
// TruClient object-repository ladder so candidates from different sources rank
// consistently when merged.
const (
	ConfidenceDataTestID  = 95
	ConfidenceHTMLID      = 90
	ConfidenceCapturedCSS = 88
	ConfidenceAriaLabel   = 82
	ConfidenceNameAttr    = 76
	ConfidenceRoleName    = 72
	ConfidencePlaceholder = 70
	ConfidenceCSSPath     = 68
	ConfidenceText        = 62
	ConfidenceXPath       = 58
)

// Unique trims, dedupes (by kind+value), and sorts candidates by confidence
// descending with value as the tiebreaker. The sort is stable so equal-confidence
// candidates keep their source order before the value tiebreak.
func Unique(candidates []Candidate) []Candidate {
	seen := make(map[string]struct{})
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Value = strings.TrimSpace(candidate.Value)
		if candidate.Value == "" {
			continue
		}
		key := strings.ToLower(candidate.Kind) + "\x00" + candidate.Value
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence == out[j].Confidence {
			return out[i].Value < out[j].Value
		}
		return out[i].Confidence > out[j].Confidence
	})
	return out
}

// CSSAttributeSelector builds an attribute-equals CSS selector with the value
// escaped for double-quoted CSS attribute syntax.
func CSSAttributeSelector(name string, value string) string {
	return fmt.Sprintf(`[%s="%s"]`, name, escapeCSSAttrValue(value))
}

func escapeCSSAttrValue(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(escaped, `"`, `\"`)
}

// PlaywrightSelector returns the highest-confidence candidate that is directly
// usable as an unambiguous Playwright selector string (css or xpath). Role and
// text candidates are not promoted automatically — they stay available for review
// and for replay fallback via Usable.
func PlaywrightSelector(candidates []Candidate) string {
	for _, candidate := range candidates {
		switch strings.ToLower(strings.TrimSpace(candidate.Kind)) {
		case "css":
			if value := strings.TrimSpace(candidate.Value); value != "" {
				return value
			}
		case "xpath":
			if value := NormalizeXPath(candidate.Value); value != "" {
				return value
			}
		}
	}
	return ""
}

// NormalizeXPath prefixes a bare XPath expression with the explicit Playwright
// "xpath=" engine prefix so it is never mistaken for a CSS selector.
func NormalizeXPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(value), "xpath=") {
		return value
	}
	return "xpath=" + value
}

// Usable converts a candidate into a Playwright-engine selector string when the
// candidate's kind has a deterministic engine form, returning false otherwise.
// Unlike PlaywrightSelector this also admits role and text candidates, which makes
// it suitable for replay-time fallback where every locator family is worth trying.
func Usable(candidate Candidate) (string, bool) {
	value := strings.TrimSpace(candidate.Value)
	if value == "" {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(candidate.Kind)) {
	case "css":
		return value, true
	case "xpath":
		return NormalizeXPath(value), true
	case "role":
		// Recorder-generated role candidates carry the full engine form
		// (role=button[name="Submit"]). Bare role names from older sources are
		// too ambiguous to replay automatically.
		if strings.HasPrefix(strings.ToLower(value), "role=") {
			return value, true
		}
		return "", false
	case "text":
		if strings.HasPrefix(strings.ToLower(value), "text=") {
			return value, true
		}
		return TextSelector(value), true
	default:
		return "", false
	}
}

// RoleSelector builds a Playwright role-engine selector from a role and
// accessible name, e.g. role=button[name="Submit"].
func RoleSelector(role string, name string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return ""
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return fmt.Sprintf(`role=%s[name="%s"]`, role, escapeCSSAttrValue(name))
}

// TextSelector builds a Playwright text-engine selector matching the exact
// trimmed text, e.g. text="Sign in".
func TextSelector(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return fmt.Sprintf(`text="%s"`, escapeCSSAttrValue(text))
}
