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
	return writeReportDOCXPackage(writer, profile, lines, headings)
}

func writeReportDOCXPackage(writer io.Writer, profile RunArchiveReportProfile, lines []string, headings map[string]struct{}) error {
	archive := zip.NewWriter(writer)
	parts := []struct {
		name string
		body []byte
	}{
		{name: "[Content_Types].xml", body: []byte(docxContentTypesXML)},
		{name: "_rels/.rels", body: []byte(docxPackageRelationshipsXML)},
		{name: "docProps/app.xml", body: []byte(docxAppPropertiesXML)},
		{name: "docProps/core.xml", body: []byte(buildDOCXCoreProperties(profile))},
		{name: "word/document.xml", body: buildDOCXDocumentXML(lines, headings)},
		{name: "word/_rels/document.xml.rels", body: []byte(docxDocumentRelationshipsXML)},
		{name: "word/styles.xml", body: []byte(docxStylesXML)},
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

func buildDOCXDocumentXML(lines []string, headings map[string]struct{}) []byte {
	var body bytes.Buffer
	body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	body.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for index, raw := range lines {
		line := sanitizeDOCXText(raw)
		style := ""
		if index == 0 {
			style = "Title"
		} else if _, ok := headings[line]; ok {
			style = "Heading1"
		}
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
	body.WriteString(`<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1080" w:right="1080" w:bottom="1080" w:left="1080"/></w:sectPr>`)
	body.WriteString(`</w:body></w:document>`)
	return body.Bytes()
}

func buildDOCXCoreProperties(profile RunArchiveReportProfile) string {
	var title bytes.Buffer
	writeDOCXEscaped(&title, "APEX Load "+profile.Label+" Archive Report")
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
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
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

const docxDocumentRelationshipsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`

const docxStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>
  <w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:rPr><w:b/><w:sz w:val="34"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:rPr><w:b/><w:sz w:val="26"/></w:rPr></w:style>
</w:styles>`
