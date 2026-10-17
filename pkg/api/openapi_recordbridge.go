package api

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
)

// addSharedRecordingHandlerDefinitions follows only the explicit controller
// adapters to the shared recording service. A forwarding method does not consume
// an extra inference depth level or erase its real request and response body.
func addSharedRecordingHandlerDefinitions(apiDir string, candidates map[string][]*ast.FuncDecl, types map[string]ast.Expr, statuses map[*ast.FuncDecl]map[int]bool) error {
	forwarded := map[string]string{}
	for name, functions := range candidates {
		if target := sharedRecordingHandlerTarget(functions); target != "" {
			forwarded[name] = target
		}
	}
	// Isolated inference fixtures and API packages without recording adapters do
	// not need a sibling recording runtime to describe their own handlers.
	if len(forwarded) == 0 {
		return nil
	}
	packages, err := parser.ParseDir(token.NewFileSet(), filepath.Join(apiDir, "..", "recordbridge"), func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse shared recording handlers: %w", err)
	}
	packageAST := packages["recordbridge"]
	if packageAST == nil {
		return fmt.Errorf("parse shared recording handlers: recordbridge package is missing")
	}
	shared := map[string][]*ast.FuncDecl{}
	for _, file := range packageAST.Files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				shared[fn.Name.Name] = append(shared[fn.Name.Name], fn)
				annotated, err := annotatedOpenAPIResponseStatuses(fn.Doc)
				if err != nil {
					return err
				}
				statuses[fn] = annotated
			}
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE {
				for _, spec := range gen.Specs {
					typ := spec.(*ast.TypeSpec)
					if types[typ.Name.Name] == nil {
						types[typ.Name.Name] = typ.Type
					}
				}
			}
		}
	}
	for name, target := range forwarded {
		implementations := shared[target]
		if len(implementations) != 1 || receiverTypeName(implementations[0]) != "Service" {
			return fmt.Errorf("resolve shared recording handler %s: expected one Service.%s implementation", name, target)
		}
		// Keep annotations on the controller adapter (for callback-owned errors)
		// without adding them to unrelated users of the shared implementation.
		forwardedFunction := *implementations[0]
		combined := map[int]bool{}
		for status := range statuses[implementations[0]] {
			combined[status] = true
		}
		for status := range statuses[candidates[name][0]] {
			combined[status] = true
		}
		statuses[&forwardedFunction] = combined
		candidates[name] = []*ast.FuncDecl{&forwardedFunction}
	}
	for name, functions := range shared {
		if len(candidates[name]) == 0 {
			candidates[name] = functions
		}
	}
	return nil
}

// sharedRecordingHandlerTarget accepts only a single direct call through the
// controller's explicit service adapter. Other methods retain normal inference.
func sharedRecordingHandlerTarget(functions []*ast.FuncDecl) string {
	if len(functions) != 1 || functions[0].Body == nil || len(functions[0].Body.List) != 1 {
		return ""
	}
	statement, ok := functions[0].Body.List[0].(*ast.ExprStmt)
	if !ok {
		return ""
	}
	call, ok := statement.X.(*ast.CallExpr)
	if !ok {
		return ""
	}
	target, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	receiver, ok := target.X.(*ast.CallExpr)
	if !ok || len(receiver.Args) != 0 {
		return ""
	}
	factory, ok := receiver.Fun.(*ast.SelectorExpr)
	if !ok || factory.Sel.Name != "recordingHTTPService" {
		return ""
	}
	return target.Sel.Name
}
