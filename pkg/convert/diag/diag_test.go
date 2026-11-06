package diag

import (
	"strings"
	"testing"
)

func TestReviewSectionsWordInfoAsNotes(t *testing.T) {
	sections := Diagnostics{
		{Severity: Info, Element: "a", Detail: "mapped"},
		{Severity: Warning, Element: "b", Detail: "check"},
	}.ReviewSections()
	if len(sections) != 2 {
		t.Fatalf("sections = %+v", sections)
	}
	for _, section := range sections {
		switch section.Severity {
		case string(Info):
			if strings.Contains(section.Summary, "require review") || !strings.Contains(section.Summary, "note") {
				t.Fatalf("info summary %q must read as a note", section.Summary)
			}
			// Shared by every importer and shown beside Info items that may
			// ask for validation before replay (cURL basic auth, JSR223), so
			// it must not tell the reader nothing is needed.
			if lower := strings.ToLower(section.Summary); strings.Contains(lower, "no action") || strings.Contains(lower, "nothing") {
				t.Fatalf("info summary %q must not claim no action is needed", section.Summary)
			}
		case string(Warning):
			if section.Summary != "1 import diagnostic(s) require review." {
				t.Fatalf("warning summary = %q", section.Summary)
			}
		}
	}
}

func TestCollectorReportedSinceIncludesDuplicates(t *testing.T) {
	var c Collector
	c.Add(Warning, "Header", "loc", "same")
	mark := c.Mark()
	c.Add(Warning, "Header", "loc", "same")
	c.Add(Info, "Other", "loc", "new")
	c.Add(Info, "Other", "loc", "new")
	if len(c.Items()) != 2 {
		t.Fatalf("items = %+v", c.Items())
	}
	got := c.ReportedSince(mark)
	if len(got) != 2 || got[0].Detail != "same" || got[1].Detail != "new" {
		t.Fatalf("reported since mark = %+v", got)
	}
	if c.ReportedSince(c.Mark()) != nil {
		t.Fatal("nothing was reported after the last mark")
	}
}
