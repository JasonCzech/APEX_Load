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
	// Lightning marks an element on a Salesforce Lightning page (the Aura
	// runtime is loaded), which keeps hidden copies of earlier pages in the
	// document and gives template ids generated suffixes.
	Lightning bool `json:"lightning,omitempty"`
}

// maxTextSelectorLength bounds text-locator candidates: longer visible text is
// content, not a label, and breaks on copy changes.
const maxTextSelectorLength = 80

// safeCSSIdentifier matches values usable directly after "#" without escaping.
var safeCSSIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// lwcGeneratedID matches an id Lightning Web Components made globally unique
// at render by appending a number (input-237, combobox-id-12).
var lwcGeneratedID = regexp.MustCompile(`-\d+$`)

// visibleSuffix restricts a CSS or XPath selector to visible elements.
const visibleSuffix = " >> visible=true"

// visibleOnly makes CSS and XPath candidates match visible elements only, so a
// hidden copy of an earlier page (Lightning keeps them in the document) can
// neither fail a strict match nor take the click. Role locators skip hidden
// elements already.
func visibleOnly(candidates []Candidate) []Candidate {
	for i := range candidates {
		switch candidates[i].Kind {
		case "css":
			candidates[i].Value += visibleSuffix
		case "xpath":
			candidates[i].Value = NormalizeXPath(candidates[i].Value) + visibleSuffix
		}
	}
	return candidates
}

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
	lightningElement := isLightningElement(meta) || meta.Lightning

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
		} else if lightningElement && lwcGeneratedID.MatchString(value) {
			confidence = ConfidenceVolatileAuraID
			reason = "Lightning Web Components id, which gets a generated numeric suffix at render; ranked below role/text fallbacks because it changes between page loads."
		}
		out = append(out, demoteIfRecordSpecific(Candidate{
			Kind:       "css",
			Value:      idSelector,
			Confidence: confidence,
			Reason:     reason,
		}, RecordSpecificID(value)))
	}
	if value := strings.TrimSpace(meta.AriaLabel); value != "" {
		confidence := ConfidenceAriaLabel
		reason := "ARIA label captured at recording time."
		if lightningElement {
			confidence = ConfidenceLightningAriaLabel
			reason = "Lightning component ARIA label captured at recording time."
		}
		out = append(out, demoteIfRecordSpecific(Candidate{
			Kind:       "css",
			Value:      tagPrefixed(tag, CSSAttributeSelector("aria-label", value)),
			Confidence: confidence,
			Reason:     reason,
		}, RecordSpecificText(value)))
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
			out = append(out, demoteIfRecordSpecific(Candidate{
				Kind:       "css",
				Value:      tagPrefixed(tag, CSSAttributeSelector("title", value)),
				Confidence: ConfidenceLightningTitle,
				Reason:     "Lightning component title captured at recording time.",
			}, RecordSpecificText(value)))
		}
	}
	if value := strings.TrimSpace(meta.Name); value != "" {
		confidence := ConfidenceNameAttr
		reason := "Form control name attribute captured at recording time."
		if lightningElement && (tag == "button" || tag == "lightning-button" || strings.EqualFold(strings.TrimSpace(meta.Role), "button")) {
			confidence = ConfidenceLightningButtonName
			reason = "Lightning button name attribute, which Salesforce's standard actions keep across releases."
		}
		out = append(out, Candidate{
			Kind:       "css",
			Value:      tagPrefixed(tag, CSSAttributeSelector("name", value)),
			Confidence: confidence,
			Reason:     reason,
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
			out = append(out, demoteIfRecordSpecific(Candidate{
				Kind:       "role",
				Value:      RoleSelector(role, accessibleName),
				Confidence: confidence,
				Reason:     reason,
			}, RecordSpecificText(accessibleName)))
		}
	}
	if value := strings.TrimSpace(meta.Placeholder); value != "" {
		// A placeholder describes the expected input ("e.g. 0412345678"),
		// never the record open, so its digits are no reason to demote it.
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
		out = append(out, demoteIfRecordSpecific(Candidate{
			Kind:       "text",
			Value:      TextSelector(value),
			Confidence: ConfidenceText,
			Reason:     "Visible text captured at recording time; verify uniqueness on replay.",
		}, RecordSpecificText(value)))
	}
	if value := strings.TrimSpace(meta.XPath); value != "" && !meta.InShadowDOM {
		out = append(out, Candidate{
			Kind:       "xpath",
			Value:      value,
			Confidence: ConfidenceXPath,
			Reason:     "Computed XPath captured at recording time; validate stability on replay.",
		})
	}
	out = Unique(out)
	if meta.Lightning {
		out = visibleOnly(out)
	}
	return out
}

// selectorTextToken splits text into the runs of letters, digits and hyphens
// a record identifier is written with.
var selectorTextToken = regexp.MustCompile(`[A-Za-z0-9-]+`)

var (
	// autoNumber is a Salesforce-style auto-number: I-0463595045, CASE-000123.
	// Six or more digits: a short code such as ISO-27001 or sku-12345 names
	// a fixed thing, not a record.
	autoNumber = regexp.MustCompile(`^[A-Za-z]{1,6}-[0-9]{6,}$`)
	// phoneCue marks text that labels a phone number.
	phoneCue = regexp.MustCompile(`(?i)\b(?:call|phone|tel|telephone|fax|toll[- ]free|mobile|sms|whatsapp)\b|\+[0-9]`)
	// longNumber is an order, account or ticket number, or a timestamp.
	longNumber = regexp.MustCompile(`^[0-9]{8,}$`)
	uuidToken  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// salesforceID is a 15- or 18-character record id (0015e00000AbCdE),
	// whose middle holds a run of zeros.
	salesforceID = regexp.MustCompile(`^[a-zA-Z0-9]{15}(?:[a-zA-Z0-9]{3})?$`)
)

// RecordSpecificText reports text that names one record rather than a kind of
// element, such as the tab label "Subtabs for I-0463595045 | Interaction".
func RecordSpecificText(text string) bool {
	// A phone number in a label ("Call 18005551234") is the same every run.
	phone := phoneCue.MatchString(text)
	for _, token := range selectorTextToken.FindAllString(text, -1) {
		token = strings.Trim(token, "-")
		if phone && longNumber.MatchString(strings.TrimLeft(token, "+")) {
			continue
		}
		if uuidToken.MatchString(token) || recordSpecificWord(token) {
			return true
		}
		// An id can join a record number to a prefix: tab-I-0463595045,
		// row-CASE-000123. Check each part, and each part with the next.
		parts := strings.Split(token, "-")
		for index, part := range parts {
			if recordSpecificWord(part) || (index+1 < len(parts) && autoNumber.MatchString(part+"-"+parts[index+1])) {
				return true
			}
		}
	}
	return false
}

func recordSpecificWord(word string) bool {
	switch {
	case autoNumber.MatchString(word), longNumber.MatchString(word):
		return true
	case salesforceRecordID(word):
		// Ids of metadata (custom fields 00N, record types 012, layouts
		// 00h, profiles 00e) are the same in every run of an org.
		return true
	}
	return false
}

func salesforceMetadataIDPrefix(id string) bool {
	for _, prefix := range []string{"00N", "012", "00h", "00e", "00D", "01I"} {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

// demoteIfRecordSpecific ranks a candidate below the positional ones when
// the text it was built from names one record. Only human-visible text (an
// ARIA label, title, role name or visible text) and ids are
// checked: a form control's name, a test hook or an Aura class is fixed by
// the page, whatever digits it holds (entry.1234567890, sku-12345).
func demoteIfRecordSpecific(candidate Candidate, recordSpecific bool) Candidate {
	if !recordSpecific || candidate.Confidence <= ConfidenceRecordSpecific {
		return candidate
	}
	candidate.Confidence = ConfidenceRecordSpecific
	candidate.Reason = "Names one record (" + strings.TrimSuffix(candidate.Reason, ".") + "); the next run opens or creates another record, so this will not match it."
	return candidate
}

// recordAutoNumberID is an auto-number inside an id, zero-padded to six or
// more digits (tab-I-0463595045, row-CASE-000123); a short code such as
// zip-90210 or sku-12345 is a fixed field.
var recordAutoNumberID = regexp.MustCompile(`^[A-Za-z]{1,6}-[0-9]{6,}$`)

// uuidInText finds a UUID anywhere in an id (row-3f2504e0-4f89-...).
var uuidInText = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// RecordSpecificID reports an element id that embeds a record identifier: a
// UUID, a Salesforce record id, or a record auto-number. A bare number in an
// id (field_20240101) is a fixed field id.
func RecordSpecificID(id string) bool {
	if uuidInText.MatchString(id) {
		return true
	}
	for _, token := range selectorTextToken.FindAllString(id, -1) {
		token = strings.Trim(token, "-")
		if uuidToken.MatchString(token) || recordAutoNumberID.MatchString(token) || salesforceRecordID(token) {
			return true
		}
		parts := strings.Split(token, "-")
		for index, part := range parts {
			if salesforceRecordID(part) || (index+1 < len(parts) && recordAutoNumberID.MatchString(part+"-"+parts[index+1])) {
				return true
			}
		}
	}
	return false
}

func salesforceRecordID(word string) bool {
	return salesforceID.MatchString(word) && strings.Contains(word, "000") &&
		strings.ContainsAny(word, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") && !salesforceMetadataIDPrefix(word)
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
