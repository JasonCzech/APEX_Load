package api

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// This file converts the rendered HTML report into WordprocessingML so the DOCX
// export is the same document as the HTML export (masthead, review summary,
// contents, KPI tiles, verdict banner, tables, findings, charts) in a standard
// paged layout, instead of a separate text projection that can drift from it.

const (
	docxContentWidth = 10080 // Letter, 0.75in margins, in twentieths of a point

	docxTeal  = "0F766E"
	docxNavy  = "1F2933"
	docxSlate = "5B6875"
	docxLine  = "D8CFC2"
	docxSilk  = "F7F4EE"
)

var docxWhitespace = regexp.MustCompile(`\s+`)

type docxRun struct {
	text  string
	br    bool
	props docxRunProps
	link  string // internal bookmark anchor
}

type docxRunProps struct {
	bold, italic, mono, caps bool
	color, shade             string
	size                     int // half-points
}

type docxParaProps struct {
	style          string
	align          string
	shade          string
	leftBar        string
	keepNext       bool
	before, after  int
	indent, hang   int
	pageBreakFirst bool
}

type docxConverter struct {
	charts     map[string]reportDocumentChart
	chartIndex map[string]int
	bookmarks  int
	title      string
}

// convertReportHTMLToDOCXBody returns the <w:body> children for the report HTML
// (without sectPr) and the document title.
func convertReportHTMLToDOCXBody(source []byte, charts []reportDocumentChart) ([]byte, string, error) {
	doc, err := html.Parse(bytes.NewReader(source))
	if err != nil {
		return nil, "", fmt.Errorf("parse report html: %w", err)
	}
	c := &docxConverter{charts: map[string]reportDocumentChart{}, chartIndex: map[string]int{}}
	for i, chart := range charts {
		c.charts[chart.Title] = chart
		c.chartIndex[chart.Title] = i
	}
	var out bytes.Buffer
	if body := findNode(doc, func(n *html.Node) bool { return isTag(n, "body") }); body != nil {
		c.flow(&out, body)
	}
	return out.Bytes(), c.title, nil
}

// ---- helpers ---------------------------------------------------------------

func isTag(n *html.Node, tags ...string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, tag := range tags {
		if n.Data == tag {
			return true
		}
	}
	return false
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func findNode(n *html.Node, match func(*html.Node) bool) *html.Node {
	if match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findNode(c, match); found != nil {
			return found
		}
	}
	return nil
}

func elementChildren(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		if isTag(x, "script", "style") {
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(docxWhitespace.ReplaceAllString(b.String(), " "))
}

func isInlineNode(n *html.Node) bool {
	if n.Type == html.TextNode {
		return true
	}
	if n.Type != html.ElementNode {
		return false
	}
	switch n.Data {
	case "a", "span", "b", "strong", "em", "i", "code", "br", "small", "sub", "sup", "u":
		return !hasClass(n, "verdict-badge")
	}
	return false
}

func docxXML(s string) string {
	var b bytes.Buffer
	writeDOCXEscaped(&b, sanitizeDOCXText(s))
	return b.String()
}

func docxBookmarkName(id string) string {
	return strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(id)
}

// ---- inline runs -----------------------------------------------------------

func pillColors(n *html.Node) (fg, bg string) {
	switch {
	case hasClass(n, "fail"), hasClass(n, "bad"):
		return "B42318", "FBE9E7"
	case hasClass(n, "warn"), hasClass(n, "conditional"):
		return "B45309", "FDF3DA"
	case hasClass(n, "pass"), hasClass(n, "good"):
		return "1A6E3C", "E6F4ED"
	}
	return "0C645D", "E8F5F2"
}

func (c *docxConverter) collectRuns(n *html.Node, props docxRunProps, preserve bool, link string, runs *[]docxRun) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		switch ch.Type {
		case html.TextNode:
			text := ch.Data
			if preserve {
				parts := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
				for i, part := range parts {
					if i > 0 {
						*runs = append(*runs, docxRun{br: true})
					}
					if part != "" {
						*runs = append(*runs, docxRun{text: part, props: props, link: link})
					}
				}
				continue
			}
			text = docxWhitespace.ReplaceAllString(text, " ")
			if text != "" {
				*runs = append(*runs, docxRun{text: text, props: props, link: link})
			}
		case html.ElementNode:
			next := props
			nextLink := link
			switch ch.Data {
			case "script", "style", "svg":
				continue
			case "br":
				*runs = append(*runs, docxRun{br: true})
				continue
			case "b", "strong":
				next.bold = true
			case "em", "i":
				next.italic = true
			case "code":
				next.mono = true
			case "a":
				if href := attr(ch, "href"); strings.HasPrefix(href, "#") && !strings.HasPrefix(href, "#/") {
					nextLink = docxBookmarkName(strings.TrimPrefix(href, "#"))
				}
			}
			if hasClass(ch, "mono") {
				next.mono = true
			}
			if hasClass(ch, "pill") || hasClass(ch, "outcome-pill") {
				next.bold = true
				next.color, next.shade = pillColors(ch)
			}
			c.collectRuns(ch, next, preserve, nextLink, runs)
		}
	}
}

func trimRuns(runs []docxRun) []docxRun {
	for len(runs) > 0 && !runs[0].br && strings.TrimSpace(runs[0].text) == "" {
		runs = runs[1:]
	}
	for len(runs) > 0 && !runs[len(runs)-1].br && strings.TrimSpace(runs[len(runs)-1].text) == "" {
		runs = runs[:len(runs)-1]
	}
	if len(runs) > 0 && !runs[0].br {
		runs[0].text = strings.TrimLeft(runs[0].text, " ")
	}
	if n := len(runs); n > 0 && !runs[n-1].br {
		runs[n-1].text = strings.TrimRight(runs[n-1].text, " ")
	}
	return runs
}

func docxRunPropsXML(p docxRunProps) string {
	var b strings.Builder
	if p.mono {
		b.WriteString(`<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:cs="Consolas"/>`)
	}
	if p.bold {
		b.WriteString(`<w:b/>`)
	}
	if p.italic {
		b.WriteString(`<w:i/>`)
	}
	if p.caps {
		b.WriteString(`<w:caps/>`)
	}
	if p.color != "" {
		b.WriteString(`<w:color w:val="` + p.color + `"/>`)
	}
	if p.size > 0 {
		b.WriteString(`<w:sz w:val="` + strconv.Itoa(p.size) + `"/>`)
	} else if p.mono {
		b.WriteString(`<w:sz w:val="18"/>`)
	}
	if p.shade != "" {
		b.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="` + p.shade + `"/>`)
	}
	if b.Len() == 0 {
		return ""
	}
	return `<w:rPr>` + b.String() + `</w:rPr>`
}

func docxRunsXML(runs []docxRun) string {
	var b strings.Builder
	for _, r := range runs {
		var inner string
		if r.br {
			inner = `<w:r><w:br/></w:r>`
		} else {
			inner = `<w:r>` + docxRunPropsXML(r.props) + `<w:t xml:space="preserve">` + docxXML(r.text) + `</w:t></w:r>`
		}
		if r.link != "" && !r.br {
			inner = `<w:hyperlink w:anchor="` + docxXML(r.link) + `" w:history="1">` + inner + `</w:hyperlink>`
		}
		b.WriteString(inner)
	}
	return b.String()
}

func docxParaPropsXML(p docxParaProps) string {
	var b strings.Builder
	if p.style != "" {
		b.WriteString(`<w:pStyle w:val="` + p.style + `"/>`)
	}
	if p.keepNext {
		b.WriteString(`<w:keepNext/>`)
	}
	if p.pageBreakFirst {
		b.WriteString(`<w:pageBreakBefore/>`)
	}
	if p.leftBar != "" {
		b.WriteString(`<w:pBdr><w:left w:val="single" w:sz="24" w:space="8" w:color="` + p.leftBar + `"/></w:pBdr>`)
	}
	if p.shade != "" {
		b.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="` + p.shade + `"/>`)
	}
	if p.before != 0 || p.after != 0 {
		b.WriteString(fmt.Sprintf(`<w:spacing w:before="%d" w:after="%d"/>`, p.before, p.after))
	}
	if p.indent != 0 || p.hang != 0 {
		b.WriteString(fmt.Sprintf(`<w:ind w:left="%d" w:hanging="%d"/>`, p.indent, p.hang))
	}
	if p.align != "" {
		b.WriteString(`<w:jc w:val="` + p.align + `"/>`)
	}
	if b.Len() == 0 {
		return ""
	}
	return `<w:pPr>` + b.String() + `</w:pPr>`
}

func (c *docxConverter) paragraph(out *bytes.Buffer, runs []docxRun, pp docxParaProps) {
	out.WriteString(`<w:p>` + docxParaPropsXML(pp) + docxRunsXML(runs) + `</w:p>`)
}

// inlineParagraph renders the inline content of n as one paragraph.
func (c *docxConverter) inlineParagraph(out *bytes.Buffer, n *html.Node, rp docxRunProps, pp docxParaProps, preserve bool) {
	var runs []docxRun
	c.collectRuns(n, rp, preserve, "", &runs)
	runs = trimRuns(runs)
	if len(runs) == 0 {
		return
	}
	c.paragraph(out, runs, pp)
}

func (c *docxConverter) spacer(out *bytes.Buffer) {
	out.WriteString(`<w:p><w:pPr><w:spacing w:before="0" w:after="0" w:line="120" w:lineRule="exact"/></w:pPr></w:p>`)
}

// ---- block flow ------------------------------------------------------------

// flow renders the children of n, gathering consecutive inline nodes into
// paragraphs and dispatching block elements.
func (c *docxConverter) flow(out *bytes.Buffer, n *html.Node) {
	var pending []*html.Node
	flush := func() {
		if len(pending) == 0 {
			return
		}
		holder := &html.Node{Type: html.ElementNode, Data: "span"}
		for _, p := range pending {
			clone := *p
			clone.Parent, clone.PrevSibling, clone.NextSibling = nil, nil, nil
			holder.AppendChild(&clone)
		}
		c.inlineParagraph(out, holder, docxRunProps{}, docxParaProps{after: 100}, false)
		pending = nil
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.CommentNode || ch.Type == html.DoctypeNode {
			continue
		}
		if hasClass(ch, "skip-link") || hasClass(ch, "back-link") {
			continue
		}
		if isInlineNode(ch) {
			pending = append(pending, ch)
			continue
		}
		flush()
		c.block(out, ch)
	}
	flush()
}

func (c *docxConverter) block(out *bytes.Buffer, n *html.Node) {
	if n.Type != html.ElementNode {
		return
	}
	switch {
	case isTag(n, "script", "style", "head", "title", "meta", "link", "svg"):
		return
	case hasClass(n, "skip-link"), hasClass(n, "back-link"), hasClass(n, "masthead"), hasClass(n, "header-rule"):
		return
	case hasClass(n, "page-footer"):
		c.inlineParagraph(out, n, docxRunProps{size: 16, color: docxSlate}, docxParaProps{before: 240, align: "center"}, false)
		return
	case hasClass(n, "report-contents"):
		c.contents(out, n)
		return
	case hasClass(n, "review-summary-status"):
		color := "1A6E3C"
		if hasClass(n, "fail") {
			color = "B42318"
		} else if hasClass(n, "conditional") || nodeText(n) == "Not assessed" {
			color = "B45309"
		}
		c.inlineParagraph(out, n, docxRunProps{bold: true, size: 30, color: color}, docxParaProps{after: 80}, false)
		return
	case hasClass(n, "eyebrow"):
		c.inlineParagraph(out, n, docxRunProps{bold: true, caps: true, size: 18, color: "0C645D"}, docxParaProps{after: 60}, false)
		return
	case isTag(n, "h1"):
		if c.title == "" {
			c.title = nodeText(n)
		}
		c.inlineParagraph(out, n, docxRunProps{}, docxParaProps{style: "Title"}, false)
		return
	case isTag(n, "h2"):
		c.inlineParagraph(out, n, docxRunProps{}, docxParaProps{style: "Heading1"}, false)
		return
	case isTag(n, "h3", "h4"):
		c.inlineParagraph(out, n, docxRunProps{}, docxParaProps{style: "Heading2", pageBreakFirst: hasClass(n, "baseline-reference")}, false)
		return
	case isTag(n, "table"):
		c.table(out, n)
		return
	case isTag(n, "ul", "ol"):
		c.list(out, n)
		return
	case isTag(n, "dl"):
		c.definitionList(out, n)
		return
	case isTag(n, "pre"):
		c.inlineParagraph(out, n, docxRunProps{mono: true}, docxParaProps{after: 100, shade: docxSilk}, true)
		return
	case hasClass(n, "verdict"):
		c.verdict(out, n)
		return
	case hasClass(n, "finding"):
		c.finding(out, n)
		return
	case hasClass(n, "integrity-banner"):
		fill, bar := "E6F4ED", "1A6E3C"
		if hasClass(n, "caution") {
			fill, bar = "FDF3DA", "B45309"
		} else if hasClass(n, "fail") {
			fill, bar = "FBE9E7", "B42318"
		}
		c.inlineParagraph(out, n, docxRunProps{color: bar}, docxParaProps{shade: fill, leftBar: bar, before: 60, after: 140}, false)
		return
	case hasClass(n, "meta-strip"), hasClass(n, "kpis"), hasClass(n, "review-summary-grid"), hasClass(n, "signoff"):
		cols := 4
		switch {
		case hasClass(n, "review-summary-grid"):
			cols = 3
		case hasClass(n, "signoff"):
			cols = 2
		}
		c.grid(out, n, cols)
		return
	case hasClass(n, "review-summary"):
		c.box(out, n, "FFFFFF", "0C645D", true)
		return
	case hasClass(n, "summary-note"):
		c.inlineParagraph(out, n, docxRunProps{}, docxParaProps{shade: docxSilk, leftBar: "0C645D", before: 80, after: 120}, false)
		return
	case hasClass(n, "charts"):
		c.chartsBlock(out, n)
		return
	case isTag(n, "p"):
		pp := docxParaProps{after: 120}
		rp := docxRunProps{}
		preserve := false
		if n.Parent != nil && hasClass(n.Parent, "analyst") {
			preserve = true
		}
		if hasClass(n, "muted") || hasClass(n, "section-copy") || hasClass(n, "subtitle") {
			rp.color = docxSlate
		}
		c.inlineParagraph(out, n, rp, pp, preserve)
		return
	case isTag(n, "li"), isTag(n, "dt"), isTag(n, "dd"):
		c.inlineParagraph(out, n, docxRunProps{}, docxParaProps{after: 60}, false)
		return
	case isTag(n, "section") && attr(n, "id") != "":
		c.bookmark(out, attr(n, "id"))
	}
	c.flow(out, n)
}

func (c *docxConverter) bookmark(out *bytes.Buffer, id string) {
	c.bookmarks++
	fmt.Fprintf(out, `<w:bookmarkStart w:id="%d" w:name="%s"/><w:bookmarkEnd w:id="%d"/>`, c.bookmarks, docxXML(docxBookmarkName(id)), c.bookmarks)
}

func (c *docxConverter) contents(out *bytes.Buffer, n *html.Node) {
	c.paragraph(out, []docxRun{{text: "Contents"}}, docxParaProps{style: "Heading1"})
	index := 0
	if ol := findNode(n, func(x *html.Node) bool { return isTag(x, "ol") }); ol != nil {
		for _, li := range elementChildren(ol) {
			index++
			var runs []docxRun
			runs = append(runs, docxRun{text: strconv.Itoa(index) + ".  "})
			c.collectRuns(li, docxRunProps{color: "0C645D"}, false, "", &runs)
			c.paragraph(out, trimRuns(runs), docxParaProps{after: 40, indent: 360, hang: 360})
		}
	}
	// The sections that do not apply to the run are named after the contents,
	// as in the HTML.
	if note := findNode(n, func(x *html.Node) bool { return hasClass(x, "not-captured") }); note != nil {
		c.inlineParagraph(out, note, docxRunProps{size: 18, color: docxSlate}, docxParaProps{before: 80, after: 40}, false)
	}
	c.spacer(out)
}

func (c *docxConverter) list(out *bytes.Buffer, n *html.Node) {
	ordered := isTag(n, "ol")
	i := 0
	for _, li := range elementChildren(n) {
		if !isTag(li, "li") {
			continue
		}
		i++
		marker := "•\t"
		if ordered {
			marker = strconv.Itoa(i) + ".\t"
		}
		var runs []docxRun
		runs = append(runs, docxRun{text: marker})
		c.collectRuns(li, docxRunProps{}, false, "", &runs)
		c.paragraph(out, runs, docxParaProps{after: 60, indent: 500, hang: 300})
	}
}

func (c *docxConverter) definitionList(out *bytes.Buffer, n *html.Node) {
	var rows [][2]*html.Node
	var term *html.Node
	for _, ch := range elementChildren(n) {
		switch ch.Data {
		case "dt":
			term = ch
		case "dd":
			rows = append(rows, [2]*html.Node{term, ch})
		}
	}
	if len(rows) == 0 {
		return
	}
	out.WriteString(`<w:tbl>` + docxTblPr(false) + `<w:tblGrid><w:gridCol w:w="2000"/><w:gridCol w:w="8080"/></w:tblGrid>`)
	for _, row := range rows {
		out.WriteString(`<w:tr><w:trPr><w:cantSplit/></w:trPr>`)
		var t bytes.Buffer
		if row[0] != nil {
			c.inlineParagraph(&t, row[0], docxRunProps{bold: true, caps: true, size: 16, color: docxSlate}, docxParaProps{after: 40}, false)
		}
		cellXML(out, 2000, "", t.Bytes())
		var d bytes.Buffer
		c.inlineParagraph(&d, row[1], docxRunProps{}, docxParaProps{after: 40}, true)
		cellXML(out, 8080, "", d.Bytes())
		out.WriteString(`</w:tr>`)
	}
	out.WriteString(`</w:tbl>`)
	c.spacer(out)
}

// ---- tables and boxes ------------------------------------------------------

func docxTblPr(borders bool) string {
	b := `<w:tblPr><w:tblW w:w="5000" w:type="pct"/><w:tblLayout w:type="fixed"/><w:tblCellMar><w:top w:w="60" w:type="dxa"/><w:left w:w="100" w:type="dxa"/><w:bottom w:w="60" w:type="dxa"/><w:right w:w="100" w:type="dxa"/></w:tblCellMar>`
	if borders {
		b = strings.Replace(b, `<w:tblLayout`, `<w:tblBorders><w:top w:val="single" w:sz="4" w:color="`+docxLine+`"/><w:left w:val="single" w:sz="4" w:color="`+docxLine+`"/><w:bottom w:val="single" w:sz="4" w:color="`+docxLine+`"/><w:right w:val="single" w:sz="4" w:color="`+docxLine+`"/><w:insideH w:val="single" w:sz="4" w:color="`+docxLine+`"/><w:insideV w:val="single" w:sz="4" w:color="`+docxLine+`"/></w:tblBorders><w:tblLayout`, 1)
	}
	return b + `</w:tblPr>`
}

func cellXML(out *bytes.Buffer, width int, fill string, content []byte) {
	out.WriteString(`<w:tc><w:tcPr><w:tcW w:w="` + strconv.Itoa(width) + `" w:type="dxa"/>`)
	if fill != "" {
		out.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="` + fill + `"/>`)
	}
	out.WriteString(`</w:tcPr>`)
	out.Write(content)
	if len(content) == 0 || bytes.HasSuffix(content, []byte(`</w:tbl>`)) {
		out.WriteString(`<w:p><w:pPr><w:spacing w:before="0" w:after="0"/></w:pPr></w:p>`)
	}
	out.WriteString(`</w:tc>`)
}

func (c *docxConverter) cellFlow(n *html.Node) []byte {
	var buf bytes.Buffer
	c.flow(&buf, n)
	return buf.Bytes()
}

// box renders n as a one-cell table (used for the review summary and similar
// bordered callouts).
func (c *docxConverter) box(out *bytes.Buffer, n *html.Node, fill, bar string, thick bool) {
	out.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/><w:tblLayout w:type="fixed"/><w:tblBorders><w:top w:val="single" w:sz="4" w:color="` + docxLine + `"/><w:left w:val="single" w:sz="36" w:color="` + bar + `"/><w:bottom w:val="single" w:sz="4" w:color="` + docxLine + `"/><w:right w:val="single" w:sz="4" w:color="` + docxLine + `"/></w:tblBorders><w:tblCellMar><w:top w:w="120" w:type="dxa"/><w:left w:w="200" w:type="dxa"/><w:bottom w:w="80" w:type="dxa"/><w:right w:w="200" w:type="dxa"/></w:tblCellMar></w:tblPr><w:tblGrid><w:gridCol w:w="` + strconv.Itoa(docxContentWidth) + `"/></w:tblGrid><w:tr><w:trPr><w:cantSplit/></w:trPr>`)
	cellXML(out, docxContentWidth, fill, c.cellFlow(n))
	out.WriteString(`</w:tr></w:tbl>`)
	c.spacer(out)
}

func (c *docxConverter) verdict(out *bytes.Buffer, n *html.Node) {
	fill, bar := "E6F4ED", "1A6E3C"
	for _, ch := range elementChildren(n) {
		if hasClass(ch, "verdict-badge") {
			switch {
			case hasClass(ch, "fail"):
				fill, bar = "FBE9E7", "B42318"
			case hasClass(ch, "conditional"):
				fill, bar = "FDF3DA", "B45309"
			}
		}
	}
	out.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/><w:tblLayout w:type="fixed"/><w:tblBorders><w:top w:val="single" w:sz="4" w:color="` + docxLine + `"/><w:left w:val="single" w:sz="4" w:color="` + docxLine + `"/><w:bottom w:val="single" w:sz="4" w:color="` + docxLine + `"/><w:right w:val="single" w:sz="4" w:color="` + docxLine + `"/></w:tblBorders><w:tblCellMar><w:top w:w="120" w:type="dxa"/><w:left w:w="160" w:type="dxa"/><w:bottom w:w="120" w:type="dxa"/><w:right w:w="160" w:type="dxa"/></w:tblCellMar></w:tblPr><w:tblGrid><w:gridCol w:w="2000"/><w:gridCol w:w="8080"/></w:tblGrid><w:tr><w:trPr><w:cantSplit/></w:trPr>`)
	for _, ch := range elementChildren(n) {
		var buf bytes.Buffer
		if hasClass(ch, "verdict-badge") {
			c.inlineParagraph(&buf, ch, docxRunProps{bold: true, caps: true, size: 20, color: "FFFFFF"}, docxParaProps{align: "center"}, false)
			cellXML(out, 2000, bar, buf.Bytes())
		} else {
			c.inlineParagraph(&buf, ch, docxRunProps{}, docxParaProps{}, false)
			cellXML(out, 8080, fill, buf.Bytes())
		}
	}
	out.WriteString(`</w:tr></w:tbl>`)
	c.spacer(out)
}

func (c *docxConverter) finding(out *bytes.Buffer, n *html.Node) {
	out.WriteString(`<w:tbl>` + docxTblPr(true) + `<w:tblGrid><w:gridCol w:w="` + strconv.Itoa(docxContentWidth) + `"/></w:tblGrid>`)
	for _, ch := range elementChildren(n) {
		fill := ""
		if hasClass(ch, "finding-head") {
			fill = docxSilk
			if hasClass(ch, "major") {
				fill = "FDF3DA"
			}
		}
		out.WriteString(`<w:tr><w:trPr><w:cantSplit/></w:trPr>`)
		var buf bytes.Buffer
		if hasClass(ch, "finding-head") {
			var runs []docxRun
			for _, part := range elementChildren(ch) {
				rp := docxRunProps{}
				switch {
				case hasClass(part, "fid"):
					rp = docxRunProps{mono: true, bold: true, color: "0C645D"}
				case hasClass(part, "ft"):
					rp = docxRunProps{bold: true, color: docxNavy}
				}
				if len(runs) > 0 {
					runs = append(runs, docxRun{text: "   "})
				}
				c.collectRuns(&html.Node{Type: html.ElementNode, Data: "span", FirstChild: part.FirstChild, LastChild: part.LastChild}, rp, false, "", &runs)
				if hasClass(part, "pill") {
					fg, bg := pillColors(part)
					runs[len(runs)-1].props = docxRunProps{bold: true, color: fg, shade: bg}
				}
			}
			c.paragraph(&buf, trimRuns(runs), docxParaProps{keepNext: true})
		} else {
			buf.Write(c.cellFlow(ch))
		}
		cellXML(out, docxContentWidth, fill, buf.Bytes())
		out.WriteString(`</w:tr>`)
	}
	out.WriteString(`</w:tbl>`)
	c.spacer(out)
}

// grid lays the element children of n (KPI tiles, meta cells, summary items) out
// in a fixed number of columns.
func (c *docxConverter) grid(out *bytes.Buffer, n *html.Node, cols int) {
	cells := elementChildren(n)
	if len(cells) == 0 {
		return
	}
	width := docxContentWidth / cols
	out.WriteString(`<w:tbl>` + docxTblPr(true) + `<w:tblGrid>`)
	for i := 0; i < cols; i++ {
		out.WriteString(`<w:gridCol w:w="` + strconv.Itoa(width) + `"/>`)
	}
	out.WriteString(`</w:tblGrid>`)
	for start := 0; start < len(cells); start += cols {
		out.WriteString(`<w:tr><w:trPr><w:cantSplit/></w:trPr>`)
		for i := 0; i < cols; i++ {
			var buf bytes.Buffer
			fill := docxSilk
			if start+i < len(cells) {
				c.gridCell(&buf, cells[start+i])
			} else {
				fill = ""
			}
			cellXML(out, width, fill, buf.Bytes())
		}
		out.WriteString(`</w:tr>`)
	}
	out.WriteString(`</w:tbl>`)
	c.spacer(out)
}

func (c *docxConverter) gridCell(out *bytes.Buffer, cell *html.Node) {
	if !hasClass(cell, "sign-cell") && findNode(cell, func(x *html.Node) bool { return x != cell && isTag(x, "b") }) == nil {
		c.inlineParagraph(out, cell, docxRunProps{bold: true}, docxParaProps{}, false)
		return
	}
	for ch := cell.FirstChild; ch != nil; ch = ch.NextSibling {
		switch {
		case isTag(ch, "b"):
			c.inlineParagraph(out, ch, docxRunProps{bold: true, caps: true, size: 15, color: docxSlate}, docxParaProps{after: 20}, false)
		case hasClass(ch, "v"):
			c.inlineParagraph(out, ch, docxRunProps{bold: true, size: 36, color: docxNavy}, docxParaProps{after: 20}, false)
		case hasClass(ch, "d"):
			c.inlineParagraph(out, ch, docxRunProps{size: 16, color: docxSlate}, docxParaProps{}, false)
		case ch.Type == html.ElementNode:
			c.inlineParagraph(out, ch, docxRunProps{bold: true}, docxParaProps{}, false)
		case ch.Type == html.TextNode && strings.TrimSpace(ch.Data) != "":
			c.paragraph(out, []docxRun{{text: strings.TrimSpace(ch.Data), props: docxRunProps{bold: true}}}, docxParaProps{})
		}
	}
}

type docxTableCell struct {
	node    *html.Node
	header  bool
	num     bool
	colspan int
}

func (c *docxConverter) table(out *bytes.Buffer, n *html.Node) {
	var rows [][]docxTableCell
	var headerRows int
	caption := ""
	var collect func(*html.Node, bool)
	collect = func(x *html.Node, inHead bool) {
		for _, ch := range elementChildren(x) {
			switch ch.Data {
			case "caption":
				caption = nodeText(ch)
			case "thead":
				collect(ch, true)
			case "tbody", "tfoot":
				collect(ch, false)
			case "tr":
				var row []docxTableCell
				for _, cell := range elementChildren(ch) {
					if !isTag(cell, "td", "th") {
						continue
					}
					span, _ := strconv.Atoi(attr(cell, "colspan"))
					if span < 1 {
						span = 1
					}
					row = append(row, docxTableCell{node: cell, header: cell.Data == "th", num: hasClass(cell, "num"), colspan: span})
				}
				if len(row) == 0 {
					continue
				}
				if inHead {
					headerRows = len(rows) + 1
				}
				rows = append(rows, row)
			}
		}
	}
	collect(n, false)
	if len(rows) == 0 {
		return
	}
	cols := 0
	for _, row := range rows {
		total := 0
		for _, cell := range row {
			total += cell.colspan
		}
		if total > cols {
			cols = total
		}
	}
	// Column weights follow content length so numeric columns stay narrow and
	// prose columns get the room, within sensible bounds.
	weights := make([]float64, cols)
	for i := range weights {
		weights[i] = 6
	}
	for _, row := range rows {
		col := 0
		for _, cell := range row {
			if cell.colspan == 1 {
				l := float64(len([]rune(nodeText(cell.node))))
				if l > 44 {
					l = 44
				}
				if l > weights[col] {
					weights[col] = l
				}
			}
			col += cell.colspan
		}
	}
	sum := 0.0
	for _, w := range weights {
		sum += w
	}
	widths := make([]int, cols)
	for i, w := range weights {
		widths[i] = int(w / sum * docxContentWidth)
	}
	colWidth := func(start, span int) int {
		w := 0
		for i := start; i < start+span && i < cols; i++ {
			w += widths[i]
		}
		return w
	}

	if caption != "" {
		c.paragraph(out, []docxRun{{text: caption, props: docxRunProps{italic: true, size: 18, color: docxSlate}}}, docxParaProps{keepNext: true, after: 40})
	}
	out.WriteString(`<w:tbl>` + docxTblPr(true) + `<w:tblGrid>`)
	for _, w := range widths {
		out.WriteString(`<w:gridCol w:w="` + strconv.Itoa(w) + `"/>`)
	}
	out.WriteString(`</w:tblGrid>`)
	for r, row := range rows {
		out.WriteString(`<w:tr><w:trPr><w:cantSplit/>`)
		if r < headerRows {
			out.WriteString(`<w:tblHeader/>`)
		}
		out.WriteString(`</w:trPr>`)
		col := 0
		for _, cell := range row {
			var buf bytes.Buffer
			rp := docxRunProps{size: 18}
			fill := ""
			if cell.header {
				rp = docxRunProps{bold: true, caps: true, size: 15, color: docxSlate}
				fill = docxSilk
			}
			pp := docxParaProps{}
			if cell.num {
				pp.align = "right"
			}
			var runs []docxRun
			c.collectRuns(cell.node, rp, false, "", &runs)
			runs = trimRuns(runs)
			if len(runs) > 0 {
				c.paragraph(&buf, runs, pp)
			}
			width := colWidth(col, cell.colspan)
			if cell.colspan > 1 {
				out.WriteString(`<w:tc><w:tcPr><w:tcW w:w="` + strconv.Itoa(width) + `" w:type="dxa"/><w:gridSpan w:val="` + strconv.Itoa(cell.colspan) + `"/>`)
				if fill != "" {
					out.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="` + fill + `"/>`)
				}
				out.WriteString(`</w:tcPr>`)
				out.Write(buf.Bytes())
				if buf.Len() == 0 {
					out.WriteString(`<w:p/>`)
				}
				out.WriteString(`</w:tc>`)
			} else {
				cellXML(out, width, fill, buf.Bytes())
			}
			col += cell.colspan
		}
		out.WriteString(`</w:tr>`)
	}
	out.WriteString(`</w:tbl>`)
	c.spacer(out)
}

// ---- charts ----------------------------------------------------------------

func (c *docxConverter) chartsBlock(out *bytes.Buffer, n *html.Node) {
	for _, svg := range elementChildren(n) {
		if !isTag(svg, "svg") {
			continue
		}
		label := attr(svg, "aria-label")
		chart, ok := c.charts[label]
		if !ok {
			continue
		}
		out.WriteString(docxInlineChartXML(chart, c.chartIndex[label]+1))
	}
}
