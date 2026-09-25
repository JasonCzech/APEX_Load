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
	htmlReport, err := renderRunArchiveHTMLReportWithAnnotations(report, profile.ID, annotations, manifest)
	if err != nil {
		return err
	}
	var charts []reportDocumentChart
	if _, hasGraphs := reportSectionSet(profile.IncludedSections)["graphs"]; hasGraphs {
		if charts, err = buildReportDocumentCharts(report); err != nil {
			return err
		}
	}
	bodyXML, title, err := convertReportHTMLToDOCXBody(htmlReport, charts)
	if err != nil {
		return err
	}
	if title == "" {
		title = profile.Label + " Report"
	}
	return writeReportDOCXPackage(writer, profile, title, buildDOCXDocumentXMLFromBody(bodyXML), charts)
}

func buildDOCXDocumentXMLFromBody(body []byte) []byte {
	var doc bytes.Buffer
	doc.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	doc.WriteString(`<w:document xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	doc.Write(body)
	doc.WriteString(`<w:sectPr><w:headerReference w:type="default" r:id="rId2"/><w:footerReference w:type="default" r:id="rId3"/><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1200" w:right="1080" w:bottom="1080" w:left="1080" w:header="500" w:footer="400"/></w:sectPr>`)
	doc.WriteString(`</w:body></w:document>`)
	return doc.Bytes()
}

func writeReportDOCXPackage(writer io.Writer, profile RunArchiveReportProfile, title string, documentXML []byte, charts []reportDocumentChart) error {
	archive := zip.NewWriter(writer)
	parts := []struct {
		name string
		body []byte
	}{
		{name: "[Content_Types].xml", body: []byte(docxContentTypesXML)},
		{name: "_rels/.rels", body: []byte(docxPackageRelationshipsXML)},
		{name: "docProps/app.xml", body: []byte(docxAppPropertiesXML)},
		{name: "docProps/core.xml", body: []byte(buildDOCXCoreProperties(title))},
		{name: "word/document.xml", body: documentXML},
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

const docxFooterXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:pPr><w:jc w:val="right"/></w:pPr><w:r><w:rPr><w:color w:val="5B6875"/><w:sz w:val="16"/></w:rPr><w:t xml:space="preserve">Page </w:t></w:r><w:fldSimple w:instr="PAGE"/><w:r><w:rPr><w:color w:val="5B6875"/><w:sz w:val="16"/></w:rPr><w:t xml:space="preserve"> of </w:t></w:r><w:fldSimple w:instr="NUMPAGES"/></w:p></w:ftr>`

const docxStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="Calibri" w:cs="Calibri"/><w:color w:val="1F2933"/><w:sz w:val="20"/><w:szCs w:val="20"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="100" w:line="264" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>
  <w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="0" w:after="160"/></w:pPr><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/><w:b/><w:color w:val="1F2933"/><w:sz w:val="52"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:keepLines/><w:pBdr><w:bottom w:val="single" w:sz="12" w:space="4" w:color="D8CFC2"/></w:pBdr><w:spacing w:before="360" w:after="140"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:color w:val="1F2933"/><w:sz w:val="32"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:keepLines/><w:spacing w:before="240" w:after="80"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/><w:color w:val="1F2933"/><w:sz w:val="24"/></w:rPr></w:style>
</w:styles>`
