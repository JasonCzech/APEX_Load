package api

// This file derives the portable parts of each HTTP contract from handler ASTs.
// Route registration remains the source of truth for method/path/access metadata;
// handler inference adds query names, request media, and response statuses without
// requiring a second 486-route catalog to be maintained by hand. Explicit schemas
// in openapi_gen.go take precedence when a payload needs a stronger contract.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const maxOpenAPIHandlerCallDepth = 4

type handlerContractAnalysis struct {
	queryNames       map[string]bool
	repeatedQueries  map[string]bool
	headerNames      map[string]bool
	repeatedHeaders  map[string]bool
	formValues       map[string]bool
	formFiles        map[string]bool
	statuses         map[int]bool
	responseMedia    map[string]bool
	jsonRequest      bool
	optionalJSONBody bool
	multipartRequest bool
	rawRequest       bool
	jsonResponse     bool
	requestSchema    *oasSchema
	responseSchemas  map[int]oasSchema
}

func newHandlerContractAnalysis() *handlerContractAnalysis {
	return &handlerContractAnalysis{
		queryNames:      map[string]bool{},
		repeatedQueries: map[string]bool{},
		headerNames:     map[string]bool{},
		repeatedHeaders: map[string]bool{},
		formValues:      map[string]bool{},
		formFiles:       map[string]bool{},
		statuses:        map[int]bool{},
		responseMedia:   map[string]bool{},
		responseSchemas: map[int]oasSchema{},
	}
}

func inferRouteContracts(serverGoPath string, routes []discoveredRoute) (map[string]inferredRouteContract, error) {
	dir := filepath.Dir(serverGoPath)
	packages, err := parser.ParseDir(token.NewFileSet(), dir, func(info fs.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("parse API handlers in %s: %w", dir, err)
	}

	functionCandidates := map[string][]*ast.FuncDecl{}
	typeDefinitions := map[string]ast.Expr{}
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && fn.Body != nil {
					functionCandidates[fn.Name.Name] = append(functionCandidates[fn.Name.Name], fn)
				}
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if ok {
						typeDefinitions[typeSpec.Name.Name] = typeSpec.Type
					}
				}
			}
		}
	}
	functions := map[string]*ast.FuncDecl{}
	functionResults := map[string][]ast.Expr{}
	for name, candidates := range functionCandidates {
		if len(candidates) == 1 {
			functions[name] = candidates[0]
			functionResults[name] = functionResultTypes(candidates[0])
			continue
		}
		// Bare selector names are not type-safe: many receiver types expose Run,
		// Get, Start, and similar methods. Never choose one based on randomized
		// package/file map iteration. Route handlers are known to be Server methods,
		// so retain that single unambiguous root while declining result inference for
		// every ambiguous method name.
		var serverHandler *ast.FuncDecl
		for _, candidate := range candidates {
			if receiverTypeName(candidate) == "Server" && strings.HasPrefix(name, "handle") {
				if serverHandler != nil {
					serverHandler = nil
					break
				}
				serverHandler = candidate
			}
		}
		if serverHandler != nil {
			functions[name] = serverHandler
		}
	}

	contracts := make(map[string]inferredRouteContract, len(routes))
	for _, route := range routes {
		analysis := newHandlerContractAnalysis()
		if route.Handler != "" {
			inspectHandlerContract(route.Handler, functions, functionResults, typeDefinitions, analysis, map[string]bool{}, 0)
		}
		contracts[route.Method+" "+route.Path] = inferredContractForAnalysis(route, analysis)
	}
	return contracts, nil
}

func receiverTypeName(fn *ast.FuncDecl) string {
	if fn == nil || fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if pointer, ok := expr.(*ast.StarExpr); ok {
		expr = pointer.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func inspectHandlerContract(name string, functions map[string]*ast.FuncDecl, functionResults map[string][]ast.Expr, typeDefinitions map[string]ast.Expr, analysis *handlerContractAnalysis, visiting map[string]bool, depth int) {
	if depth > maxOpenAPIHandlerCallDepth || visiting[name] {
		return
	}
	fn := functions[name]
	if fn == nil || fn.Body == nil {
		return
	}
	visiting[name] = true
	defer delete(visiting, name)

	queryVariables := findQueryVariables(fn.Body)
	variableTypes := findLocalVariableTypes(fn.Body, functionResults)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if index, ok := node.(*ast.IndexExpr); ok && receiverIsQuery(index.X, queryVariables) {
			if value, ok := stringLiteral(index.Index); ok && plausibleQueryName(value) {
				analysis.queryNames[value] = true
				analysis.repeatedQueries[value] = true
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		callName := calledFunctionName(call.Fun)

		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && len(call.Args) > 0 {
			if (selector.Sel.Name == "Get" || selector.Sel.Name == "Has" || selector.Sel.Name == "Values") && receiverIsQuery(selector.X, queryVariables) {
				if value, ok := stringLiteral(call.Args[0]); ok && plausibleQueryName(value) {
					analysis.queryNames[value] = true
					if selector.Sel.Name == "Values" {
						analysis.repeatedQueries[value] = true
					}
				}
			}
			if (selector.Sel.Name == "Get" || selector.Sel.Name == "Values") && receiverIsRequestHeader(selector.X) {
				if value, ok := stringLiteral(call.Args[0]); ok && documentableRequestHeader(value) {
					analysis.headerNames[value] = true
					if selector.Sel.Name == "Values" {
						analysis.repeatedHeaders[value] = true
					}
				}
			}
			if selector.Sel.Name == "FormValue" {
				if value, ok := stringLiteral(call.Args[0]); ok {
					analysis.multipartRequest = true
					analysis.formValues[value] = true
				}
			}
			if selector.Sel.Name == "FormFile" {
				if value, ok := stringLiteral(call.Args[0]); ok {
					analysis.multipartRequest = true
					analysis.formFiles[value] = true
				}
			}
		}

		if strings.Contains(strings.ToLower(callName), "query") {
			for _, arg := range call.Args {
				if value, ok := stringLiteral(arg); ok && plausibleQueryName(value) {
					analysis.queryNames[value] = true
				}
			}
		}
		if callName == "parseLimitOffsetQuery" {
			analysis.queryNames["limit"] = true
			analysis.queryNames["offset"] = true
		}

		switch callName {
		case "decodeJSONBody", "decodeRequestBody":
			analysis.jsonRequest = true
			setInferredRequestSchema(call, variableTypes, typeDefinitions, analysis)
		case "decodeOptionalJSONBody":
			analysis.jsonRequest = true
			analysis.optionalJSONBody = true
			setInferredRequestSchema(call, variableTypes, typeDefinitions, analysis)
		case "ParseMultipartForm", "MultipartReader":
			analysis.multipartRequest = true
		case "NewDecoder":
			if expressionContainsRequestBody(call) {
				analysis.jsonRequest = true
			}
		case "ReadAll":
			if expressionContainsRequestBody(call) {
				analysis.rawRequest = true
			}
		case "writeJSON":
			analysis.jsonResponse = true
			if len(call.Args) > 1 {
				if status, ok := openAPIHTTPStatus(call.Args[1]); ok {
					analysis.statuses[status] = true
					setInferredResponseSchema(call, status, variableTypes, functionResults, typeDefinitions, analysis)
				}
			}
		case "writeError":
			analysis.jsonResponse = true
			if len(call.Args) > 1 {
				if status, ok := openAPIHTTPStatus(call.Args[1]); ok {
					analysis.statuses[status] = true
				}
			}
		case "Error":
			if len(call.Args) > 2 {
				if status, ok := openAPIHTTPStatus(call.Args[2]); ok {
					analysis.statuses[status] = true
				}
			}
		case "WriteHeader":
			if len(call.Args) > 0 {
				if status, ok := openAPIHTTPStatus(call.Args[0]); ok {
					analysis.statuses[status] = true
				}
			}
		case "Set", "Add":
			if len(call.Args) > 1 {
				key, keyOK := stringLiteral(call.Args[0])
				value, valueOK := stringLiteral(call.Args[1])
				if keyOK && valueOK && strings.EqualFold(key, "Content-Type") {
					analysis.responseMedia[normalizeMediaType(value)] = true
				}
			}
		}

		if depth < maxOpenAPIHandlerCallDepth && functions[callName] != nil && callPassesRequest(call) && shouldInspectCalledContract(callName) {
			inspectHandlerContract(callName, functions, functionResults, typeDefinitions, analysis, visiting, depth+1)
		}
		return true
	})
}

func shouldInspectCalledContract(name string) bool {
	// Follow handler delegation and focused query parsers. Broad request/auth
	// resolution helpers deliberately dispatch across many unrelated route
	// lookups and would contaminate one operation with another operation's body.
	return strings.HasPrefix(name, "handle") ||
		strings.Contains(strings.ToLower(name), "query") ||
		strings.HasPrefix(name, "parse") ||
		strings.HasPrefix(name, "decode") ||
		strings.HasPrefix(name, "read") ||
		strings.HasPrefix(name, "serve") ||
		strings.HasPrefix(name, "write") ||
		strings.HasPrefix(name, "wants")
}

func inferredContractForAnalysis(route discoveredRoute, analysis *handlerContractAnalysis) inferredRouteContract {
	headerParameters := inferredHeaderParameters(analysis)
	responses := inferredResponses(route, analysis)
	if isScheduleUpdateRoute(route.Method, route.Path) {
		headerParameters = appendOrReplaceHeaderParameter(headerParameters, oasParameter{
			Name:        "If-Match",
			In:          "header",
			Description: "Required unless expected_revision is supplied in the JSON body. Must identify the positive current schedule revision; weak ETag syntax is accepted.",
			Required:    false,
			Schema:      oasParamSchema{Type: "string"},
		})
		if responses == nil {
			responses = map[string]oasResponse{}
		}
		responses["409"] = jsonErrorResponse("The schedule changed after the supplied revision was read, or an active occurrence prevents the update.")
		responses["428"] = jsonErrorResponse("Supply either expected_revision in the JSON body or the If-Match header.")
	}
	return inferredRouteContract{
		QueryParameters:  inferredQueryParameters(route, analysis),
		HeaderParameters: headerParameters,
		RequestBody:      inferredRequestBody(route, analysis),
		Responses:        responses,
	}
}

func isScheduleUpdateRoute(method, path string) bool {
	if method != http.MethodPut {
		return false
	}
	return path == "/api/v1/scenarios/{id}/schedules/{schedule_id}" ||
		path == "/api/v1/execution-plans/{id}/schedules/{schedule_id}"
}

func appendOrReplaceHeaderParameter(parameters []oasParameter, replacement oasParameter) []oasParameter {
	for index := range parameters {
		if strings.EqualFold(parameters[index].Name, replacement.Name) {
			parameters[index] = replacement
			sort.Slice(parameters, func(i, j int) bool {
				return strings.ToLower(parameters[i].Name) < strings.ToLower(parameters[j].Name)
			})
			return parameters
		}
	}
	parameters = append(parameters, replacement)
	sort.Slice(parameters, func(i, j int) bool {
		return strings.ToLower(parameters[i].Name) < strings.ToLower(parameters[j].Name)
	})
	return parameters
}

func inferredHeaderParameters(analysis *handlerContractAnalysis) []oasParameter {
	parameters := make([]oasParameter, 0, len(analysis.headerNames))
	for name := range analysis.headerNames {
		schema := oasParamSchema{Type: "string"}
		if analysis.repeatedHeaders[name] {
			item := schema
			schema = oasParamSchema{Type: "array", Items: &item}
		}
		parameters = append(parameters, oasParameter{
			Name:        name,
			In:          "header",
			Description: "Handler-read request header; whether it is required can depend on authentication mode or operation state.",
			Required:    false,
			Schema:      schema,
		})
	}
	sort.Slice(parameters, func(i, j int) bool {
		return strings.ToLower(parameters[i].Name) < strings.ToLower(parameters[j].Name)
	})
	return parameters
}

func inferredQueryParameters(route discoveredRoute, analysis *handlerContractAnalysis) []oasParameter {
	parameters := make([]oasParameter, 0, len(analysis.queryNames))
	for name := range analysis.queryNames {
		schema := queryParameterSchema(name, analysis.repeatedQueries[name])
		parameters = append(parameters, oasParameter{
			Name:        name,
			In:          "query",
			Description: "Handler-defined query parameter.",
			Required:    requiredQueryParameter(route.Method, route.Path, name),
			Schema:      schema,
		})
	}
	sort.Slice(parameters, func(i, j int) bool { return parameters[i].Name < parameters[j].Name })
	return parameters
}

func queryParameterSchema(name string, repeated bool) oasParamSchema {
	integerNames := map[string]bool{
		"limit": true, "offset": true, "page": true, "page_size": true, "sample_limit": true,
		"top": true, "window_seconds": true, "bucket_seconds": true, "max_points": true,
		"step_seconds": true, "retention_days": true, "max_events": true, "max_destinations": true,
		"min_bytes": true, "max_bytes": true, "min_duration_ms": true, "max_duration_ms": true,
	}
	booleanNames := map[string]bool{
		"include_archived": true, "include_deleted": true, "include_inactive": true,
		"include_steps": true, "include_evidence": true, "force": true, "download": true,
		"success": true, "raw": true, "scrub": true, "requests": true, "include_requests": true,
		"include_excluded": true, "redact_sensitive": true, "include_static_assets": true,
		"salesforce_native": true, "agentforce_transform": true, "sandbox": true, "capacity": true,
	}
	dateTimeNames := map[string]bool{
		"created_after": true, "created_before": true, "time_from": true, "time_to": true,
		"from": true, "to": true, "start": true, "end": true,
	}
	item := oasParamSchema{Type: "string"}
	if integerNames[name] {
		item.Type = "integer"
		item.Format = "int64"
	} else if booleanNames[name] {
		item.Type = "boolean"
	} else if dateTimeNames[name] {
		item.Format = "date-time"
	}
	if repeated {
		return oasParamSchema{Type: "array", Items: &item}
	}
	return item
}

func requiredQueryParameter(method, path, name string) bool {
	required := map[string]map[string]bool{
		"GET /api/v1/tests/{id}/errors/evidence":                   {"signature": true},
		"GET /api/v1/applications/{id}/load-comparison":            {"scope": true},
		"GET /api/v1/library/catalog":                              {"kind": true},
		"GET /api/v1/library/remotes/{id}/catalog":                 {"kind": true},
		"GET /api/v1/auth/effective-access/principal":              {"principal_type": true, "principal_id": true},
		"GET /api/v1/browser-recorder/sessions/{id}/storage-state": {},
	}
	return required[method+" "+path][name]
}

func inferredRequestBody(route discoveredRoute, analysis *handlerContractAnalysis) *oasRequestBody {
	if analysis.multipartRequest {
		properties := map[string]oasSchema{}
		for name := range analysis.formValues {
			properties[name] = oasSchema{Type: "string"}
		}
		for name := range analysis.formFiles {
			properties[name] = oasSchema{Type: "string", Format: "binary"}
		}
		additional := true
		return &oasRequestBody{
			Required: true,
			Content: map[string]oasMediaType{
				"multipart/form-data": {Schema: oasSchema{Type: "object", Properties: properties, AdditionalProperties: &additional}},
			},
		}
	}
	if analysis.jsonRequest {
		additional := true
		schema := oasSchema{Type: "object", Description: "Handler-defined JSON request object.", AdditionalProperties: &additional}
		if analysis.requestSchema != nil {
			schema = *analysis.requestSchema
		}
		schema.Required = mergeOpenAPIRequiredFields(
			schema.Required,
			explicitRequestRequiredFields(route.Method, route.Path, schema.Properties),
		)
		if isScheduleUpdateRoute(route.Method, route.Path) {
			if expectedRevision, ok := schema.Properties["expected_revision"]; ok {
				expectedRevision.Description = "Required unless the If-Match header is supplied. Must be the positive current schedule revision."
				schema.Properties["expected_revision"] = expectedRevision
			}
		}
		return &oasRequestBody{
			Required: !analysis.optionalJSONBody,
			Content: map[string]oasMediaType{
				"application/json": {Schema: schema},
			},
		}
	}
	if analysis.rawRequest {
		return &oasRequestBody{
			Required: true,
			Content: map[string]oasMediaType{
				"application/octet-stream": {Schema: oasSchema{Type: "string", Format: "binary"}},
			},
		}
	}
	return nil
}

func explicitRequestRequiredFields(method, path string, properties map[string]oasSchema) []string {
	declared := map[string][]string{
		"POST /api/v1/scenarios":                                                                        {"name", "duration", "groups"},
		"PUT /api/v1/scenarios/{id}":                                                                    {"name", "duration", "groups"},
		"POST /api/v1/scenarios/{id}/schedules":                                                         {"name", "cron_expr"},
		"PUT /api/v1/scenarios/{id}/schedules/{schedule_id}":                                            {"name", "cron_expr"},
		"POST /api/v1/scenarios/{id}/schedules/preview":                                                 {"cron_expr"},
		"POST /api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences/{occurrence_id}/resolve":       {"action"},
		"POST /api/v1/execution-plans/{id}/schedules":                                                   {"cron_expr"},
		"PUT /api/v1/execution-plans/{id}/schedules/{schedule_id}":                                      {"cron_expr"},
		"POST /api/v1/execution-plans/{id}/schedules/{schedule_id}/occurrences/{occurrence_id}/resolve": {"action"},
		"POST /api/v1/execution-plans":                                                                  {"name"},
		"PUT /api/v1/execution-plans/{id}":                                                              {"name", "revision"},
	}
	fields := declared[method+" "+path]
	if len(fields) == 0 {
		return nil
	}
	required := make([]string, 0, len(fields))
	for _, field := range fields {
		if _, ok := properties[field]; ok {
			required = append(required, field)
		}
	}
	sort.Strings(required)
	return required
}

func mergeOpenAPIRequiredFields(existing, explicit []string) []string {
	if len(existing) == 0 && len(explicit) == 0 {
		return nil
	}
	unique := make(map[string]struct{}, len(existing)+len(explicit))
	for _, field := range append(append([]string(nil), existing...), explicit...) {
		if strings.TrimSpace(field) != "" {
			unique[field] = struct{}{}
		}
	}
	merged := make([]string, 0, len(unique))
	for field := range unique {
		merged = append(merged, field)
	}
	sort.Strings(merged)
	return merged
}

func functionResultTypes(fn *ast.FuncDecl) []ast.Expr {
	if fn == nil || fn.Type == nil || fn.Type.Results == nil {
		return nil
	}
	results := make([]ast.Expr, 0, len(fn.Type.Results.List))
	for _, field := range fn.Type.Results.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for index := 0; index < count; index++ {
			results = append(results, field.Type)
		}
	}
	return results
}

func findLocalVariableTypes(body *ast.BlockStmt, functionResults map[string][]ast.Expr) map[string]ast.Expr {
	variables := map[string]ast.Expr{}
	ast.Inspect(body, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.DeclStmt:
			declaration, ok := statement.Decl.(*ast.GenDecl)
			if !ok || declaration.Tok != token.VAR {
				return true
			}
			for _, spec := range declaration.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok || valueSpec.Type == nil {
					continue
				}
				for _, name := range valueSpec.Names {
					variables[name.Name] = valueSpec.Type
				}
			}
		case *ast.AssignStmt:
			if len(statement.Rhs) == 1 {
				if call, ok := statement.Rhs[0].(*ast.CallExpr); ok {
					results := functionResults[calledFunctionName(call.Fun)]
					for index, lhs := range statement.Lhs {
						ident, identOK := lhs.(*ast.Ident)
						if identOK && ident.Name != "_" && index < len(results) {
							variables[ident.Name] = results[index]
						}
					}
				}
			}
			for index, rhs := range statement.Rhs {
				if index >= len(statement.Lhs) {
					continue
				}
				literal, ok := rhs.(*ast.CompositeLit)
				ident, identOK := statement.Lhs[index].(*ast.Ident)
				if ok && identOK && literal.Type != nil {
					variables[ident.Name] = literal.Type
				}
			}
		}
		return true
	})
	return variables
}

func setInferredResponseSchema(call *ast.CallExpr, status int, variableTypes map[string]ast.Expr, functionResults map[string][]ast.Expr, typeDefinitions map[string]ast.Expr, analysis *handlerContractAnalysis) {
	if analysis == nil || status < 200 || status >= 400 || len(call.Args) < 3 {
		return
	}
	expr := inferredExpressionType(call.Args[len(call.Args)-1], variableTypes, functionResults)
	if expr == nil {
		return
	}
	schema := openAPISchemaForASTType(expr, typeDefinitions, map[string]bool{}, 0)
	analysis.responseSchemas[status] = schema
}

func inferredExpressionType(expr ast.Expr, variableTypes map[string]ast.Expr, functionResults map[string][]ast.Expr) ast.Expr {
	switch value := expr.(type) {
	case *ast.Ident:
		return variableTypes[value.Name]
	case *ast.UnaryExpr:
		return inferredExpressionType(value.X, variableTypes, functionResults)
	case *ast.CompositeLit:
		return value.Type
	case *ast.CallExpr:
		results := functionResults[calledFunctionName(value.Fun)]
		if len(results) > 0 {
			return results[0]
		}
	}
	return nil
}

func setInferredRequestSchema(call *ast.CallExpr, variableTypes map[string]ast.Expr, typeDefinitions map[string]ast.Expr, analysis *handlerContractAnalysis) {
	if analysis.requestSchema != nil || len(call.Args) == 0 {
		return
	}
	target := call.Args[len(call.Args)-1]
	if unary, ok := target.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		target = unary.X
	}
	ident, ok := target.(*ast.Ident)
	if !ok {
		return
	}
	expr := variableTypes[ident.Name]
	if expr == nil {
		return
	}
	schema := openAPISchemaForASTType(expr, typeDefinitions, map[string]bool{}, 0)
	analysis.requestSchema = &schema
}

func openAPISchemaForASTType(expr ast.Expr, typeDefinitions map[string]ast.Expr, visiting map[string]bool, depth int) oasSchema {
	if expr == nil {
		additional := true
		return oasSchema{Type: "object", AdditionalProperties: &additional}
	}
	switch value := expr.(type) {
	case *ast.Ident:
		switch value.Name {
		case "string":
			return oasSchema{Type: "string"}
		case "bool":
			return oasSchema{Type: "boolean"}
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
			return oasSchema{Type: "integer", Format: "int64"}
		case "float32", "float64":
			return oasSchema{Type: "number", Format: "double"}
		case "any", "interface{}":
			return oasSchema{}
		}
		if depth > 8 {
			additional := true
			return oasSchema{Type: "object", Description: "Go type: " + value.Name, AdditionalProperties: &additional}
		}
		definition := typeDefinitions[value.Name]
		if definition == nil || visiting[value.Name] {
			additional := true
			return oasSchema{Type: "object", Description: "Go type: " + value.Name, AdditionalProperties: &additional}
		}
		visiting[value.Name] = true
		schema := openAPISchemaForASTType(definition, typeDefinitions, visiting, depth+1)
		delete(visiting, value.Name)
		return schema
	case *ast.StarExpr:
		// Pointer indirection does not make a JSON document structurally deeper.
		// Keeping the same depth also lets scalar pointers retain their real type
		// when nested request models approach the recursion guard.
		return openAPISchemaForASTType(value.X, typeDefinitions, visiting, depth)
	case *ast.ArrayType:
		item := openAPISchemaForASTType(value.Elt, typeDefinitions, visiting, depth+1)
		return oasSchema{Type: "array", Items: &item}
	case *ast.MapType:
		additional := true
		return oasSchema{Type: "object", AdditionalProperties: &additional}
	case *ast.InterfaceType:
		return oasSchema{}
	case *ast.SelectorExpr:
		if pkg, ok := value.X.(*ast.Ident); ok {
			if pkg.Name == "time" && value.Sel.Name == "Time" {
				return oasSchema{Type: "string", Format: "date-time"}
			}
			if pkg.Name == "json" && value.Sel.Name == "RawMessage" {
				return oasSchema{}
			}
			additional := true
			return oasSchema{Type: "object", Description: "Go type: " + pkg.Name + "." + value.Sel.Name, AdditionalProperties: &additional}
		}
	case *ast.StructType:
		if depth > 8 {
			additional := true
			return oasSchema{Type: "object", AdditionalProperties: &additional}
		}
		properties := map[string]oasSchema{}
		for _, field := range value.Fields.List {
			name, include, _ := jsonFieldName(field)
			if !include || name == "" {
				continue
			}
			properties[name] = openAPISchemaForASTType(field.Type, typeDefinitions, visiting, depth+1)
		}
		return oasSchema{Type: "object", Properties: properties}
	}
	additional := true
	return oasSchema{Type: "object", AdditionalProperties: &additional}
}

func jsonFieldName(field *ast.Field) (string, bool, bool) {
	// encoding/json never serializes unexported fields, even when they carry an
	// explicit tag. Exposing them here advertised process-only implementation
	// details such as creationActor and runtimeSecretScope as public API fields.
	if len(field.Names) > 0 && !ast.IsExported(field.Names[0].Name) {
		return "", false, false
	}
	if field.Tag != nil {
		tagValue, err := strconv.Unquote(field.Tag.Value)
		if err == nil {
			for _, tag := range strings.Fields(tagValue) {
				if !strings.HasPrefix(tag, "json:") {
					continue
				}
				jsonTag, err := strconv.Unquote(strings.TrimPrefix(tag, "json:"))
				if err != nil {
					break
				}
				name := strings.SplitN(jsonTag, ",", 2)[0]
				if name == "-" {
					return "", false, false
				}
				if name != "" {
					omitEmpty := false
					for _, option := range strings.Split(jsonTag, ",")[1:] {
						if option == "omitempty" {
							omitEmpty = true
							break
						}
					}
					return name, true, omitEmpty
				}
			}
		}
	}
	if len(field.Names) == 0 {
		return "", false, false
	}
	return field.Names[0].Name, true, false
}

func inferredResponses(route discoveredRoute, analysis *handlerContractAnalysis) map[string]oasResponse {
	if route.Path == "/api/v1/ws" || strings.HasSuffix(route.Path, "/stream") {
		responses := map[string]oasResponse{
			"101": {Description: "WebSocket protocol switch."},
			"4XX": {Description: "Authentication, authorization, origin, resource, or upgrade error."},
		}
		if route.Authenticated {
			responses["401"] = jsonErrorResponse("Authentication required or invalid.")
			responses["403"] = jsonErrorResponse("Authenticated principal lacks the required role or service-account scope.")
		}
		return responses
	}
	if len(analysis.statuses) == 0 && !analysis.jsonResponse && len(analysis.responseMedia) == 0 {
		return nil
	}
	responses := map[string]oasResponse{}
	statuses := make([]int, 0, len(analysis.statuses))
	for status := range analysis.statuses {
		statuses = append(statuses, status)
	}
	sort.Ints(statuses)
	for _, status := range statuses {
		description := http.StatusText(status)
		if description == "" {
			description = "Handler-defined response"
		}
		response := oasResponse{Description: description}
		if status >= 400 && analysis.jsonResponse {
			response.Content = map[string]oasMediaType{
				"application/json": {Schema: oasSchema{Ref: "#/components/schemas/ErrorResponse"}},
			}
		} else if status < 400 && status != http.StatusNoContent && status != http.StatusNotModified && route.Method != http.MethodHead {
			response.Content = inferredSuccessResponseContent(route, analysis, status)
		}
		responses[strconv.Itoa(status)] = response
	}
	if !hasSuccessStatus(statuses) {
		responses["2XX"] = oasResponse{Description: "Successful response; exact status is selected by the handler.", Content: inferredSuccessResponseContent(route, analysis, 0)}
	}
	if route.Authenticated {
		responses["401"] = jsonErrorResponse("Authentication required or invalid.")
		responses["403"] = jsonErrorResponse("Authenticated principal lacks the required role or service-account scope.")
	}
	responses["default"] = oasResponse{Description: "Additional handler-defined response."}
	return responses
}

func inferredSuccessResponseContent(route discoveredRoute, analysis *handlerContractAnalysis, status int) map[string]oasMediaType {
	content := map[string]oasMediaType{}
	if analysis.jsonResponse {
		schema, ok := analysis.responseSchemas[status]
		if !ok && status == 0 && len(analysis.responseSchemas) == 1 {
			for _, candidate := range analysis.responseSchemas {
				schema = candidate
				ok = true
			}
		}
		if !ok {
			schema = oasSchema{Description: "Operation-specific JSON response."}
		}
		content["application/json"] = oasMediaType{Schema: schema}
	}
	for mediaType := range analysis.responseMedia {
		if mediaType == "" {
			continue
		}
		schema := oasSchema{}
		if strings.HasPrefix(mediaType, "text/") || strings.Contains(mediaType, "yaml") || strings.Contains(mediaType, "json") || strings.Contains(mediaType, "csv") {
			schema.Type = "string"
		} else {
			schema.Type = "string"
			schema.Format = "binary"
		}
		content[mediaType] = oasMediaType{Schema: schema}
	}
	if len(content) == 0 {
		if route.Path == "/metrics" {
			content["text/plain"] = oasMediaType{Schema: oasSchema{Type: "string"}}
		} else if strings.HasSuffix(route.Path, "/swagger.yaml") {
			content["application/yaml"] = oasMediaType{Schema: oasSchema{Type: "string"}}
		} else if strings.HasSuffix(route.Path, "/endpoint-catalog") {
			content["application/json"] = oasMediaType{Schema: oasSchema{Type: "object"}}
		}
	}
	return content
}

func jsonErrorResponse(description string) oasResponse {
	return oasResponse{
		Description: description,
		Content: map[string]oasMediaType{
			"application/json": {Schema: oasSchema{Ref: "#/components/schemas/ErrorResponse"}},
		},
	}
}

func findQueryVariables(body *ast.BlockStmt) map[string]bool {
	variables := map[string]bool{"query": true, "queries": true, "values": true, "params": true, "q": true}
	ast.Inspect(body, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.AssignStmt:
			for index, rhs := range statement.Rhs {
				if !expressionContainsURLQuery(rhs) || index >= len(statement.Lhs) {
					continue
				}
				if ident, ok := statement.Lhs[index].(*ast.Ident); ok {
					variables[ident.Name] = true
				}
			}
		case *ast.ValueSpec:
			for index, value := range statement.Values {
				if !expressionContainsURLQuery(value) || index >= len(statement.Names) {
					continue
				}
				variables[statement.Names[index].Name] = true
			}
		}
		return true
	})
	return variables
}

func receiverIsQuery(expr ast.Expr, variables map[string]bool) bool {
	if expressionContainsURLQuery(expr) {
		return true
	}
	ident, ok := expr.(*ast.Ident)
	return ok && variables[ident.Name]
}

func receiverIsRequestHeader(expr ast.Expr) bool {
	header, ok := expr.(*ast.SelectorExpr)
	if !ok || header.Sel.Name != "Header" {
		return false
	}
	receiver, ok := header.X.(*ast.Ident)
	return ok && receiver.Name == "r"
}

func documentableRequestHeader(value string) bool {
	// OpenAPI ignores Accept, Content-Type, and Authorization header parameters.
	// Authorization and the session cookie are represented by security schemes;
	// Content-Type is represented by requestBody content.
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "accept", "authorization", "content-type", "cookie":
		return false
	default:
		return strings.TrimSpace(value) != ""
	}
}

func expressionContainsURLQuery(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(next ast.Node) bool {
		if found {
			return false
		}
		selector, ok := next.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Query" {
			found = true
			return false
		}
		return true
	})
	return found
}

func expressionContainsRequestBody(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(next ast.Node) bool {
		selector, ok := next.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Body" {
			found = true
			return false
		}
		return !found
	})
	return found
}

func callPassesRequest(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		if ident, ok := arg.(*ast.Ident); ok && ident.Name == "r" {
			return true
		}
	}
	return false
}

func calledFunctionName(expr ast.Expr) string {
	switch fn := expr.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	default:
		return ""
	}
}

func plausibleQueryName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/ ?=:,|\n\t") {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func normalizeMediaType(value string) string {
	return strings.TrimSpace(strings.SplitN(value, ";", 2)[0])
}

func hasSuccessStatus(statuses []int) bool {
	for _, status := range statuses {
		if status >= 200 && status < 400 {
			return true
		}
	}
	return false
}

func openAPIHTTPStatus(expr ast.Expr) (int, bool) {
	if literal, ok := expr.(*ast.BasicLit); ok && literal.Kind == token.INT {
		value, err := strconv.Atoi(literal.Value)
		return value, err == nil
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	statuses := map[string]int{
		"StatusSwitchingProtocols": http.StatusSwitchingProtocols,
		"StatusOK":                 http.StatusOK, "StatusCreated": http.StatusCreated, "StatusAccepted": http.StatusAccepted,
		"StatusNoContent": http.StatusNoContent, "StatusPartialContent": http.StatusPartialContent,
		"StatusFound": http.StatusFound, "StatusSeeOther": http.StatusSeeOther,
		"StatusTemporaryRedirect": http.StatusTemporaryRedirect, "StatusPermanentRedirect": http.StatusPermanentRedirect,
		"StatusBadRequest": http.StatusBadRequest, "StatusUnauthorized": http.StatusUnauthorized,
		"StatusForbidden": http.StatusForbidden, "StatusNotFound": http.StatusNotFound,
		"StatusMethodNotAllowed": http.StatusMethodNotAllowed, "StatusConflict": http.StatusConflict,
		"StatusGone": http.StatusGone, "StatusRequestEntityTooLarge": http.StatusRequestEntityTooLarge,
		"StatusPreconditionRequired": http.StatusPreconditionRequired,
		"StatusUnsupportedMediaType": http.StatusUnsupportedMediaType, "StatusUnprocessableEntity": http.StatusUnprocessableEntity,
		"StatusTooManyRequests": http.StatusTooManyRequests, "StatusInternalServerError": http.StatusInternalServerError,
		"StatusNotImplemented": http.StatusNotImplemented, "StatusBadGateway": http.StatusBadGateway,
		"StatusServiceUnavailable": http.StatusServiceUnavailable, "StatusGatewayTimeout": http.StatusGatewayTimeout,
	}
	status, ok := statuses[selector.Sel.Name]
	return status, ok
}
