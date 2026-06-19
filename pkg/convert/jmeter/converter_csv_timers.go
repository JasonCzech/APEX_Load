package jmeter

// converter_csv_timers.go — split from converter.go (same package, behavior-preserving).

import (
	"bytes"
	"encoding/csv"
	"path"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func parseTestActionPauseMs(node *treeNode) int {
	if node == nil || node.Element == nil {
		return 0
	}
	for _, child := range node.Children {
		switch child.Element.Name {
		case "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer", "PoissonRandomTimer":
			if delay := parseTimerMs(child.Element, child.Element.Name); delay > 0 {
				return delay
			}
		}
	}
	return parseInt(convertutil.FirstNonEmpty(
		prop(node.Element, "ActionProcessor.duration"),
		prop(node.Element, "TestAction.duration"),
		prop(node.Element, "duration"),
	))
}

func parseCSVDataSet(node *xmlNode, sidecarFiles map[string][]byte) dsl.DataSource {
	filename := prop(node, "filename")
	variableNames := splitCSVNames(prop(node, "variableNames"))
	delimiter := convertutil.FirstNonEmpty(prop(node, "delimiter"), ",")
	firstRowHeader := len(variableNames) == 0
	inlineData := ""
	if len(variableNames) == 0 {
		if content, ok := jmeterSidecarFile(sidecarFiles, filename); ok {
			variableNames = inferCSVHeader(content, delimiter)
			inlineData = string(content)
		}
	}
	name := sanitizeName(convertutil.FirstNonEmpty(attrOr(node, "testname", ""), path.Base(filename), "csv_data"))
	strategy := "sequential"
	if strings.EqualFold(prop(node, "randomOrder"), "true") {
		strategy = "random"
	}
	return dsl.DataSource{
		Name:           name,
		Type:           "csv",
		Path:           filename,
		InlineData:     inlineData,
		Strategy:       strategy,
		Delimiter:      delimiter,
		FirstRowHeader: firstRowHeader,
		Columns:        variableNames,
	}
}

func jmeterSidecarFile(files map[string][]byte, filename string) ([]byte, bool) {
	if len(files) == 0 {
		return nil, false
	}
	keys := []string{
		filename,
		jmeterBaseName(filename),
		strings.TrimLeft(strings.ReplaceAll(filename, `\`, `/`), `/`),
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if data, ok := files[key]; ok {
			return data, true
		}
	}
	for key, data := range files {
		if strings.EqualFold(jmeterBaseName(key), jmeterBaseName(filename)) {
			return data, true
		}
	}
	return nil, false
}

func inferCSVHeader(data []byte, delimiter string) []string {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	if comma := jmeterCSVDelimiter(delimiter); comma != 0 {
		reader.Comma = comma
	}
	row, err := reader.Read()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(row))
	for _, field := range row {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func jmeterCSVDelimiter(value string) rune {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ",":
		return ','
	case "\\t", "tab", "tabs":
		return '\t'
	case ";", "semicolon":
		return ';'
	case "|", "pipe":
		return '|'
	default:
		runes := []rune(value)
		if len(runes) == 1 {
			return runes[0]
		}
		return ','
	}
}

func parseTimerMs(node *xmlNode, elementName string) int {
	delay := parseInt(convertutil.FirstNonEmpty(
		prop(node, "ConstantTimer.delay"),
		prop(node, "RandomTimer.delay"),
		prop(node, "GaussianRandomTimer.delay"),
		prop(node, "PoissonRandomTimer.delay"),
	))
	if elementName == "UniformRandomTimer" {
		rangeMs := parseInt(prop(node, "RandomTimer.range"))
		if rangeMs > 0 {
			return delay + rangeMs/2
		}
	}
	if elementName == "GaussianRandomTimer" {
		rangeMs := parseInt(prop(node, "GaussianRandomTimer.range"))
		if rangeMs > 0 {
			return delay + rangeMs/2
		}
	}
	if elementName == "PoissonRandomTimer" {
		rangeMs := parseInt(prop(node, "PoissonRandomTimer.range"))
		if rangeMs > 0 {
			return delay + rangeMs/2
		}
	}
	if isThroughputTimer(elementName) {
		if throughput := parseFloat(convertutil.FirstNonEmpty(prop(node, "throughput"), jmeterNamedValue(node, "throughput"), prop(node, "ConstantThroughputTimer.throughput"), prop(node, "PreciseThroughputTimer.throughput"))); throughput > 0 {
			period := parseInt(convertutil.FirstNonEmpty(prop(node, "throughputPeriod"), jmeterNamedValue(node, "throughputPeriod"), prop(node, "PreciseThroughputTimer.throughputPeriod")))
			if period <= 0 {
				period = 60
			}
			return int((float64(period) * 1000) / throughput)
		}
	}
	return delay
}

func isThroughputTimer(elementName string) bool {
	return elementName == "ConstantThroughputTimer" || elementName == "PreciseThroughputTimer"
}

func splitCSVNames(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
