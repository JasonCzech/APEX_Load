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
	Element  string   `json:"element"`
	Detail   string   `json:"detail"`
	Severity Severity `json:"severity"`
	Location string   `json:"location,omitempty"`
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
	seen  map[string]struct{}
}

func (c *Collector) Add(sev Severity, element, location, detail string) {
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
		Element:  element,
		Detail:   detail,
		Severity: sev,
		Location: location,
	}
	key := strings.Join([]string{string(item.Severity), item.Element, item.Location, item.Detail}, "\x00")
	if c.seen == nil {
		c.seen = map[string]struct{}{}
	}
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = struct{}{}
	c.items = append(c.items, item)
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
	if d.Location != "" {
		prefix += " at " + d.Location
	}
	if d.Detail == "" {
		return fmt.Sprintf("%s [%s]", prefix, d.Severity)
	}
	return fmt.Sprintf("%s [%s]: %s", prefix, d.Severity, d.Detail)
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
			Summary:  fmt.Sprintf("%d import diagnostic(s) require review.", len(items)),
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
