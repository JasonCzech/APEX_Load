package selector

import (
	"regexp"
	"strings"
)

// ElementMetadata is the structured description of a live DOM element reported by
// a capture surface (the embedded recorder's injected script or an imported
// Chrome DevTools Recorder step). Candidates derives ranked selectors from it.
type ElementMetadata struct {
	Tag         string            `json:"tag,omitempty"`
	ID          string            `json:"id,omitempty"`
	Name        string            `json:"name,omitempty"`
	Type        string            `json:"type,omitempty"`
	Role        string            `json:"role,omitempty"`
	AriaLabel   string            `json:"aria_label,omitempty"`
	DataTestID  string            `json:"data_test_id,omitempty"`
	Placeholder string            `json:"placeholder,omitempty"`
	Text        string            `json:"text,omitempty"`
	Classes     []string          `json:"classes,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	CSSPath     string            `json:"css_path,omitempty"`
	XPath       string            `json:"xpath,omitempty"`
	InShadowDOM bool              `json:"in_shadow_dom,omitempty"`
	FramePath   []string          `json:"frame_path,omitempty"`
}

// maxTextSelectorLength bounds text-locator candidates: longer visible text is
// content, not a label, and breaks on copy changes.
const maxTextSelectorLength = 80

// safeCSSIdentifier matches values usable directly after "#" without escaping.
var safeCSSIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// dataTestAttributes lists the test-hook attribute names recognized across
// frameworks, in preference order.
var dataTestAttributes = []string{"data-testid", "data-test-id", "data-test", "data-cy", "data-qa"}

// Candidates derives ranked selector candidates from captured element metadata
// using the shared confidence ladder. XPath candidates are suppressed for
// elements inside open shadow roots because the XPath engine cannot pierce
// shadow boundaries (Playwright's CSS engine can).
func Candidates(meta ElementMetadata) []Candidate {
	out := make([]Candidate, 0, 8)
	tag := strings.ToLower(strings.TrimSpace(meta.Tag))
	lightningElement := isLightningElement(meta)

	if value := strings.TrimSpace(meta.DataTestID); value != "" {
		out = append(out, Candidate{
			Kind:       "css",
			Value:      CSSAttributeSelector(dataTestAttributeName(meta), value),
			Confidence: ConfidenceDataTestID,
			Reason:     "Dedicated test-hook attribute captured at recording time.",
		})
	}
	if value := strings.TrimSpace(meta.ID); value != "" && !strings.ContainsAny(value, " \t\r\n") {
		idSelector := "#" + value
		if !safeCSSIdentifier.MatchString(value) {
			idSelector = CSSAttributeSelector("id", value)
		}
		confidence := ConfidenceHTMLID
		reason := "HTML id attribute captured at recording time."
		if isVolatileAuraID(value) {
			confidence = ConfidenceVolatileAuraID
			reason = "Aura-generated id captured at recording time; ranked below role/text fallbacks because it may drift."
		}
		out = append(out, Candidate{
			Kind:       "css",
			Value:      idSelector,
			Confidence: confidence,
			Reason:     reason,
		})
	}
	if value := strings.TrimSpace(meta.AriaLabel); value != "" {
		confidence := ConfidenceAriaLabel
		reason := "ARIA label captured at recording time."
		if lightningElement {
			confidence = ConfidenceLightningAriaLabel
			reason = "Lightning component ARIA label captured at recording time."
		}
		out = append(out, Candidate{
			Kind:       "css",
			Value:      tagPrefixed(tag, CSSAttributeSelector("aria-label", value)),
			Confidence: confidence,
			Reason:     reason,
		})
	}
	if lightningElement {
		if value := strings.TrimSpace(meta.Attributes["data-aura-class"]); value != "" {
			out = append(out, Candidate{
				Kind:       "css",
				Value:      tagPrefixed(tag, CSSAttributeSelector("data-aura-class", value)),
				Confidence: ConfidenceLightningAuraClass,
				Reason:     "Lightning Aura class hint captured at recording time.",
			})
		}
		if value := strings.TrimSpace(meta.Attributes["title"]); value != "" {
			out = append(out, Candidate{
				Kind:       "css",
				Value:      tagPrefixed(tag, CSSAttributeSelector("title", value)),
				Confidence: ConfidenceLightningTitle,
				Reason:     "Lightning component title captured at recording time.",
			})
		}
	}
	if value := strings.TrimSpace(meta.Name); value != "" {
		out = append(out, Candidate{
			Kind:       "css",
			Value:      tagPrefixed(tag, CSSAttributeSelector("name", value)),
			Confidence: ConfidenceNameAttr,
			Reason:     "Form control name attribute captured at recording time.",
		})
	}
	if role := strings.TrimSpace(meta.Role); role != "" {
		if accessibleName := firstNonEmpty(meta.AriaLabel, shortText(meta.Text)); accessibleName != "" {
			confidence := ConfidenceRoleName
			reason := "Accessible role and name captured at recording time."
			if lightningElement {
				confidence = ConfidenceLightningRoleName
				reason = "Lightning accessible role and name captured at recording time."
			}
			out = append(out, Candidate{
				Kind:       "role",
				Value:      RoleSelector(role, accessibleName),
				Confidence: confidence,
				Reason:     reason,
			})
		}
	}
	if value := strings.TrimSpace(meta.Placeholder); value != "" {
		out = append(out, Candidate{
			Kind:       "css",
			Value:      tagPrefixed(tag, CSSAttributeSelector("placeholder", value)),
			Confidence: ConfidencePlaceholder,
			Reason:     "Input placeholder captured at recording time.",
		})
	}
	if value := strings.TrimSpace(meta.CSSPath); value != "" {
		reason := "Computed CSS path captured at recording time; may drift as page structure changes."
		if meta.InShadowDOM {
			reason = "Computed CSS path piercing open shadow roots; may drift as page structure changes."
		}
		out = append(out, Candidate{
			Kind:       "css",
			Value:      value,
			Confidence: ConfidenceCSSPath,
			Reason:     reason,
		})
	}
	if value := shortText(meta.Text); value != "" {
		out = append(out, Candidate{
			Kind:       "text",
			Value:      TextSelector(value),
			Confidence: ConfidenceText,
			Reason:     "Visible text captured at recording time; verify uniqueness on replay.",
		})
	}
	if value := strings.TrimSpace(meta.XPath); value != "" && !meta.InShadowDOM {
		out = append(out, Candidate{
			Kind:       "xpath",
			Value:      value,
			Confidence: ConfidenceXPath,
			Reason:     "Computed XPath captured at recording time; validate stability on replay.",
		})
	}
	return Unique(out)
}

func dataTestAttributeName(meta ElementMetadata) string {
	for _, name := range dataTestAttributes {
		if strings.TrimSpace(meta.Attributes[name]) == strings.TrimSpace(meta.DataTestID) && meta.Attributes[name] != "" {
			return name
		}
	}
	return dataTestAttributes[0]
}

func tagPrefixed(tag string, attrSelector string) string {
	if tag == "" {
		return attrSelector
	}
	return tag + attrSelector
}

func shortText(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" || len(text) > maxTextSelectorLength {
		return ""
	}
	return text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func isLightningElement(meta ElementMetadata) bool {
	tag := strings.ToLower(strings.TrimSpace(meta.Tag))
	if strings.HasPrefix(tag, "lightning-") || strings.HasPrefix(tag, "force-") || strings.HasPrefix(tag, "c-") {
		return true
	}
	for key := range meta.Attributes {
		lower := strings.ToLower(strings.TrimSpace(key))
		if strings.HasPrefix(lower, "data-aura-") {
			return true
		}
	}
	return false
}

func isVolatileAuraID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.Contains(value, ":") {
		return true
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
