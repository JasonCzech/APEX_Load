package browser

import (
	"fmt"
	"strings"

	playwright "github.com/mxschmitt/playwright-go"
)

// HTMLPDFOptions controls RenderHTMLToPDF.
type HTMLPDFOptions struct {
	// Format is a paper size such as "Letter" or "A4". Defaults to "Letter".
	Format string
	// HeaderTemplate / FooterTemplate use Chromium's print-template markup
	// (classes pageNumber, totalPages, title, date). Empty disables them.
	HeaderTemplate string
	FooterTemplate string
}

// RenderHTMLToPDF prints a self-contained HTML document to PDF with headless
// Chromium, so the PDF export is the same document as the HTML report with
// print pagination. JavaScript is disabled and every non-inline request is
// aborted: the input is rendered, never executed or allowed to reach the network.
func RenderHTMLToPDF(html string, opts HTMLPDFOptions) ([]byte, error) {
	activePoolOperations.Add(1)
	defer activePoolOperations.Add(-1)
	browser, err := getBrowserInstance("chromium", true)
	if err != nil {
		return nil, fmt.Errorf("start chromium: %w", err)
	}
	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		JavaScriptEnabled: playwright.Bool(false),
	})
	if err != nil {
		return nil, fmt.Errorf("new browser context: %w", err)
	}
	defer ctx.Close()
	if err := ctx.Route("**/*", func(route playwright.Route) {
		if u := route.Request().URL(); strings.HasPrefix(u, "data:") || u == "about:blank" {
			_ = route.Continue()
			return
		}
		_ = route.Abort()
	}); err != nil {
		return nil, fmt.Errorf("restrict report network access: %w", err)
	}
	page, err := ctx.NewPage()
	if err != nil {
		return nil, fmt.Errorf("new page: %w", err)
	}
	if err := page.SetContent(html, playwright.PageSetContentOptions{
		WaitUntil: playwright.WaitUntilStateLoad,
		Timeout:   playwright.Float(30000),
	}); err != nil {
		return nil, fmt.Errorf("load report html: %w", err)
	}
	format := opts.Format
	if format == "" {
		format = "Letter"
	}
	pdf := playwright.PagePdfOptions{
		Format:          playwright.String(format),
		PrintBackground: playwright.Bool(true),
		Outline:         playwright.Bool(true),
		Tagged:          playwright.Bool(true),
		Margin: &playwright.Margin{
			Top:    playwright.String("18mm"),
			Bottom: playwright.String("18mm"),
			Left:   playwright.String("14mm"),
			Right:  playwright.String("14mm"),
		},
	}
	if opts.HeaderTemplate != "" || opts.FooterTemplate != "" {
		pdf.DisplayHeaderFooter = playwright.Bool(true)
		pdf.HeaderTemplate = playwright.String(opts.HeaderTemplate)
		pdf.FooterTemplate = playwright.String(opts.FooterTemplate)
	}
	out, err := page.PDF(pdf)
	if err != nil {
		return nil, fmt.Errorf("print report pdf: %w", err)
	}
	return out, nil
}
