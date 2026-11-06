package diag

import (
	"fmt"
	"sort"
	"strings"
)

type Severity string

const (
	Info    Severity = "info"
	Warning Severity = "warning"
	Blocker Severity = "blocker"
)

type Diagnostic struct {
	Code      string `json:"code,omitempty"`
	SourceRef string `json:"source_ref,omitempty"`
	Element   string `json:"element"`
	Detail    string `json:"detail"`
	// Resolution, when set, is what clears the finding, kept apart from
	// Detail (what it means) so a client can show the two separately.
	Resolution         string   `json:"resolution,omitempty"`
	Severity           Severity `json:"severity"`
	Location           string   `json:"location,omitempty"`
	Count              int      `json:"count,omitempty"`
	Locations          []string `json:"locations,omitempty"`
	LocationsTruncated bool     `json:"locations_truncated,omitempty"`
}

type Diagnostics []Diagnostic

type ReviewSection struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Severity string   `json:"severity"`
	Summary  string   `json:"summary"`
	Items    []string `json:"items"`
}

type Collector struct {
	items Diagnostics
	seen  map[string]int
	// reported holds the item index of every Add call, duplicates included,
	// so a caller can attribute a de-duplicated diagnostic to each scope that
	// reported it (see Mark and ReportedSince).
	reported []int
}

func (c *Collector) Add(sev Severity, element, location, detail string) {
	c.AddCoded(sev, "", element, location, detail)
}

// AddCoded records a diagnostic with a stable machine-readable code.
func (c *Collector) AddCoded(sev Severity, code, element, location, detail string) {
	element = strings.TrimSpace(element)
	detail = strings.TrimSpace(detail)
	location = strings.TrimSpace(location)
	if element == "" && detail == "" {
		return
	}
	if sev == "" {
		sev = Warning
	}
	item := Diagnostic{
		Code:     strings.TrimSpace(code),
		Element:  element,
		Detail:   detail,
		Severity: sev,
		Location: location,
	}
	key := strings.Join([]string{string(item.Severity), item.Code, item.Element, item.Location, item.Detail}, "\x00")
	if c.seen == nil {
		c.seen = map[string]int{}
	}
	if index, ok := c.seen[key]; ok {
		c.reported = append(c.reported, index)
		return
	}
	c.seen[key] = len(c.items)
	c.reported = append(c.reported, len(c.items))
	c.items = append(c.items, item)
}

// Mark returns a position for ReportedSince.
func (c *Collector) Mark() int {
	if c == nil {
		return 0
	}
	return len(c.reported)
}

// ReportedSince returns, once each and in first-report order, the
// diagnostics reported since mark, including ones an earlier scope had
// already reported and Items lists only once.
func (c *Collector) ReportedSince(mark int) Diagnostics {
	if c == nil || mark >= len(c.reported) {
		return nil
	}
	seen := make(map[int]bool, len(c.reported)-mark)
	var out Diagnostics
	for _, index := range c.reported[mark:] {
		if !seen[index] {
			seen[index] = true
			out = append(out, c.items[index])
		}
	}
	return out
}

func (c *Collector) Items() Diagnostics {
	if c == nil || len(c.items) == 0 {
		return nil
	}
	out := make(Diagnostics, len(c.items))
	copy(out, c.items)
	return out
}

func (d Diagnostic) String() string {
	element := strings.TrimSpace(d.Element)
	if element == "" {
		element = "import element"
	}
	prefix := element
	if d.Count > 1 {
		prefix += fmt.Sprintf(" (%d occurrences", d.Count)
		if len(d.Locations) > 0 {
			prefix += "; examples: " + strings.Join(d.Locations, ", ")
		}
		prefix += ")"
	} else if d.Location != "" {
		prefix += " at " + d.Location
	}
	// Text surfaces (preflight issues, review sections) keep the resolution
	// after the meaning, as one sentence pair.
	detail := strings.TrimSpace(strings.Join([]string{d.Detail, d.Resolution}, " "))
	if detail == "" {
		return fmt.Sprintf("%s [%s]", prefix, d.Severity)
	}
	return fmt.Sprintf("%s [%s]: %s", prefix, d.Severity, detail)
}

func (d Diagnostics) Strings() []string {
	if len(d) == 0 {
		return nil
	}
	out := make([]string, 0, len(d))
	for _, item := range d {
		out = append(out, item.String())
	}
	return out
}

func (d Diagnostics) ReviewSections() []ReviewSection {
	if len(d) == 0 {
		return nil
	}
	groups := map[Severity][]string{}
	for _, item := range d {
		sev := item.Severity
		if sev == "" {
			sev = Warning
		}
		groups[sev] = append(groups[sev], item.String())
	}
	order := []Severity{Blocker, Warning, Info}
	sections := make([]ReviewSection, 0, len(groups))
	for _, sev := range order {
		items := groups[sev]
		if len(items) == 0 {
			continue
		}
		sections = append(sections, ReviewSection{
			Key:      "import_diagnostics_" + string(sev),
			Title:    severityTitle(sev),
			Severity: string(sev),
			Summary:  reviewSectionSummary(sev, len(items)),
			Items:    items,
		})
	}
	return sections
}

func (d Diagnostics) HasLocation(location string) bool {
	location = strings.TrimSpace(location)
	for _, item := range d {
		if strings.EqualFold(strings.TrimSpace(item.Location), location) {
			return true
		}
	}
	return false
}

// reviewSectionSummary words a section by what it asks of the reader. Info
// items are notes about how the source was mapped; saying they "require
// review" made a clean import read as if something were left to do. The
// wording stays neutral, though: this summary is shared by every importer and
// is shown beside each Info item, and some Info items do ask the operator to
// validate something before replay, so it must not claim nothing is needed.
func reviewSectionSummary(sev Severity, count int) string {
	if sev == Info {
		return fmt.Sprintf("%d import note(s) about how the source was mapped.", count)
	}
	return fmt.Sprintf("%d import diagnostic(s) require review.", count)
}

func severityTitle(sev Severity) string {
	switch sev {
	case Blocker:
		return "Blocking Import Diagnostics"
	case Info:
		return "Import Assumptions"
	default:
		return "Import Diagnostics"
	}
}

func MergeStrings(values ...[]string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, group := range values {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}
