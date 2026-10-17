package importreview

import (
	"github.com/jasonczech/apexload/pkg/convert/diag"
)

func ImportDiagnosticReviewSections(items diag.Diagnostics) []ScriptImportReviewSection {
	sections := items.ReviewSections()
	if len(sections) == 0 {
		return nil
	}
	out := make([]ScriptImportReviewSection, 0, len(sections))
	for _, section := range sections {
		out = append(out, ScriptImportReviewSection{
			Key:      section.Key,
			Title:    section.Title,
			Severity: section.Severity,
			Summary:  section.Summary,
			Items:    section.Items,
		})
	}
	return out
}

func AppendImportDiagnosticReviewSections(base []ScriptImportReviewSection, items diag.Diagnostics) []ScriptImportReviewSection {
	return append(base, ImportDiagnosticReviewSections(items)...)
}
