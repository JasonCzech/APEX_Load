package api

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
)

const docxContentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

// writeRunArchiveReportDOCXWithProfileAndAnnotations generates a self-contained
// OOXML document directly in the controller. It intentionally reuses the text
// projection from the shared report section registry, so DOCX cannot drift to a
// different profile order or expose sections omitted from HTML/PDF/CSV.
func writeRunArchiveReportDOCXWithProfileAndAnnotations(report *RunArchiveReport, writer io.Writer, profileID string, annotations *ReportAnnotations, manifest *TestRunManifest) error {
	profile, ok := lookupRunArchiveReportProfile(profileID)
	if !ok {
		return fmt.Errorf("unknown report profile %q", profileID)
	}
	if !runArchiveReportProfileSupportsFormat(profile, "docx") {
		return fmt.Errorf("report profile %q does not support docx export", profile.ID)
	}
	lines := buildRunArchiveReportPDFLines(report, profile, annotations, manifest)
	headings := buildDOCXSectionHeadings(report, profile, annotations, manifest)
	headings["Review Summary"] = struct{}{}
	var charts []reportDocumentChart
	if _, hasGraphs := reportSectionSet(profile.IncludedSections)["graphs"]; hasGraphs {
		var err error
		charts, err = buildReportDocumentCharts(report)
		if err != nil {
			return err
		}
	}
	return writeReportDOCXPackage(writer, profile, lines, headings, charts)
}

func writeReportDOCXPackage(writer io.Writer, profile RunArchiveReportProfile, lines []string, headings map[string]struct{}, charts []reportDocumentChart) error {
	archive := zip.NewWriter(writer)
	parts := []struct {
		name string
		body []byte
	}{
		{name: "[Content_Types].xml", body: []byte(docxContentTypesXML)},
		{name: "_rels/.rels", body: []byte(docxPackageRelationshipsXML)},
		{name: "docProps/app.xml", body: []byte(docxAppPropertiesXML)},
		{name: "docProps/core.xml", body: []byte(buildDOCXCoreProperties(lines[0]))},
		{name: "word/document.xml", body: buildDOCXDocumentXML(lines, headings, charts)},
		{name: "word/_rels/document.xml.rels", body: []byte(buildDOCXDocumentRelationshipsXML(charts))},
		{name: "word/styles.xml", body: []byte(docxStylesXML)},
		{name: "word/header1.xml", body: []byte(buildDOCXHeaderXML(profile))},
		{name: "word/footer1.xml", body: []byte(docxFooterXML)},
	}
	for index, chart := range charts {
		parts = append(parts, struct {
			name string
			body []byte
		}{name: fmt.Sprintf("word/media/chart-%d.png", index+1), body: chart.PNG})
	}
	for _, part := range parts {
		header := &zip.FileHeader{Name: part.name, Method: zip.Deflate}
		// A stable timestamp makes repeated renders easier to compare while the
		// generated-at value remains inside the report content and core metadata.
		header.SetModTime(time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC))
		entry, err := archive.CreateHeader(header)
		if err != nil {
			_ = archive.Close()
			return fmt.Errorf("create DOCX part %q: %w", part.name, err)
		}
		if _, err := entry.Write(part.body); err != nil {
			_ = archive.Close()
			return fmt.Errorf("write DOCX part %q: %w", part.name, err)
		}
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("finalize DOCX package: %w", err)
	}
	return nil
}

func buildDOCXSectionHeadings(report *RunArchiveReport, profile RunArchiveReportProfile, annotations *ReportAnnotations, manifest *TestRunManifest) map[string]struct{} {
	headings := make(map[string]struct{})
	if report == nil {
		return headings
	}
	data := buildRunArchiveReportRenderData(report, profile, annotations, manifest)
	for _, section := range reportSectionsForProfile(profile) {
		var sectionLines []string
		section.PDF(&data, &sectionLines)
		for _, line := range sectionLines {
			line = sanitizeDOCXText(strings.TrimSpace(line))
			if line != "" {
				headings[line] = struct{}{}
				break
			}
		}
	}
	return headings
}

func buildDOCXDocumentXML(lines []string, headings map[string]struct{}, charts []reportDocumentChart) []byte {
	var body bytes.Buffer
	body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	body.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	type pair struct{ label, value string }
	var pairs []pair
	var tableRows [][]string
	chartIndexes := make(map[string]int, len(charts))
	for index, chart := range charts {
		chartIndexes[chart.ID] = index
	}
	flushPairs := func() {
		if len(pairs) == 0 {
			return
		}
		body.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/><w:tblBorders><w:bottom w:val="single" w:sz="4" w:color="D4DBE3"/><w:insideH w:val="single" w:sz="4" w:color="E5EAF0"/></w:tblBorders></w:tblPr><w:tblGrid><w:gridCol w:w="3400"/><w:gridCol w:w="7400"/></w:tblGrid>`)
		for _, item := range pairs {
			body.WriteString(`<w:tr><w:tc><w:tcPr><w:tcW w:w="3400" w:type="dxa"/><w:shd w:fill="F3F6F9"/></w:tcPr><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>`)
			writeDOCXEscaped(&body, item.label)
			body.WriteString(`</w:t></w:r></w:p></w:tc><w:tc><w:tcPr><w:tcW w:w="7400" w:type="dxa"/></w:tcPr><w:p><w:r><w:t xml:space="preserve">`)
			writeDOCXEscaped(&body, item.value)
			body.WriteString(`</w:t></w:r></w:p></w:tc></w:tr>`)
		}
		body.WriteString(`</w:tbl>`)
		pairs = nil
	}
	flushTable := func() {
		if len(tableRows) == 0 {
			return
		}
		body.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/><w:tblBorders><w:bottom w:val="single" w:sz="4" w:color="D4DBE3"/><w:insideH w:val="single" w:sz="4" w:color="E5EAF0"/></w:tblBorders></w:tblPr>`)
		for rowIndex, row := range tableRows {
			body.WriteString(`<w:tr>`)
			if rowIndex == 0 {
				body.WriteString(`<w:trPr><w:tblHeader/></w:trPr>`)
			}
			for _, cell := range row {
				body.WriteString(`<w:tc>`)
				if rowIndex == 0 {
					body.WriteString(`<w:tcPr><w:shd w:fill="F3F6F9"/></w:tcPr>`)
				}
				body.WriteString(`<w:p><w:r>`)
				if rowIndex == 0 {
					body.WriteString(`<w:rPr><w:b/></w:rPr>`)
				}
				body.WriteString(`<w:t xml:space="preserve">`)
				writeDOCXEscaped(&body, cell)
				body.WriteString(`</w:t></w:r></w:p></w:tc>`)
			}
			body.WriteString(`</w:tr>`)
		}
		body.WriteString(`</w:tbl>`)
		tableRows = nil
	}
	for index, raw := range lines {
		if strings.HasPrefix(raw, reportDocumentChartMarker) {
			flushPairs()
			flushTable()
			if chartIndex, ok := chartIndexes[strings.TrimPrefix(raw, reportDocumentChartMarker)]; ok {
				body.WriteString(docxInlineChartXML(charts[chartIndex], chartIndex+1))
			}
			continue
		}
		line := sanitizeDOCXText(raw)
		if cells, ok := parseReportTableLine(line); ok {
			flushPairs()
			tableRows = append(tableRows, cells)
			continue
		}
		flushTable()
		style := ""
		if index == 0 {
			style = "Title"
		} else if _, ok := headings[line]; ok {
			style = "Heading1"
		}
		if style == "" {
			if label, value, ok := splitDOCXKeyValue(line); ok {
				pairs = append(pairs, pair{label: label, value: value})
				continue
			}
		}
		flushPairs()
		body.WriteString(`<w:p>`)
		if style != "" {
			body.WriteString(`<w:pPr><w:pStyle w:val="`)
			writeDOCXEscaped(&body, style)
			body.WriteString(`"/></w:pPr>`)
		}
		if line != "" {
			body.WriteString(`<w:r><w:t xml:space="preserve">`)
			writeDOCXEscaped(&body, line)
			body.WriteString(`</w:t></w:r>`)
		}
		body.WriteString(`</w:p>`)
	}
	flushPairs()
	flushTable()
	body.WriteString(`<w:sectPr><w:headerReference w:type="default" r:id="rId2"/><w:footerReference w:type="default" r:id="rId3"/><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1080" w:right="1080" w:bottom="1080" w:left="1080" w:header="360" w:footer="360"/></w:sectPr>`)
	body.WriteString(`</w:body></w:document>`)
	return bytes.Replace(body.Bytes(), []byte(`<w:document xmlns:w=`), []byte(`<w:document xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:w=`), 1)
}

func docxInlineChartXML(chart reportDocumentChart, index int) string {
	// 6.6 inches fits the letter page after its 0.75-inch margins. Preserve
	// the source aspect ratio so the chart's axes and labels are not distorted.
	const widthEMU = 6035040
	heightEMU := widthEMU * chart.Height / chart.Width
	var name bytes.Buffer
	writeDOCXEscaped(&name, chart.Title)
	return fmt.Sprintf(`<w:p><w:pPr><w:spacing w:before="120" w:after="140"/><w:keepLines/></w:pPr><w:r><w:drawing><wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"><wp:extent cx="%d" cy="%d"/><wp:docPr id="%d" name="%s" descr="%s"/><a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:nvPicPr><pic:cNvPr id="%d" name="%s"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="rId%d"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`, widthEMU, heightEMU, index, name.String(), name.String(), index, name.String(), index+3, widthEMU, heightEMU)
}

func buildDOCXDocumentRelationshipsXML(charts []reportDocumentChart) string {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	body.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`)
	body.WriteString(`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>`)
	body.WriteString(`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>`)
	for index := range charts {
		fmt.Fprintf(&body, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/chart-%d.png"/>`, index+4, index+1)
	}
	body.WriteString(`</Relationships>`)
	return body.String()
}

func splitDOCXKeyValue(line string) (string, string, bool) {
	index := strings.IndexByte(line, ':')
	if index < 2 || index > 42 || index+1 >= len(line) {
		return "", "", false
	}
	label := strings.TrimSpace(line[:index])
	value := strings.TrimSpace(line[index+1:])
	if label == "" || value == "" || strings.HasPrefix(label, "-") || strings.Contains(label, "/") || strings.HasPrefix(value, "//") {
		return "", "", false
	}
	return label, value, true
}

func buildDOCXHeaderXML(profile RunArchiveReportProfile) string {
	var label bytes.Buffer
	writeDOCXEscaped(&label, "APEX Load  /  "+profile.Label)
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:color="D8CFC2"/></w:pBdr></w:pPr><w:r><w:rPr><w:b/><w:color w:val="0F766E"/><w:sz w:val="18"/></w:rPr><w:t>` + label.String() + `</w:t></w:r></w:p></w:hdr>`
}

func buildDOCXCoreProperties(reportTitle string) string {
	var title bytes.Buffer
	writeDOCXEscaped(&title, reportTitle)
	generated := time.Now().UTC().Format(time.RFC3339)
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + title.String() + `</dc:title><dc:creator>APEX Load</dc:creator><cp:lastModifiedBy>APEX Load</cp:lastModifiedBy>` +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + generated + `</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + generated + `</dcterms:modified></cp:coreProperties>`
}

func writeDOCXEscaped(writer io.Writer, value string) {
	_ = xml.EscapeText(writer, []byte(value))
}

func sanitizeDOCXText(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || unicode.IsGraphic(r) || r == ' ' {
			return r
		}
		return -1
	}, value)
}

const docxContentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="png" ContentType="image/png"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
  <Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/>
  <Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/>
  <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
  <Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>
</Types>`

const docxPackageRelationshipsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
  <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>
</Relationships>`

const docxAppPropertiesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>APEX Load</Application><AppVersion>1.0</AppVersion></Properties>`

const docxFooterXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:pPr><w:jc w:val="right"/></w:pPr><w:r><w:t xml:space="preserve">Page </w:t></w:r><w:fldSimple w:instr="PAGE"/></w:p></w:ftr>`

const docxStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>
  <w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:spacing w:after="240"/></w:pPr><w:rPr><w:b/><w:color w:val="0F766E"/><w:sz w:val="36"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="320" w:after="120"/></w:pPr><w:rPr><w:b/><w:color w:val="0F766E"/><w:sz w:val="25"/></w:rPr></w:style>
</w:styles>`
