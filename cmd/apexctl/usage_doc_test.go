package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestTopLevelSubcommandsDocumentedInUsageAndReadme(t *testing.T) {
	commands := collectTopLevelSubcommands(t)
	if len(commands) == 0 {
		t.Fatal("discovered zero top-level apexctl subcommands")
	}

	usageOutput := capturePrintUsage(t)
	usageCommands := parseUsageCommandSection(usageOutput)

	readmeBytes, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readmeText := string(readmeBytes)

	for _, cmd := range commands {
		if !usageCommands[cmd] {
			t.Errorf("top-level subcommand %q is handled in main.go but missing from printUsage() Commands block", cmd)
		}
		if cmd == "help" {
			continue
		}
		if !readmeMentionsSubcommand(readmeText, cmd) {
			t.Errorf("top-level subcommand %q is handled in main.go but missing from README.md", cmd)
		}
	}
}

func collectTopLevelSubcommands(t *testing.T) []string {
	t.Helper()

	discovered := make(map[string]bool, len(apexctlCommandHandlers)+2)
	for cmd := range apexctlCommandHandlers {
		discovered[cmd] = true
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ValueSpec:
			for i, name := range node.Names {
				if name.Name != "apexctlCommandHandlers" || i >= len(node.Values) {
					continue
				}
				comp, ok := node.Values[i].(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, elt := range comp.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if lit, ok := kv.Key.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if unquoted, err := strconv.Unquote(lit.Value); err == nil && unquoted != "" {
							discovered[unquoted] = true
						}
					}
				}
			}
		case *ast.FuncDecl:
			if node.Name == nil || node.Name.Name != "main" || node.Body == nil {
				return true
			}
			ast.Inspect(node.Body, func(inner ast.Node) bool {
				sw, ok := inner.(*ast.SwitchStmt)
				if !ok || !isOSArgsOne(sw.Tag) {
					return true
				}
				for _, stmt := range sw.Body.List {
					clause, ok := stmt.(*ast.CaseClause)
					if !ok {
						continue
					}
					for _, expr := range clause.List {
						lit, ok := expr.(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						unquoted, err := strconv.Unquote(lit.Value)
						if err != nil || unquoted == "" || strings.HasPrefix(unquoted, "-") {
							continue
						}
						discovered[unquoted] = true
					}
				}
				return false
			})
			return false
		}
		return true
	})

	out := make([]string, 0, len(discovered))
	for cmd := range discovered {
		out = append(out, cmd)
	}
	sort.Strings(out)
	return out
}

func isOSArgsOne(expr ast.Expr) bool {
	idx, ok := expr.(*ast.IndexExpr)
	if !ok {
		return false
	}
	sel, ok := idx.X.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != "Args" {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok || pkgIdent.Name != "os" {
		return false
	}
	lit, ok := idx.Index.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == "1"
}

func capturePrintUsage(t *testing.T) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	printUsage()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read captured usage: %v", err)
	}
	_ = r.Close()
	return buf.String()
}

func parseUsageCommandSection(usage string) map[string]bool {
	commands := make(map[string]bool)
	inCommands := false
	for _, rawLine := range strings.Split(usage, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "Commands:" {
			inCommands = true
			continue
		}
		if !inCommands {
			continue
		}
		if trimmed == "" {
			if len(commands) > 0 {
				break
			}
			continue
		}
		if !strings.HasPrefix(rawLine, " ") && !strings.HasPrefix(rawLine, "\t") {
			break
		}
		fields := strings.Fields(trimmed)
		if len(fields) > 0 {
			commands[fields[0]] = true
		}
	}
	return commands
}

func readmeMentionsSubcommand(readme string, cmd string) bool {
	patterns := []string{
		"apexctl " + cmd + " ",
		"apexctl " + cmd + "`",
		"apexctl " + cmd + "\n",
		"`" + cmd + "`",
	}
	for _, pattern := range patterns {
		if strings.Contains(readme, pattern) {
			return true
		}
	}
	return false
}
