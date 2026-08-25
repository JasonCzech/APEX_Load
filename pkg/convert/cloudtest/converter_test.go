package cloudtest

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertClassifiesEveryElementAndBlocksUnprovenMappings(t *testing.T) {
	input := []byte(`<?xml version="1.0"?>
<Repository>
  <Clip name="Checkout">
    <Transaction name="Open"><HTTP method="GET" url="https://example.test/"/></Transaction>
    <MysteryThing value="secret-must-not-escape"/>
  </Clip>
  <Composition name="Regional Sale" duration="180000">
    <Track name="East" clip="Checkout" offset="60000" users="25" ramp="30000" region="us-east"/>
  </Composition>
</Repository>`)
	result, err := Convert(input, ConvertOptions{ProjectID: "project-1", FileName: "export.xml"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got, want := len(result.Dispositions), 7; got != want {
		t.Fatalf("dispositions = %d, want %d: %#v", got, want, result.Dispositions)
	}
	for _, disposition := range result.Dispositions {
		if disposition.Disposition != DispositionBlockedDraft || disposition.FixtureProven {
			t.Fatalf("unsafe disposition: %#v", disposition)
		}
		if disposition.Path == "" || disposition.Element == "" {
			t.Fatalf("untraceable disposition: %#v", disposition)
		}
	}
	if result.Stats.UnknownElements != 1 || len(result.Unsupported) != 1 {
		t.Fatalf("unknown accounting = stats=%#v unsupported=%#v", result.Stats, result.Unsupported)
	}
	if len(result.Scripts) != 1 || result.Scripts[0].ProjectID != "project-1" {
		t.Fatalf("scripts = %#v", result.Scripts)
	}
	for _, action := range result.Scripts[0].Actions {
		if action.Type != "source_review" || action.Metadata["execution_blocker"] == nil {
			t.Fatalf("action is executable or unblocked: %#v", action)
		}
	}
	if issues := dsl.ValidateScriptIssues(result.Scripts[0]); len(issues) != 0 {
		t.Fatalf("review script validation issues: %v", issues)
	}
	if len(result.ScenarioDrafts) != 1 || len(result.ScenarioDrafts[0].Groups) != 1 {
		t.Fatalf("scenario drafts = %#v", result.ScenarioDrafts)
	}
	group := result.ScenarioDrafts[0].Groups[0]
	if group.StartOffset != "1m0s" || group.RampUp != "30s" || group.VUsers != 25 || group.LoadZone != "us-east" {
		t.Fatalf("group = %#v", group)
	}
	serialized := mustJSON(t, result)
	if strings.Contains(serialized, "secret-must-not-escape") {
		t.Fatal("unapproved attribute value leaked into review output")
	}
}

func TestConvertBoundedZipOrdersXMLAndRejectsMissingXML(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range []struct{ name, body string }{
		{"b.xml", `<Repository><Clip name="B"><HTTP/></Clip></Repository>`},
		{"a.xml", `<Repository><Clip name="A"><HTTP/></Clip></Repository>`},
		{"readme.txt", "not parsed"},
	} {
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := Convert(buffer.Bytes(), ConvertOptions{FileName: "bundle.zip"})
	if err != nil {
		t.Fatalf("Convert zip: %v", err)
	}
	if result.SourceFormat != "cloudtest_zip" || len(result.Scripts) != 2 || result.Scripts[0].Name != "A" || result.Scripts[1].Name != "B" {
		t.Fatalf("zip result = format=%q scripts=%#v", result.SourceFormat, result.Scripts)
	}

	buffer.Reset()
	writer = zip.NewWriter(&buffer)
	file, _ := writer.Create("readme.txt")
	_, _ = file.Write([]byte("none"))
	_ = writer.Close()
	if _, err := Convert(buffer.Bytes(), ConvertOptions{FileName: "bundle.zip"}); err == nil || !strings.Contains(err.Error(), "no XML") {
		t.Fatalf("missing XML error = %v", err)
	}
}

func TestConvertBoundsDraftAssetsAcrossZIPFiles(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for fileIndex := 0; fileIndex < 2; fileIndex++ {
		entry, err := writer.Create(fmt.Sprintf("%d.xml", fileIndex))
		if err != nil {
			t.Fatal(err)
		}
		var document strings.Builder
		document.WriteString("<Repository>")
		for compositionIndex := 0; compositionIndex < 150; compositionIndex++ {
			document.WriteString(fmt.Sprintf(`<Composition name="c-%d-%d"/>`, fileIndex, compositionIndex))
		}
		document.WriteString("</Repository>")
		if _, err := entry.Write([]byte(document.String())); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Convert(buffer.Bytes(), ConvertOptions{FileName: "bounded.zip"}); err == nil || !strings.Contains(err.Error(), "Composition assets") {
		t.Fatalf("cross-file asset bound error = %v", err)
	}
}

func TestConvertRejectsDOCTYPEAndMalformedXML(t *testing.T) {
	tests := []string{
		`<!DOCTYPE foo [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><Repository>&xxe;</Repository>`,
		`<Repository><Clip></Repository>`,
	}
	for _, input := range tests {
		if _, err := Convert([]byte(input), ConvertOptions{FileName: "bad.xml"}); err == nil {
			t.Fatalf("Convert(%q) unexpectedly succeeded", input)
		}
	}
}

func TestSupportMatrixRemainsFixtureGated(t *testing.T) {
	entries := GetSupportMatrix()
	if len(entries) == 0 {
		t.Fatal("support matrix is empty")
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.ID == "" || seen[entry.ID] {
			t.Fatalf("invalid support entry: %#v", entry)
		}
		seen[entry.ID] = true
		if entry.FixtureProven {
			t.Fatalf("support entry claims unavailable fixture proof: %#v", entry)
		}
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
