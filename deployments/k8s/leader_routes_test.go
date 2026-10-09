package k8s_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// leaderOnlyRoute is a controller route whose handler refuses to act on a
// follower replica (HTTP 503) under HA.
type leaderOnlyRoute struct {
	Method  string
	Path    string
	Handler string
}

// leaderOnlyRoutes derives the leader-only routes from the controller source
// instead of a hand-kept list: a *Server method is leader-only when it calls
// requireExecutionLeader, tests a negated isExecutionLeader(), or returns
// errNotExecutionLeader, or when it calls another leader-only *Server method.
// Handler names map to routes through the generated endpoint catalog, which
// covers both registry and legacy registrations.
func leaderOnlyRoutes(t *testing.T) []leaderOnlyRoute {
	t.Helper()
	files, err := filepath.Glob("../../pkg/api/*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	bodies := make(map[string]*ast.BlockStmt)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil || len(fn.Recv.List) != 1 {
				continue
			}
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "Server" {
				bodies[fn.Name.Name] = fn.Body
			}
		}
	}
	gated := make(map[string]bool)
	calls := make(map[string][]string)
	for name, body := range bodies {
		ast.Inspect(body, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok {
					if sel.Sel.Name == "requireExecutionLeader" {
						gated[name] = true
					}
					if recv, ok := sel.X.(*ast.Ident); ok && recv.Name == "s" {
						if _, isMethod := bodies[sel.Sel.Name]; isMethod {
							calls[name] = append(calls[name], sel.Sel.Name)
						}
					}
				}
			case *ast.UnaryExpr:
				if call, ok := n.X.(*ast.CallExpr); ok && n.Op == token.NOT {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "isExecutionLeader" {
						gated[name] = true
					}
				}
			case *ast.Ident:
				if n.Name == "errNotExecutionLeader" {
					gated[name] = true
				}
			}
			return true
		})
	}
	for changed := true; changed; {
		changed = false
		for caller, callees := range calls {
			if gated[caller] {
				continue
			}
			for _, callee := range callees {
				if gated[callee] {
					gated[caller], changed = true, true
					break
				}
			}
		}
	}

	raw, err := os.ReadFile("../../docs/api-endpoint-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Routes []struct {
			Method      string `json:"method"`
			Path        string `json:"path"`
			OperationID string `json:"operationId"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatalf("decode endpoint catalog: %v", err)
	}
	var routes []leaderOnlyRoute
	for _, route := range catalog.Routes {
		if gated[route.OperationID] {
			routes = append(routes, leaderOnlyRoute{Method: route.Method, Path: route.Path, Handler: route.OperationID})
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Path+routes[i].Method < routes[j].Path+routes[j].Method })
	return routes
}

// activeIngressCovers reports whether an ingress path routed to the active
// controller covers routePath. Prefix paths match whole segments.
func activeIngressCovers(active map[string]string, routePath string) bool {
	for path, pathType := range active {
		if pathType == "Exact" {
			if routePath == path {
				return true
			}
			continue
		}
		if routePath == path || strings.HasPrefix(routePath, strings.TrimSuffix(path, "/")+"/") {
			return true
		}
	}
	return false
}

func TestLeaderOnlyRouteDiscoveryFindsKnownLeaderRoutes(t *testing.T) {
	routes := leaderOnlyRoutes(t)
	found := make(map[string]bool, len(routes))
	for _, route := range routes {
		found[route.Method+" "+route.Path] = true
	}
	// Spot checks across direct gates, negated checks and delegated helpers.
	for _, want := range []string{
		"POST /api/v1/tests/{id}/start",
		"DELETE /api/v1/projects/{id}",
		"POST /api/v1/fleet/generators/{id}/cordon",
		"POST /api/v1/fleet/pools",
	} {
		if !found[want] {
			t.Errorf("leader-only route discovery missed %s (found %d routes)", want, len(routes))
		}
	}
}
