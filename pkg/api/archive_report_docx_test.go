package api

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestRunArchiveReportDOCXProfilesUseSharedSectionRegistry(t *testing.T) {
	report := consistencyMatrixReport()
	manifest := consistencyMatrixManifest()
	for _, profile := range buildRunArchiveReportProfiles() {
		profile := profile
		t.Run(profile.ID, func(t *testing.T) {
			if !runArchiveReportProfileSupportsFormat(profile, "docx") {
				t.Fatalf("profile formats = %#v, want docx", profile.ExportFormats)
			}
			var output bytes.Buffer
			if err := writeRunArchiveReportDOCXWithProfileAndAnnotations(report, &output, profile.ID, nil, manifest); err != nil {
				t.Fatalf("write DOCX: %v", err)
			}
			archive, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
			if err != nil {
				t.Fatalf("open DOCX ZIP: %v", err)
			}
			document := readDOCXTestPart(t, archive, "word/document.xml")
			if err := xml.Unmarshal(document, new(any)); err != nil {
				t.Fatalf("document.xml is not well-formed: %v", err)
			}
			text := string(document)
			if !strings.Contains(text, report.Manifest.TestName) || !strings.Contains(text, profile.Label) {
				t.Fatalf("DOCX document is missing report identity: %s", text)
			}
			renderData := buildRunArchiveReportRenderData(report, profile, nil, manifest)
			for _, section := range reportSectionsForProfile(profile) {
				var sectionLines []string
				section.PDF(&renderData, &sectionLines)
				heading := ""
				for _, line := range sectionLines {
					if strings.TrimSpace(line) != "" {
						heading = strings.TrimSpace(line)
						break
					}
				}
				var escaped bytes.Buffer
				if err := xml.EscapeText(&escaped, []byte(heading)); err != nil {
					t.Fatal(err)
				}
				if heading == "" || !strings.Contains(text, ">"+escaped.String()+"<") {
					t.Fatalf("DOCX profile %q is missing rendered section %q (heading %q)", profile.ID, section.Label, heading)
				}
			}
			if _, err := zipPart(archive, "[Content_Types].xml"); err != nil {
				t.Fatal(err)
			}
			if _, err := zipPart(archive, "word/styles.xml"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunArchiveReportDOCXRejectsUnknownProfile(t *testing.T) {
	var output bytes.Buffer
	err := writeRunArchiveReportDOCXWithProfileAndAnnotations(consistencyMatrixReport(), &output, "unknown", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown report profile") {
		t.Fatalf("error = %v, want unknown report profile", err)
	}
}

func readDOCXTestPart(t *testing.T, archive *zip.Reader, name string) []byte {
	t.Helper()
	part, err := zipPart(archive, name)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := part.Open()
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func zipPart(archive *zip.Reader, name string) (*zip.File, error) {
	for _, part := range archive.File {
		if part.Name == name {
			return part, nil
		}
	}
	return nil, io.EOF
}
