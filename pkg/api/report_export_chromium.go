package api

import (
	"fmt"
	"html"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol/browser"
)

// exportPDFClass is added to <html> for the PDF export so the print stylesheet can
// keep the contents list (hidden when a person prints the on-screen report) and
// drop screen-only chrome.
const exportPDFClass = "export-pdf"

// reportPDFChromiumRenderer is a seam so tests that assert on the built-in text
// PDF can disable the Chromium path deterministically.
var reportPDFChromiumRenderer = renderRunArchiveReportPDFViaChromium

// renderRunArchiveReportPDFViaChromium prints the HTML report — the same document
// the HTML export produces — to a paginated PDF. It returns an error when headless
// Chromium is unavailable so callers can fall back to the built-in text renderer.
func renderRunArchiveReportPDFViaChromium(report *RunArchiveReport, profileID string, annotations *ReportAnnotations, manifest *TestRunManifest) ([]byte, error) {
	profile, ok := lookupRunArchiveReportProfile(profileID)
	if !ok {
		return nil, fmt.Errorf("unknown report profile %q", profileID)
	}
	body, err := renderRunArchiveHTMLReportWithAnnotations(report, profile.ID, annotations, manifest)
	if err != nil {
		return nil, err
	}
	doc := strings.Replace(string(body), "<html", "<html class=\""+exportPDFClass+"\"", 1)
	const style = `font-family:Helvetica,Arial,sans-serif;font-size:8px;color:#5b6875;width:100%;padding:0 14mm;`
	footer := `<div style="` + style + `display:flex;justify-content:space-between;">` +
		`<span>APEX Load · ` + html.EscapeString(profile.Label) + ` Report</span>` +
		`<span>Page <span class="pageNumber"></span> of <span class="totalPages"></span></span></div>`
	return browser.RenderHTMLToPDF(doc, browser.HTMLPDFOptions{
		Format:         "Letter",
		HeaderTemplate: "<span></span>",
		FooterTemplate: footer,
	})
}
