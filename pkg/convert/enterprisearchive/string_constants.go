package enterprisearchive

import (
	"regexp"
	"strings"
)

// Source tooling moves oversized request payloads out of action files into
// companion headers as C string constants (for example
// `const char * const body_variable_1 = "Body=...";`) and references them as
// bare identifier arguments in request calls. The lexical parser only
// understands quoted arguments, so those payloads vanish silently unless the
// constants are inlined back into the call sites before parsing.

var sourceStringConstantDeclRe = regexp.MustCompile(`const\s+char\s*\*\s*(?:const\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)

// collectSourceStringConstants scans every C/header file in the archive for
// string-constant declarations and returns each constant name mapped to a
// single quoted C string literal (adjacent literal fragments merged) that can
// be substituted verbatim as a call argument.
func collectSourceStringConstants(files map[string]string) map[string]string {
	constants := make(map[string]string)
	for name, content := range files {
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".c") && !strings.HasSuffix(lower, ".h") {
			continue
		}
		for _, match := range sourceStringConstantDeclRe.FindAllStringSubmatchIndex(content, -1) {
			constName := content[match[2]:match[3]]
			literal, ok := scanAdjacentStringLiterals(content[match[1]:])
			if !ok {
				continue
			}
			constants[constName] = literal
		}
	}
	return constants
}

// scanAdjacentStringLiterals reads one or more adjacent C string literals
// (separated only by whitespace) terminated by a semicolon and merges them
// into a single quoted literal. It returns ok=false when the declaration is
// not a pure string-literal initializer.
func scanAdjacentStringLiterals(src string) (string, bool) {
	var merged strings.Builder
	merged.WriteByte('"')
	i := 0
	sawLiteral := false
	for i < len(src) {
		switch c := src[i]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '"':
			i++
			for i < len(src) {
				if src[i] == '\\' && i+1 < len(src) {
					merged.WriteString(src[i : i+2])
					i += 2
					continue
				}
				if src[i] == '"' {
					break
				}
				merged.WriteByte(src[i])
				i++
			}
			if i >= len(src) {
				return "", false
			}
			i++ // closing quote
			sawLiteral = true
		case c == ';':
			if !sawLiteral {
				return "", false
			}
			merged.WriteByte('"')
			return merged.String(), true
		default:
			return "", false
		}
	}
	return "", false
}

// inlineSourceStringConstants replaces bare identifier arguments that name a
// known string constant with the constant's quoted literal so the argument
// parser sees a normal string argument.
func inlineSourceStringConstants(content string, constants map[string]string) string {
	if len(constants) == 0 {
		return content
	}
	anyPresent := false
	for name := range constants {
		if strings.Contains(content, name) {
			anyPresent = true
			break
		}
	}
	if !anyPresent {
		return content
	}

	var out strings.Builder
	out.Grow(len(content))
	// prevDelim tracks the last meaningful code character emitted, so an
	// identifier counts as an argument only between '('/',' and ','/')'.
	prevDelim := byte(0)

	i := 0
	for i < len(content) {
		c := content[i]
		switch {
		case c == '/' && i+1 < len(content) && content[i+1] == '/':
			// Line comment: copy through verbatim.
			end := strings.IndexByte(content[i:], '\n')
			if end < 0 {
				out.WriteString(content[i:])
				i = len(content)
				continue
			}
			out.WriteString(content[i : i+end])
			i += end
		case c == '/' && i+1 < len(content) && content[i+1] == '*':
			end := strings.Index(content[i+2:], "*/")
			if end < 0 {
				out.WriteString(content[i:])
				i = len(content)
				continue
			}
			out.WriteString(content[i : i+2+end+2])
			i += 2 + end + 2
		case c == '"' || c == '\'':
			// String/char literal: copy through with escape handling.
			quote := c
			j := i + 1
			for j < len(content) {
				if content[j] == '\\' && j+1 < len(content) {
					j += 2
					continue
				}
				if content[j] == quote {
					j++
					break
				}
				j++
			}
			out.WriteString(content[i:j])
			prevDelim = quote
			i = j
		case isSourceIdentByte(c) && !isSourceDigit(c):
			j := i + 1
			for j < len(content) && isSourceIdentByte(content[j]) {
				j++
			}
			ident := content[i:j]
			if literal, ok := constants[ident]; ok && (prevDelim == '(' || prevDelim == ',') {
				// Only substitute when the identifier stands alone as a call
				// argument: the next meaningful char must close or continue
				// the argument list.
				k := j
				for k < len(content) && (content[k] == ' ' || content[k] == '\t' || content[k] == '\r' || content[k] == '\n') {
					k++
				}
				if k < len(content) && (content[k] == ',' || content[k] == ')') {
					out.WriteString(literal)
					prevDelim = '"'
					i = j
					continue
				}
			}
			out.WriteString(ident)
			prevDelim = ident[len(ident)-1]
			i = j
		default:
			out.WriteByte(c)
			if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
				prevDelim = c
			}
			i++
		}
	}
	return out.String()
}

func isSourceIdentByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isSourceDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
