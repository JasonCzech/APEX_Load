package jmeter

import (
	"strings"
	"testing"
	"time"
)

func TestJavaTimeLayoutPreservesEscapedApostrophes(t *testing.T) {
	clock := time.Date(2026, 9, 23, 1, 2, 3, 456000000, time.UTC)
	for _, tc := range []struct{ pattern, want string }{
		{"yyyy''MM", "2026'09"},
		{"''yyyy-MM-dd''", "'2026-09-23'"},
		{"yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", "2026-09-23T01:02:03.456Z"},
		{"yyyy'it''s'MM", "2026it's09"},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			layout, ok := javaTimeLayout(tc.pattern)
			if !ok || clock.Format(layout) != tc.want {
				t.Fatalf("layout %q supported=%t yields %q; want %q", layout, ok, clock.Format(layout), tc.want)
			}
		})
	}
}

func TestCSVWriterRequiresFinalSourceExtraction(t *testing.T) {
	source := string(splitPayFixture(t, "repaired"))
	start := strings.Index(source, "          <JSONPostProcessor ")
	end := strings.Index(source[start:], "          <JSR223PostProcessor ") + start
	extractor := source[start:end]
	afterWriter := strings.Index(source, "</JSR223PostProcessor>") + len("</JSR223PostProcessor>")
	afterWriter += strings.Index(source[afterWriter:], "<hashTree/>") + len("<hashTree/>")
	for _, tc := range []struct {
		name, source string
		blocked      bool
	}{
		{"extractor before writer", source, false},
		{"extractor after writer", source[:start] + source[end:afterWriter] + extractor + source[afterWriter:], true},
		{"later overwrite", source[:afterWriter] + extractor + source[afterWriter:], true},
		{"disabled later overwrite", source[:afterWriter] + strings.Replace(extractor, `enabled="true"`, `enabled="false"`, 1) + source[afterWriter:], false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ConvertJMX([]byte(tc.source), ConvertOptions{TimeMode: "utc"})
			if err != nil {
				t.Fatal(err)
			}
			script := result.Scripts[0]
			blocked := hasJMeterReviewReason(script.Actions, "unsupported_jmeter_processor")
			if blocked != tc.blocked || (len(script.Config.Results.CSVExports) == 0) != tc.blocked {
				t.Fatalf("blocked=%t exports=%v", blocked, script.Config.Results.CSVExports)
			}
		})
	}
}

func TestCSVWriterRejectsUnrepresentableFilenames(t *testing.T) {
	for _, tc := range []struct {
		name    string
		blocked bool
	}{{" trace.csv", true}, {"C:trace.csv", true}, {"trace&#9;.csv", true}, {".csv", true}, {"trace.CSV", false}} {
		t.Run(tc.name, func(t *testing.T) {
			source := strings.Replace(string(splitPayFixture(t, "repaired")), "C:/temp/traceId-results.csv", tc.name, 1)
			result, err := ConvertJMX([]byte(source), ConvertOptions{TimeMode: "utc"})
			if err != nil {
				t.Fatal(err)
			}
			if blocked := hasJMeterReviewReason(result.Scripts[0].Actions, "unsupported_jmeter_processor"); blocked != tc.blocked {
				t.Fatalf("blocked=%t want=%t", blocked, tc.blocked)
			}
		})
	}
}
