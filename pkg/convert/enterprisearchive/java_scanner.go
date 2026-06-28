package enterprisearchive

import (
	"fmt"
	"regexp"
	"strings"
)

type javaClass struct {
	File     string
	Imports  []string
	Fields   []javaStatement
	Methods  map[string]javaMethod
	Unparsed []javaStatement
}

type javaMethod struct {
	Name       string
	File       string
	StartLine  int
	EndLine    int
	Body       string
	Statements []javaStatement
}

type javaStatement struct {
	Kind      string
	Text      string
	Header    string
	File      string
	StartLine int
	EndLine   int
	Children  []javaStatement
}

var javaMethodHeaderRe = regexp.MustCompile(`(?m)(?:^|[\r\n])\s*(?:@[A-Za-z_][A-Za-z0-9_.]*(?:\s*\([^{};]*\))?\s*)*(?:public|private|protected)?\s*(?:(?:static|final|synchronized|native|strictfp|abstract)\s+)*(?:<[^{};()]+>\s*)?[A-Za-z_$][A-Za-z0-9_$.]*(?:\s*<[^{};()]+>)?(?:\s*\[\])*\s+([A-Za-z_][A-Za-z0-9_]*)\s*\([^;{}]*\)\s*(?:throws\s+[^{;]+)?\s*\{`)

func parseJavaFile(filename string, content string) (javaClass, error) {
	cls := javaClass{
		File:    filename,
		Imports: parseJavaImports(content),
		Methods: map[string]javaMethod{},
	}
	matches := javaMethodHeaderRe.FindAllStringSubmatchIndex(content, -1)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		name := content[match[2]:match[3]]
		openBrace := strings.Index(content[match[0]:match[1]], "{")
		if openBrace < 0 {
			continue
		}
		open := match[0] + openBrace
		close := findMatchingJavaBrace(content, open)
		if close < 0 {
			cls.Unparsed = append(cls.Unparsed, javaStatement{
				Kind:      "unparsed",
				Text:      strings.TrimSpace(content[match[0]:]),
				Header:    strings.TrimSpace(content[match[0]:match[1]]),
				File:      filename,
				StartLine: javaLineForOffset(content, match[0]),
				EndLine:   javaLineForOffset(content, len(content)),
			})
			continue
		}
		body := content[open+1 : close]
		startLine := javaLineForOffset(content, match[0])
		endLine := javaLineForOffset(content, close)
		method := javaMethod{
			Name:      name,
			File:      filename,
			StartLine: startLine,
			EndLine:   endLine,
			Body:      body,
		}
		method.Statements = parseJavaStatements(filename, body, startLine)
		cls.Methods[name] = method
	}
	if len(cls.Methods) == 0 && len(cls.Unparsed) == 0 {
		return cls, fmt.Errorf("no Java methods found in %s", filename)
	}
	if len(cls.Unparsed) > 0 {
		return cls, fmt.Errorf("%s contains %d unparsed Java region(s) with unmatched braces", filename, len(cls.Unparsed))
	}
	return cls, nil
}

func parseJavaImports(content string) []string {
	imports := make([]string, 0)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "import ") && strings.HasSuffix(line, ";") {
			imports = append(imports, strings.TrimSuffix(strings.TrimPrefix(line, "import "), ";"))
		}
	}
	return imports
}

func parseJavaStatements(filename string, body string, baseLine int) []javaStatement {
	statements := make([]javaStatement, 0)
	start := 0
	parenDepth := 0
	bracketDepth := 0
	inLineComment := false
	inBlockComment := false
	inString := false
	var quote byte
	escaped := false

	for i := 0; i < len(body); i++ {
		ch := body[i]
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(body) && body[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				inString = false
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inString = true
			quote = ch
			continue
		}
		if ch == '/' && i+1 < len(body) {
			switch body[i+1] {
			case '/':
				inLineComment = true
				i++
				continue
			case '*':
				inBlockComment = true
				i++
				continue
			}
		}
		switch ch {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case ';':
			if parenDepth == 0 && bracketDepth == 0 {
				appendJavaStatement(&statements, filename, body, start, i+1, baseLine, "", nil)
				start = i + 1
			}
		case '{':
			if parenDepth == 0 && bracketDepth == 0 {
				close := findMatchingJavaBrace(body, i)
				if close < 0 {
					continue
				}
				header := strings.TrimSpace(body[start:i])
				children := parseJavaStatements(filename, body[i+1:close], baseLine+strings.Count(body[:i+1], "\n"))
				appendJavaStatement(&statements, filename, body, start, close+1, baseLine, header, children)
				i = close
				start = close + 1
			}
		}
	}
	appendJavaStatement(&statements, filename, body, start, len(body), baseLine, "", nil)
	return statements
}

func appendJavaStatement(statements *[]javaStatement, filename string, body string, start int, end int, baseLine int, header string, children []javaStatement) {
	if start < 0 {
		start = 0
	}
	if end > len(body) {
		end = len(body)
	}
	text := strings.TrimSpace(body[start:end])
	if text == "" {
		return
	}
	kind := "statement"
	if header != "" {
		kind = "block"
	}
	*statements = append(*statements, javaStatement{
		Kind:      kind,
		Text:      text,
		Header:    header,
		File:      filename,
		StartLine: baseLine + strings.Count(body[:start], "\n"),
		EndLine:   baseLine + strings.Count(body[:end], "\n"),
		Children:  children,
	})
}

func findMatchingJavaBrace(content string, open int) int {
	if open < 0 || open >= len(content) || content[open] != '{' {
		return -1
	}
	depth := 0
	inLineComment := false
	inBlockComment := false
	inString := false
	var quote byte
	escaped := false
	for i := open; i < len(content); i++ {
		ch := content[i]
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(content) && content[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				inString = false
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inString = true
			quote = ch
			continue
		}
		if ch == '/' && i+1 < len(content) {
			switch content[i+1] {
			case '/':
				inLineComment = true
				i++
				continue
			case '*':
				inBlockComment = true
				i++
				continue
			}
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func javaLineForOffset(content string, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(content) {
		offset = len(content)
	}
	return strings.Count(content[:offset], "\n") + 1
}
