package k6

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
)

var (
	k6DefaultFunctionPattern = regexp.MustCompile(`(?m)\bexport\s+default\s+(?:async\s+)?function(?:\s+[A-Za-z_$][A-Za-z0-9_$]*)?\s*\([^)]*\)\s*\{?`)
	k6LineCommentPattern     = regexp.MustCompile(`(?m)//[^\n]*`)
	k6BlockCommentPattern    = regexp.MustCompile(`(?s)/\*.*?\*/`)
	k6SafeAssignmentPrefix   = regexp.MustCompile(`^\s*(?:const|let|var)\s+[A-Za-z_$][A-Za-z0-9_$]*\s*=\s*$`)
)

// detectK6ResidualSource performs a conservative lexical remainder check after
// the constrained importer has identified every HTTP call it understands.
// Anything other than core imports, function scaffolding, comments, punctuation,
// those calls, and the statically translated spans is source behavior that the
// generated DSL does not reproduce.
func detectK6ResidualSource(content string, requests []parsedHTTPRequest, translated []k6Span) []int {
	masked := []byte(content)
	lineIndex := sourceview.NewLineIndex(content)
	residual := map[int]struct{}{}

	for _, request := range requests {
		if len(request.ReviewReasons) != 0 {
			for line := request.StartLine; line <= request.EndLine; line++ {
				residual[line] = struct{}{}
			}
		}
		if request.StartOffset < 0 || request.EndOffset <= request.StartOffset || request.EndOffset > len(masked) {
			continue
		}
		lineStart := strings.LastIndex(content[:request.StartOffset], "\n") + 1
		prefix := content[lineStart:request.StartOffset]
		if k6SafeAssignmentPrefix.MatchString(prefix) {
			maskK6Span(masked, lineStart, request.StartOffset)
		}
		maskK6Span(masked, request.StartOffset, request.EndOffset)
	}

	for _, span := range translated {
		maskK6Span(masked, span.Start, span.End)
	}

	for _, match := range importPattern.FindAllStringIndex(content, -1) {
		line := extractLine(content, match[0])
		if !k6CoreImport(line) {
			continue
		}
		start := strings.LastIndex(content[:match[0]], "\n") + 1
		end := strings.IndexByte(content[match[0]:], '\n')
		if end < 0 {
			end = len(content)
		} else {
			end += match[0]
		}
		maskK6Span(masked, start, end)
	}
	for _, match := range k6DefaultFunctionPattern.FindAllStringIndex(content, -1) {
		maskK6Span(masked, match[0], match[1])
	}
	for _, pattern := range []*regexp.Regexp{k6LineCommentPattern, k6BlockCommentPattern} {
		for _, match := range pattern.FindAllStringIndex(string(masked), -1) {
			maskK6Span(masked, match[0], match[1])
		}
	}

	for offset, value := range masked {
		switch value {
		case ' ', '\t', '\r', '\n', ';', '{', '}', '(', ')':
			continue
		default:
			residual[lineIndex.LineForOffset(offset)] = struct{}{}
		}
	}
	return sortedK6Lines(residual)
}

func maskK6Span(content []byte, start, end int) {
	if start < 0 {
		start = 0
	}
	if end > len(content) {
		end = len(content)
	}
	for index := start; index < end; index++ {
		if content[index] != '\r' && content[index] != '\n' {
			content[index] = ' '
		}
	}
}

func sortedK6Lines(lines map[int]struct{}) []int {
	if len(lines) == 0 {
		return nil
	}
	values := make([]int, 0, len(lines))
	for line := range lines {
		if line > 0 {
			values = append(values, line)
		}
	}
	// The sets involved are small; insertion sort keeps this helper dependency
	// free from converter.go's implementation details.
	for index := 1; index < len(values); index++ {
		for cursor := index; cursor > 0 && values[cursor] < values[cursor-1]; cursor-- {
			values[cursor], values[cursor-1] = values[cursor-1], values[cursor]
		}
	}
	return values
}

func diagnoseK6ResidualSource(lines []int, diagnostics *diag.Collector, stats *ConvertStats, sourceFile string) {
	if len(lines) == 0 {
		return
	}
	stats.UnsupportedCalls = append(stats.UnsupportedCalls, "residual source logic")
	diagnostics.Add(
		diag.Warning,
		"k6 residual source logic",
		fmt.Sprintf("%s line %d", sourceFile, lines[0]),
		"JavaScript outside the proven static HTTP subset was omitted from the public draft; affected requests are blocked until the behavior is replaced",
	)
}

func disclosureSafeK6SourceReview(content string, hasResidual bool) (string, bool) {
	if hasResidual {
		lines := strings.Split(content, "\n")
		for index := range lines {
			if strings.TrimSpace(lines[index]) != "" {
				lines[index] = "// [SOURCE OMITTED FROM PUBLIC IMPORT REVIEW]"
			}
		}
		redacted := strings.Join(lines, "\n")
		return redacted, redacted != content
	}

	return content, false
}

func markK6SourceReviewRedacted(review *sourceview.Review, sourceFile string) {
	if review == nil {
		return
	}
	want := strings.ToLower(archiveingest.LogicalPath(sourceFile))
	wantBase := strings.ToLower(archiveingest.LogicalBase(sourceFile))
	for index := range review.SourceFiles {
		got := strings.ToLower(archiveingest.LogicalPath(review.SourceFiles[index].Path))
		if got == want || strings.ToLower(archiveingest.LogicalBase(got)) == wantBase {
			review.SourceFiles[index].Redacted = true
		}
	}
}
