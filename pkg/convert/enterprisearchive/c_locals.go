package enterprisearchive

import (
	"fmt"
	"regexp"
	"strings"
)

// VuGen scripts drive polling loops with an int local:
//
//	int assertionPassed = 0;
//	while (!assertionPassed) { ...; if (found) { assertionPassed = 1; } }
//
// A local qualifies when it is declared once in the file, inside a function,
// as `int NAME = <integer>`, and every write anywhere in the file is a plain
// `NAME = <integer>` assignment. Each write imports as lr_save_int into the
// clocal_NAME variable at its source position, and flow conditions read the
// local through atoi(lr_eval_string("{clocal_NAME}")), a form the condition
// translator already handles. Any other write (++, +=, &NAME, a computed
// value) leaves the local unqualified, so its conditions stay untranslated
// and keep the existing review warning.

const (
	cLocalVariablePrefix = "clocal_"
	// cLocalLoopMaxIterations bounds a loop driven by a C local. The source
	// loop has no bound; this matches the engine's condition-loop safety cap
	// but states it on the imported loop so the limit is visible in review.
	cLocalLoopMaxIterations = 1000
)

var (
	cLocalDeclarationRe       = regexp.MustCompile(`^int\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(-?\d+)\s*;?$`)
	cLocalAssignmentRe        = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(-?\d+)\s*;?$`)
	cLocalDeclarationsRe      = regexp.MustCompile(`\bint\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*-?\d+\s*;`)
	cLocalIntegerLiteralValue = regexp.MustCompile(`^\s*-?\d+\s*$`)
)

// cLocalCandidates returns the names declared exactly once as `int NAME =
// <integer>;` whose every write in content is a literal assignment. The AST
// walk still requires the declaration to sit inside a function.
func cLocalCandidates(content string) map[string]bool {
	code := cCodeOnly(content)
	declared := map[string]int{}
	for _, match := range cLocalDeclarationsRe.FindAllStringSubmatch(code, -1) {
		declared[match[1]]++
	}
	candidates := map[string]bool{}
	for name, count := range declared {
		if count == 1 && !cLocalHasOtherWrites(code, name) {
			candidates[name] = true
		}
	}
	return candidates
}

func cLocalHasOtherWrites(code, name string) bool {
	quoted := regexp.QuoteMeta(name)
	for _, pattern := range []string{
		`\b` + quoted + `\s*(\+\+|--)`,
		`(\+\+|--)\s*` + quoted + `\b`,
		`\b` + quoted + `\s*(<<|>>|[-+*/%&|^])=`,
		`(^|[^&])&\s*` + quoted + `\b`,
	} {
		if regexp.MustCompile(pattern).MatchString(code) {
			return true
		}
	}
	for _, location := range regexp.MustCompile(`\b`+quoted+`\s*=`).FindAllStringIndex(code, -1) {
		rest := code[location[1]:]
		if strings.HasPrefix(rest, "=") {
			continue // NAME == value is a comparison
		}
		stop := strings.IndexAny(rest, ";),")
		if stop < 0 || rest[stop] != ';' || !cLocalIntegerLiteralValue.MatchString(rest[:stop]) || !cLocalWriteIsStatement(code[:location[0]]) {
			return true
		}
	}
	return false
}

// cLocalWriteIsStatement reports whether a write starting after before is its
// own statement (or the declaration), the only writes the AST walk emits. An
// assignment inside a condition, a call argument, a for header or a chained
// assignment is an expression the import cannot reproduce.
func cLocalWriteIsStatement(before string) bool {
	before = strings.TrimRight(before, " \t\r\n")
	if before == "" {
		return true
	}
	switch before[len(before)-1] {
	case ';', '{', '}', ')', ':':
		// ')' ends an if/while/for header guarding the statement; ':' a label.
		return true
	}
	for _, keyword := range []string{"int", "else", "do"} {
		if strings.HasSuffix(before, keyword) && (len(before) == len(keyword) || !isCIdentifierContinue(before[len(before)-len(keyword)-1])) {
			if keyword == "int" {
				// The declaration itself must start a statement: a for-init
				// declaration runs once, not where its loop body would emit it.
				return cLocalWriteIsStatement(strings.TrimSuffix(before, keyword))
			}
			return true
		}
	}
	return false
}

// cCodeOnly removes comments (with the parser's stripCComments) and blanks the
// contents of string and character literals, keeping one line per source line.
func cCodeOnly(content string) string {
	inComment := false
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		lines[index] = blankCStringLiterals(stripCComments(line, &inComment))
	}
	return strings.Join(lines, "\n")
}

func blankCStringLiterals(line string) string {
	out := []byte(line)
	for index := 0; index < len(out); index++ {
		if out[index] != '"' && out[index] != '\'' {
			continue
		}
		quote := out[index]
		for index++; index < len(out) && out[index] != quote; index++ {
			if out[index] == '\\' && index+1 < len(out) {
				out[index] = ' '
				index++
			}
			out[index] = ' '
		}
	}
	return string(out)
}

// cLocalSaveCall renders a qualified local write as the lr_save_int call it
// imports as.
func cLocalSaveCall(name, value string) string {
	return fmt.Sprintf(`lr_save_int(%s, "%s%s");`, value, cLocalVariablePrefix, name)
}

// expandCLocalReferences rewrites references to qualified locals in a raw C
// condition or loop header into the atoi(lr_eval_string(...)) form, leaving
// string literals, member accesses and function calls untouched.
func expandCLocalReferences(text string, locals map[string]bool) string {
	if len(locals) == 0 {
		return text
	}
	var out strings.Builder
	for index := 0; index < len(text); {
		c := text[index]
		if c == '"' || c == '\'' {
			end := index + 1
			for end < len(text) && text[end] != c {
				if text[end] == '\\' {
					end++
				}
				end++
			}
			if end < len(text) {
				end++
			}
			out.WriteString(text[index:min(end, len(text))])
			index = end
			continue
		}
		if !isCIdentifierStart(c) {
			out.WriteByte(c)
			index++
			continue
		}
		end := index + 1
		for end < len(text) && isCIdentifierContinue(text[end]) {
			end++
		}
		token := text[index:end]
		rest := strings.TrimLeft(text[end:], " \t")
		before := strings.TrimRight(text[:index], " \t")
		member := strings.HasSuffix(before, ".") || strings.HasSuffix(before, "->")
		if locals[token] && !member && !strings.HasPrefix(rest, "(") {
			out.WriteString(`(atoi(lr_eval_string("{` + cLocalVariablePrefix + token + `}")))`)
		} else {
			out.WriteString(token)
		}
		index = end
	}
	return out.String()
}

func expandCLocalFrames(frames []string, locals map[string]bool) []string {
	if len(locals) == 0 || len(frames) == 0 {
		return frames
	}
	out := make([]string, len(frames))
	for index, frame := range frames {
		out[index] = expandCLocalReferences(frame, locals)
	}
	return out
}
