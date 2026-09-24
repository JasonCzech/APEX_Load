package jmeter

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// This is a complete-language-template recognizer, never a Groovy evaluator.
// Anything outside this deliberately small contract remains review gated.
var csvWriterPattern = regexp.MustCompile(`^deftraceId=vars\.get\('([A-Za-z_][A-Za-z0-9_.-]*)'\)\?:'([^'\\]*)'longlatencyMs=prev\.getLatency\(\)defoutputFile=newFile\('([^'\\]*)'\)outputFile\.parentFile\?\.mkdirs\(\)synchronized\(org\.apache\.jmeter\.util\.JMeterUtils\.class\)\{booleanwriteHeader=!outputFile\.exists\(\)\|\|outputFile\.length\(\)==0outputFile\.withWriterAppend\('UTF-8'\)\{writer->if\(writeHeader\)\{writer\.writeLine\('traceId,latencyMs'\)\}writer\.writeLine\("\$\{traceId\},\$\{latencyMs\}"\)\}\}$`)

func compactGroovy(source string) string {
	var out strings.Builder
	var quote rune
	escaped := false
	for _, r := range source {
		if quote != 0 {
			out.WriteRune(r)
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			out.WriteRune(r)
			continue
		}
		if !unicode.IsSpace(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func translateCSVWriter(node *xmlNode, action *dsl.Action, script *dsl.Script, siblings []*treeNode, writerIndex int) bool {
	if !strings.EqualFold(prop(node, "scriptLanguage"), "groovy") || prop(node, "filename") != "" || prop(node, "parameters") != "" {
		return false
	}
	match := csvWriterPattern.FindStringSubmatch(compactGroovy(prop(node, "script")))
	if match == nil {
		return false
	}
	// Native exports read finalized sample extractions. The source writer must
	// therefore follow the last enabled extractor that assigns this variable.
	found := false
	for index, sibling := range siblings {
		extractors, _ := parseSamplerExtractors(&treeNode{Element: node, Children: []*treeNode{sibling}}, nil)
		for _, ext := range extractors {
			if ext.Name != match[1] {
				continue
			}
			if index >= writerIndex {
				return false
			}
			found = true
		}
	}
	if !found {
		return false
	}
	name := path.Base(match[3])
	if dsl.ValidateCSVExportFileName(name) != nil {
		return false
	}
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	indices, _ := action.Metadata["jmeter_csv_export_indices"].([]int)
	action.Metadata["jmeter_csv_export_indices"] = append(indices, len(script.Config.Results.CSVExports))
	fallback := match[2]
	script.Config.Results.CSVExports = append(script.Config.Results.CSVExports, dsl.CSVExport{
		ID: fmt.Sprintf("jmeter_csv_%d", len(script.Config.Results.CSVExports)+1), FileName: name, ActionName: action.Name,
		Columns: []dsl.CSVExportColumn{{Name: "traceId", Extraction: match[1], DefaultValue: &fallback}, {Name: "latencyMs", Metric: "latency_ms"}},
	})
	return true
}
