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
	cLocalIntegerLiteralValue = regexp.MustCompile(`^\s*-?\d+\s*$`)
)

// cLocalCandidates returns the names declared exactly once as `int NAME =
// <integer>;` whose every write in content is a literal assignment. The AST
// walk still requires the declaration to sit inside a function.
//
// The scans below are linear passes over code, written out by hand. As
// regexps (\bint\s+NAME\s*=\s*-?\d+\s*; and the write patterns in
// cLocalHasOtherWrites) each began with \b, which leaves no literal prefix
// to search for, so every pattern stepped through every byte of the source,
// once per local. On a multi-megabyte preprocessed source that cost more than
// the C parse itself. The tests keep those regexps as the oracle.
func cLocalCandidates(content string) map[string]bool {
	code := cCodeOnly(content)
	declared := cLocalDeclarationCounts(code)
	candidates := map[string]bool{}
	for name, count := range declared {
		if count == 1 && !cLocalHasOtherWrites(code, name) {
			candidates[name] = true
		}
	}
	return candidates
}

// cLocalDeclarationCounts counts each `int NAME = <integer>;` in code.
func cLocalDeclarationCounts(code string) map[string]int {
	declared := map[string]int{}
	for index := 0; index < len(code); {
		if !isCIdentifierContinue(code[index]) {
			index++
			continue
		}
		end := cLocalWordEnd(code, index)
		if code[index:end] == "int" {
			if name, next, ok := cLocalDeclarationAfterInt(code, end); ok {
				declared[name]++
				index = next
				continue
			}
		}
		index = end
	}
	return declared
}

// cLocalDeclarationAfterInt matches `\s+NAME\s*=\s*-?\d+\s*;` at index, just
// past an `int` word, and returns NAME and the index past the semicolon.
func cLocalDeclarationAfterInt(code string, index int) (string, int, bool) {
	start := cLocalSkipSpace(code, index)
	if start == index || start == len(code) || !isCIdentifierStart(code[start]) {
		return "", 0, false
	}
	end := cLocalWordEnd(code, start)
	index = cLocalSkipSpace(code, end)
	if index == len(code) || code[index] != '=' {
		return "", 0, false
	}
	index = cLocalSkipSpace(code, index+1)
	if index < len(code) && code[index] == '-' {
		index++
	}
	digits := index
	for index < len(code) && code[index] >= '0' && code[index] <= '9' {
		index++
	}
	if index == digits {
		return "", 0, false
	}
	index = cLocalSkipSpace(code, index)
	if index == len(code) || code[index] != ';' {
		return "", 0, false
	}
	return code[start:end], index + 1, true
}

// cLocalHasOtherWrites reports whether code writes name other than through a
// literal assignment statement: NAME++ / ++NAME (and --), NAME op= for the
// compound operators, &NAME (not &&NAME), or NAME = anything else.
func cLocalHasOtherWrites(code, name string) bool {
	for from := 0; from < len(code); {
		offset := strings.Index(code[from:], name)
		if offset < 0 {
			return false
		}
		start := from + offset
		end := start + len(name)
		// No identifier can start inside this match, so search on from its end.
		from = end
		if (start > 0 && isCIdentifierContinue(code[start-1])) || (end < len(code) && isCIdentifierContinue(code[end])) {
			continue
		}
		before := start
		for before > 0 && cLocalSpace(code[before-1]) {
			before--
		}
		if before >= 2 && (code[before-2:before] == "++" || code[before-2:before] == "--") {
			return true
		}
		if before >= 1 && code[before-1] == '&' && (before == 1 || code[before-2] != '&') {
			return true
		}
		rest := code[cLocalSkipSpace(code, end):]
		if strings.HasPrefix(rest, "++") || strings.HasPrefix(rest, "--") || strings.HasPrefix(rest, "<<=") || strings.HasPrefix(rest, ">>=") {
			return true
		}
		if len(rest) >= 2 && rest[1] == '=' && strings.IndexByte("-+*/%&|^", rest[0]) >= 0 {
			return true
		}
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		rest = rest[1:]
		if strings.HasPrefix(rest, "=") {
			continue // NAME == value is a comparison
		}
		stop := strings.IndexAny(rest, ";),")
		if stop < 0 || rest[stop] != ';' || !cLocalIntegerLiteralValue.MatchString(rest[:stop]) || !cLocalWriteIsStatement(code[:start]) {
			return true
		}
	}
	return false
}

// cLocalSpace matches the regexp class \s, which the scans above replace.
func cLocalSpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\f' || ch == '\r'
}

func cLocalSkipSpace(code string, index int) int {
	for index < len(code) && cLocalSpace(code[index]) {
		index++
	}
	return index
}

func cLocalWordEnd(code string, index int) int {
	for index < len(code) && isCIdentifierContinue(code[index]) {
		index++
	}
	return index
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
