package enterprisearchive

import (
	"fmt"
	"reflect"
	"strings"

	"modernc.org/cc/v4"
)

const enterpriseArchiveCPrelude = `
int __predefined_declarator;
`

// ParseActionFileAST parses an enterprise C/VUser action file with a full C AST
// and adapts top-level expression-statement calls into the converter's source
// function model.
func ParseActionFileAST(filename string, content string) (*ParsedAction, error) {
	normalized := normalizeEnterpriseArchiveCSource(content)
	ast, err := cc.Parse(&cc.Config{}, []cc.Source{
		{Name: "<enterprise-archive-compat.h>", Value: enterpriseArchiveCPrelude},
		{Name: filename, Value: normalized},
	})
	if err != nil {
		return nil, fmt.Errorf("parse enterprise C archive action %s: %w", filename, err)
	}

	parsed := &ParsedAction{
		Filename:  filename,
		Functions: make([]ParsedFunction, 0),
	}
	originalLines := strings.Split(content, "\n")

	walkCCNode(ast.TranslationUnit, func(node cc.Node) {
		stmt, ok := node.(*cc.ExpressionStatement)
		if !ok || stmt.ExpressionList == nil {
			return
		}
		block := strings.TrimSpace(cc.NodeSource(stmt.ExpressionList))
		if block == "" || !reFuncCall.MatchString(block) || isCControlCall(block) {
			return
		}
		if !strings.HasSuffix(block, ";") {
			block += ";"
		}
		startLine, endLine := nodeLineRange(stmt.ExpressionList)
		if startLine == 0 {
			startLine = stmt.Position().Line
		}
		if endLine == 0 {
			endLine = startLine
		}
		rawBlock := rawSourceBlock(originalLines, startLine, endLine)
		fn, parseErr := parseFunctionBlock(block, filename, startLine, endLine, rawBlock)
		if parseErr == nil {
			parsed.Functions = append(parsed.Functions, fn)
		}
	})

	return parsed, nil
}

func normalizeEnterpriseArchiveCSource(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#include") {
			lines[i] = ""
			continue
		}
		for _, name := range []string{"Action", "vuser_init", "vuser_end"} {
			if strings.TrimSpace(line) == name+"()" {
				indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				lines[i] = indent + "int " + name + "()"
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

func nodeLineRange(node cc.Node) (int, int) {
	tokens := cc.NodeTokens(node)
	startLine := 0
	endLine := 0
	for _, token := range tokens {
		pos := token.Position()
		if !pos.IsValid() || pos.Line == 0 {
			continue
		}
		if startLine == 0 || pos.Line < startLine {
			startLine = pos.Line
		}
		if pos.Line > endLine {
			endLine = pos.Line
		}
	}
	return startLine, endLine
}

func rawSourceBlock(lines []string, startLine int, endLine int) string {
	if startLine <= 0 || len(lines) == 0 {
		return ""
	}
	if endLine < startLine {
		endLine = startLine
	}
	start := startLine - 1
	if start >= len(lines) {
		return ""
	}
	end := endLine
	if end > len(lines) {
		end = len(lines)
	}
	return strings.TrimRight(strings.Join(lines[start:end], "\n"), "\n")
}

func walkCCNode(node cc.Node, visit func(cc.Node)) {
	seen := make(map[uintptr]struct{})
	var walkValue func(reflect.Value)
	walkValue = func(value reflect.Value) {
		if !value.IsValid() {
			return
		}
		if value.Kind() == reflect.Interface {
			if value.IsNil() {
				return
			}
			value = value.Elem()
		}
		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return
			}
			ptr := value.Pointer()
			if ptr != 0 {
				if _, ok := seen[ptr]; ok {
					return
				}
				seen[ptr] = struct{}{}
			}
		}
		if value.CanInterface() {
			if _, ok := value.Interface().(cc.Token); ok {
				return
			}
			if node, ok := value.Interface().(cc.Node); ok {
				visit(node)
			}
		}
		if value.Kind() == reflect.Pointer {
			value = value.Elem()
		}
		switch value.Kind() {
		case reflect.Struct:
			valueType := value.Type()
			for i := 0; i < value.NumField(); i++ {
				field := valueType.Field(i)
				if field.PkgPath != "" {
					continue
				}
				walkValue(value.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < value.Len(); i++ {
				walkValue(value.Index(i))
			}
		}
	}
	walkValue(reflect.ValueOf(node))
}
