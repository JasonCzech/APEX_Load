package http

import (
	"fmt"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

// PAC deliberately accepts a bounded, non-allocating language profile. No
// loops, recursion, objects, dynamic calls, property access, or string-building
// operators are accepted. Unsupported enterprise PAC scripts fail closed.
// This is also the hard allocation guard on hosts without usable RLIMIT_DATA.
func validatePACSubset(source string) error {
	if len(source) > maxPACBytes {
		return fmt.Errorf("PAC source exceeds limit")
	}
	program, err := parser.ParseFile(nil, "pac", source, 0)
	if err != nil {
		return err
	}
	if len(program.Body) != 1 {
		return fmt.Errorf("PAC requires a single FindProxyForURL function")
	}
	decl, ok := program.Body[0].(*ast.FunctionDeclaration)
	if !ok {
		return fmt.Errorf("PAC requires FindProxyForURL")
	}
	f := decl.Function
	if f.Name == nil || f.Name.Name.String() != "FindProxyForURL" || f.Async || f.Generator || f.ParameterList.Rest != nil || len(f.ParameterList.List) > 2 {
		return fmt.Errorf("unsupported PAC entrypoint")
	}
	bindings := map[string]bool{}
	bind := func(id *ast.Identifier) error {
		name := id.Name.String()
		if pacHelper(name) || name == "FindProxyForURL" || name == "arguments" || name == "eval" {
			return fmt.Errorf("reserved PAC binding")
		}
		bindings[name] = true
		return nil
	}
	for _, b := range f.ParameterList.List {
		id, ok := b.Target.(*ast.Identifier)
		if !ok || b.Initializer != nil {
			return fmt.Errorf("unsupported PAC parameter")
		}
		if err := bind(id); err != nil {
			return err
		}
	}
	nodes := 0
	var visit func(ast.Node, int) error
	visit = func(n ast.Node, depth int) error {
		if n == nil {
			return nil
		}
		nodes++
		if nodes > 4096 || depth > 64 {
			return fmt.Errorf("PAC complexity exceeds limits")
		}
		children := []ast.Node{}
		switch x := n.(type) {
		case *ast.BlockStatement:
			for _, s := range x.List {
				children = append(children, s)
			}
		case *ast.EmptyStatement:
		case *ast.IfStatement:
			children = append(children, x.Test, x.Consequent)
			if x.Alternate != nil {
				children = append(children, x.Alternate)
			}
		case *ast.ReturnStatement:
			if x.Argument == nil {
				return fmt.Errorf("PAC return requires value")
			}
			children = append(children, x.Argument)
		case *ast.VariableStatement:
			for _, b := range x.List {
				id, ok := b.Target.(*ast.Identifier)
				if !ok || b.Initializer == nil {
					return fmt.Errorf("unsupported PAC binding")
				}
				if err := bind(id); err != nil {
					return err
				}
				children = append(children, b.Initializer)
			}
		case *ast.StringLiteral:
			if len(x.Value.String()) > 4096 {
				return fmt.Errorf("PAC string exceeds limit")
			}
		case *ast.Identifier:
			if !bindings[x.Name.String()] {
				return fmt.Errorf("PAC global values are unavailable")
			}
		case *ast.BooleanLiteral, *ast.NumberLiteral, *ast.NullLiteral:
		case *ast.BinaryExpression:
			switch x.Operator.String() {
			case "==", "===", "!=", "!==", "<", ">", "<=", ">=", "&&", "||":
			default:
				return fmt.Errorf("unsupported PAC operator")
			}
			children = append(children, x.Left, x.Right)
		case *ast.UnaryExpression:
			if x.Operator.String() != "!" {
				return fmt.Errorf("unsupported PAC unary operator")
			}
			children = append(children, x.Operand)
		case *ast.ConditionalExpression:
			children = append(children, x.Test, x.Consequent, x.Alternate)
		case *ast.CallExpression:
			id, ok := x.Callee.(*ast.Identifier)
			if !ok {
				return fmt.Errorf("unsupported PAC call")
			}
			if !pacHelper(id.Name.String()) {
				return fmt.Errorf("unsupported PAC helper")
			}
			if len(x.ArgumentList) > 3 {
				return fmt.Errorf("too many PAC helper arguments")
			}
			for _, arg := range x.ArgumentList {
				children = append(children, arg)
			}
		default:
			return fmt.Errorf("unsupported PAC syntax %T", n)
		}
		for _, child := range children {
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(f.Body, 0)
}

func pacHelper(name string) bool {
	switch name {
	case "dnsResolve", "isPlainHostName", "dnsDomainIs", "localHostOrDomainIs", "dnsDomainLevels", "isResolvable", "shExpMatch", "isInNet":
		return true
	}
	return false
}
