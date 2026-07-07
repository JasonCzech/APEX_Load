package sample

// sourceview.go — split from converter.go (same package, behavior-preserving).

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
)

type sampleSourceLocation struct {
	SourceFile        string
	StartLine         int
	EndLine           int
	ResponseFile      string
	ResponseStartLine int
	ResponseEndLine   int
}

func sampleSourceFiles(format string, content string, responseContent string) map[string]string {
	files := map[string]string{
		sampleSourceFileName(format): content,
	}
	if strings.TrimSpace(responseContent) != "" {
		files[sampleResponseFileName(format)] = responseContent
	}
	return files
}

func sampleSourceFileName(format string) string {
	switch normalizeFormat(format) {
	case "raw_http":
		return "sample.http"
	case "curl":
		return "sample.curl"
	case "har":
		return "sample.har"
	case "rest_json":
		return "sample.json"
	default:
		return "sample.txt"
	}
}

func sampleResponseFileName(format string) string {
	switch normalizeFormat(format) {
	case "raw_http":
		return "sample-response.http"
	case "curl":
		return "sample-response.txt"
	default:
		return "sample-response.txt"
	}
}

func sampleSourceFunction(format string) string {
	switch normalizeFormat(format) {
	case "raw_http":
		return "raw_http_request"
	case "curl":
		return "curl_command"
	case "har":
		return "har_entry"
	case "rest_json":
		return "rest_json_request"
	default:
		return "sample_request"
	}
}

func sampleSourceLocations(format string, content string, responseContent string, exchanges []exchange) []sampleSourceLocation {
	sourceFile := sampleSourceFileName(format)
	responseFile := ""
	if strings.TrimSpace(responseContent) != "" {
		responseFile = sampleResponseFileName(format)
	}
	contentLineIndex := sourceview.NewLineIndex(content)
	responseLineIndex := sourceview.NewLineIndex(responseContent)
	contentMaxLine := maxLineFromIndex(content, contentLineIndex, 1)
	responseMaxLine := maxLineFromIndex(responseContent, responseLineIndex, 1)
	locations := make([]sampleSourceLocation, len(exchanges))
	for index := range exchanges {
		locations[index] = sampleSourceLocation{
			SourceFile:        sourceFile,
			StartLine:         1,
			EndLine:           contentMaxLine,
			ResponseFile:      responseFile,
			ResponseStartLine: 1,
			ResponseEndLine:   responseMaxLine,
		}
	}
	if len(exchanges) == 0 {
		return locations
	}

	switch normalizeFormat(format) {
	case "raw_http":
		assignDelimitedContentLocations(locations, content, contentLineIndex, rawHTTPRequestDelimiter)
	case "rest_json", "har":
		assignURLLocations(locations, content, contentLineIndex, exchanges)
	case "curl":
		assignCurlLocations(locations, contentLineIndex, contentMaxLine, exchanges)
	}
	if responseFile != "" {
		assignResponseLocations(locations, responseContent, responseLineIndex)
	}
	return locations
}

func assignDelimitedContentLocations(locations []sampleSourceLocation, content string, lineIndex sourceview.LineIndex, delimiter string) {
	offset := 0
	for index := range locations {
		start := offset
		end := len(content)
		if next := strings.Index(content[offset:], delimiter); next >= 0 {
			end = offset + next
			offset = end + len(delimiter)
		}
		locations[index].StartLine = lineIndex.LineForOffset(start)
		locations[index].EndLine = lineIndex.LineForOffset(end)
	}
}

func assignURLLocations(locations []sampleSourceLocation, content string, lineIndex sourceview.LineIndex, exchanges []exchange) {
	offset := 0
	for index := range locations {
		needle := exchanges[index].URL
		if strings.TrimSpace(needle) == "" {
			continue
		}
		found := strings.Index(content[offset:], needle)
		if found < 0 {
			found = strings.Index(content, needle)
			if found < 0 {
				continue
			}
		} else {
			found += offset
		}
		locations[index].StartLine = lineIndex.LineForOffset(found)
		locations[index].EndLine = locations[index].StartLine
		offset = found + len(needle)
	}
}

func assignCurlLocations(locations []sampleSourceLocation, lineIndex sourceview.LineIndex, end int, exchanges []exchange) {
	if len(exchanges) == 0 {
		return
	}
	start := lineIndex.LineForOffset(0)
	for index := range locations {
		locations[index].StartLine = start
		locations[index].EndLine = end
	}
}

func assignResponseLocations(locations []sampleSourceLocation, responseContent string, lineIndex sourceview.LineIndex) {
	offset := 0
	for index := range locations {
		start := offset
		end := len(responseContent)
		if next := strings.Index(responseContent[offset:], rawHTTPResponseDelimiter); next >= 0 {
			end = offset + next
			offset = end + len(rawHTTPResponseDelimiter)
		}
		locations[index].ResponseStartLine = lineIndex.LineForOffset(start)
		locations[index].ResponseEndLine = lineIndex.LineForOffset(end)
	}
}

func requestSourceLocation(locations []sampleSourceLocation, index int) sampleSourceLocation {
	if index >= 0 && index < len(locations) {
		return locations[index]
	}
	return sampleSourceLocation{SourceFile: "sample.txt", StartLine: 1, EndLine: 1}
}

func responseSourceLocation(locations []sampleSourceLocation, index int) sampleSourceLocation {
	location := requestSourceLocation(locations, index)
	if location.ResponseFile != "" {
		location.SourceFile = location.ResponseFile
		location.StartLine = location.ResponseStartLine
		location.EndLine = location.ResponseEndLine
	}
	return location
}

func maxLineFromIndex(content string, lineIndex sourceview.LineIndex, fallback int) int {
	if strings.TrimSpace(content) == "" {
		return fallback
	}
	return lineIndex.MaxLine(fallback)
}
