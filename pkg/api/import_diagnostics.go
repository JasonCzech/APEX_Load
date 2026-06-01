package api

import "github.com/jasonczech/apexload/pkg/convert/diag"

func importDiagnosticReviewSections(items diag.Diagnostics) []scriptImportReviewSection {
	sections := items.ReviewSections()
	if len(sections) == 0 {
		return nil
	}
	out := make([]scriptImportReviewSection, 0, len(sections))
	for _, section := range sections {
		out = append(out, scriptImportReviewSection{
			Key:      section.Key,
			Title:    section.Title,
			Severity: section.Severity,
			Summary:  section.Summary,
			Items:    section.Items,
		})
	}
	return out
}

func appendImportDiagnosticReviewSections(base []scriptImportReviewSection, items diag.Diagnostics) []scriptImportReviewSection {
	return append(base, importDiagnosticReviewSections(items)...)
}
