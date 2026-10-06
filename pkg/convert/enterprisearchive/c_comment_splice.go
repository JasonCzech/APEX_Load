package enterprisearchive

import (
	"fmt"
	"strings"
)

// maxSplicedCommentWarnings bounds the per-file warnings; the remainder is
// reported as a count.
const maxSplicedCommentWarnings = 20

// CSplicedCommentLine is a source line that C reads as part of the // comment
// on an earlier line, because that comment ends in a backslash: line splicing
// happens before comments are recognized. LoadRunner never runs code on such a
// line, and neither does the import, but nothing in the editor shows it as a
// comment. VuGen scripts hit this when a response snippet pasted as a comment
// ends inside an escaped quote.
type CSplicedCommentLine struct {
	LineNumber  int
	CommentLine int
	Function    string
}

// findSplicedCommentLines returns the lines holding code that a backslash-
// continued // comment swallows. Lines that are blank or are comments in their
// own right are not reported: splicing does not change their meaning.
func findSplicedCommentLines(content string) []CSplicedCommentLine {
	var found []CSplicedCommentLine
	inBlockComment := false
	commentLine := 0 // the line whose // comment continues, or 0
	for index, line := range strings.Split(content, "\n") {
		line = strings.TrimSuffix(line, "\r")
		lineNumber := index + 1
		if commentLine > 0 {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
				entry := CSplicedCommentLine{LineNumber: lineNumber, CommentLine: commentLine}
				if match := reFuncCall.FindStringSubmatch(trimmed); len(match) > 1 {
					entry.Function = match[1]
				}
				found = append(found, entry)
			}
			if !strings.HasSuffix(line, `\`) {
				commentLine = 0
			}
			continue
		}
		if lineCommentStart(line, &inBlockComment) && strings.HasSuffix(line, `\`) {
			commentLine = lineNumber
		}
	}
	return found
}

// lineCommentStart reports whether line has a // comment outside string and
// character literals and block comments, carrying block-comment state across
// lines.
func lineCommentStart(line string, inBlockComment *bool) bool {
	var quote byte
	for index := 0; index < len(line); index++ {
		ch := line[index]
		switch {
		case *inBlockComment:
			if ch == '*' && index+1 < len(line) && line[index+1] == '/' {
				*inBlockComment = false
				index++
			}
		case quote != 0:
			if ch == '\\' {
				index++
			} else if ch == quote {
				quote = 0
			}
		case ch == '"' || ch == '\'':
			quote = ch
		case ch == '/' && index+1 < len(line) && line[index+1] == '/':
			return true
		case ch == '/' && index+1 < len(line) && line[index+1] == '*':
			*inBlockComment = true
			index++
		}
	}
	return false
}

func splicedCommentLineSet(lines []CSplicedCommentLine) map[int]bool {
	if len(lines) == 0 {
		return nil
	}
	set := make(map[int]bool, len(lines))
	for _, line := range lines {
		set[line.LineNumber] = true
	}
	return set
}

func appendSplicedCommentWarnings(result *ConvertResult, actions []*ParsedAction) {
	if result == nil {
		return
	}
	for _, action := range actions {
		if action == nil {
			continue
		}
		for index, line := range action.SplicedCommentLines {
			if index == maxSplicedCommentWarnings {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %d more lines are inside backslash-continued // comments and do not run.", action.Filename, len(action.SplicedCommentLines)-index))
				break
			}
			code := "code"
			if line.Function != "" {
				code = line.Function
			}
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d: %s does not run: the // comment on line %d ends in a backslash, which continues the comment onto this line. LoadRunner skips it too; remove the trailing backslash if it should run.", action.Filename, line.LineNumber, code, line.CommentLine))
		}
	}
}
