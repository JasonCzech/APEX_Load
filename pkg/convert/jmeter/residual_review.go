package jmeter

import (
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
)

const jmeterOmittedSourceMarker = "[SOURCE LOGIC OMITTED]"

// jmeterResidualPropertyPattern selects source-language bodies and controller
// expressions whose semantics were either translated separately or review-
// gated. These values are not required to understand the source mapping and can
// contain arbitrary credentials, so they never enter the public SourceReview.
var jmeterResidualPropertyPattern = regexp.MustCompile(`(?is)<(?:stringProp|boolProp|intProp|longProp)\b[^>]*\bname\s*=\s*["'](?:script|BeanShellSampler\.query|BSFSampler\.query|BeanShellSampler\.script|IfController\.condition|WhileController\.condition|LoopController\.loops)["'][^>]*>.*?</(?:stringProp|boolProp|intProp|longProp)\s*>`)

func redactJMeterResidualSource(content string) (string, bool) {
	redacted := jmeterResidualPropertyPattern.ReplaceAllStringFunc(content, func(property string) string {
		openEnd := strings.IndexByte(property, '>')
		closeStart := strings.LastIndex(strings.ToLower(property), "</")
		if openEnd < 0 || closeStart <= openEnd {
			return property
		}
		body := property[openEnd+1 : closeStart]
		// Retain the original line count so source mappings continue to point at
		// the same locations even when a multiline script body is omitted.
		replacement := jmeterOmittedSourceMarker + strings.Repeat("\n", strings.Count(body, "\n"))
		return property[:openEnd+1] + replacement + property[closeStart:]
	})
	return redacted, redacted != content
}

func markJMeterSourceReviewRedacted(review *sourceview.Review, sourceFileName string) {
	if review == nil {
		return
	}
	want := strings.ToLower(archiveingest.LogicalPath(sourceFileName))
	wantBase := strings.ToLower(archiveingest.LogicalBase(sourceFileName))
	for index := range review.SourceFiles {
		got := strings.ToLower(archiveingest.LogicalPath(review.SourceFiles[index].Path))
		if got == want || strings.ToLower(archiveingest.LogicalBase(got)) == wantBase {
			review.SourceFiles[index].Redacted = true
		}
	}
}
