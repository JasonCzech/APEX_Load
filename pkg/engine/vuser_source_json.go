package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/jsonquery"
)

// handleSourceJSONAction executes the LoadRunner JSON helpers scripts use to
// read Aura and REST return values: lr_eval_json parses a buffer into a named
// document, lr_json_get_values saves JSONPath matches into parameters, and
// lr_json_stringify saves a document as text. Every other JSON function fails
// the step instead of guessing at its effect.
func (vu *VUser) handleSourceJSONAction(ctx context.Context, action dsl.Action) error {
	function := sourceActionFunction(action)
	params := sourceJSONParams(action)
	switch function {
	case "lr_eval_json":
		return vu.evalSourceJSON(params)
	case "lr_json_get_values":
		return vu.getSourceJSONValues(ctx, params)
	case "lr_json_stringify":
		return vu.stringifySourceJSON(params)
	default:
		return sourceDocumentActionUnsupported(action)
	}
}

// sourceJSONParams prefers the importer's structured params and falls back to
// the source Key=Value arguments of older imports.
func sourceJSONParams(action dsl.Action) map[string]string {
	params := map[string]string{}
	for _, key := range []string{"json_object", "buffer", "value_param", "query", "select_all", "not_found", "format", "output_param"} {
		if value, ok := action.Params[key].(string); ok {
			params[key] = value
		}
	}
	if params["json_object"] != "" {
		return params
	}
	keys := map[string]string{"jsonobject": "json_object", "buffer": "buffer", "valueparam": "value_param", "querystring": "query", "selectall": "select_all", "notfound": "not_found", "format": "format", "outputparam": "output_param"}
	for _, arg := range sourceActionArgs(action) {
		name, value, found := strings.Cut(arg, "=")
		if key, known := keys[strings.ToLower(strings.TrimSpace(name))]; found && known {
			params[key] = value
		}
	}
	return params
}

func (vu *VUser) sourceJSONDocument(name string) (any, error) {
	document, ok := vu.sourceJSONDocuments[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil, fmt.Errorf("JsonObject %q was not created by a preceding lr_eval_json", name)
	}
	return document, nil
}

func (vu *VUser) evalSourceJSON(params map[string]string) error {
	name := strings.TrimSpace(params["json_object"])
	if name == "" {
		return errors.New("lr_eval_json requires JsonObject")
	}
	// Interpolate once (each-occurrence parameters advance per resolution),
	// then look for the template's own tokens: unresolved references stay
	// verbatim, while {{...}} text inside a resolved document is data.
	buffer := vu.interpolate(params["buffer"])
	for _, token := range sourceJSONTemplateToken.FindAllString(params["buffer"], -1) {
		if strings.Contains(buffer, token) {
			return fmt.Errorf("lr_eval_json %s: Buffer reference %s has no value", name, token)
		}
	}
	document, err := jsonquery.Decode([]byte(buffer))
	if err != nil {
		return fmt.Errorf("lr_eval_json %s: %w", name, err)
	}
	if vu.sourceJSONDocuments == nil {
		vu.sourceJSONDocuments = map[string]any{}
	}
	vu.sourceJSONDocuments[strings.ToLower(name)] = document
	return nil
}

func (vu *VUser) getSourceJSONValues(ctx context.Context, params map[string]string) error {
	name := strings.TrimSpace(params["json_object"])
	valueParam := strings.TrimSpace(params["value_param"])
	if name == "" || valueParam == "" {
		return errors.New("lr_json_get_values requires JsonObject and ValueParam")
	}
	document, err := vu.sourceJSONDocument(name)
	if err != nil {
		return fmt.Errorf("lr_json_get_values: %w", err)
	}
	query := vu.interpolate(params["query"])
	path, err := jsonquery.Parse(query)
	if err != nil {
		return fmt.Errorf("lr_json_get_values %s: %w", valueParam, err)
	}
	all := strings.EqualFold(strings.TrimSpace(params["select_all"]), "yes")
	limit := 1
	if all {
		limit = 0
	}
	matches, err := path.Select(document, limit)
	if err != nil {
		return fmt.Errorf("lr_json_get_values %s: %w", valueParam, err)
	}
	if len(matches) == 0 {
		if !strings.EqualFold(strings.TrimSpace(params["not_found"]), "continue") {
			return fmt.Errorf("lr_json_get_values %s: QueryString %q matched nothing in JsonObject %s", valueParam, query, name)
		}
		vu.runtimeLogger().WarnContext(ctx, "lr_json_get_values matched nothing", "vuser", vu.ID, "value_param", valueParam, "query", query)
		if all {
			vu.setSourceJSONArray(valueParam, nil)
		}
		return nil
	}
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		value, err := jsonquery.ValueString(match)
		if err != nil {
			return fmt.Errorf("lr_json_get_values %s: %w", valueParam, err)
		}
		values = append(values, value)
	}
	if all {
		vu.setSourceJSONArray(valueParam, values)
		return nil
	}
	vu.setResolvedSourceVariable(valueParam, values[0])
	return nil
}

// setSourceJSONArray publishes NAME_1..NAME_n and NAME_count, removing the
// entries an earlier, longer result left behind.
func (vu *VUser) setSourceJSONArray(name string, values []string) {
	vu.ensureVariables()
	previous, _ := strconv.Atoi(strings.TrimSpace(vu.Variables[strings.ToLower(name+"_count")]))
	for index, value := range values {
		vu.setResolvedSourceVariable(fmt.Sprintf("%s_%d", name, index+1), value)
	}
	for index := len(values) + 1; index <= previous; index++ {
		stale := fmt.Sprintf("%s_%d", name, index)
		delete(vu.Variables, stale)
		delete(vu.Variables, strings.ToLower(stale))
	}
	vu.setResolvedSourceVariable(name+"_count", strconv.Itoa(len(values)))
}

func (vu *VUser) stringifySourceJSON(params map[string]string) error {
	name := strings.TrimSpace(params["json_object"])
	output := strings.TrimSpace(params["output_param"])
	if name == "" || output == "" {
		return errors.New("lr_json_stringify requires JsonObject and OutputParam")
	}
	document, err := vu.sourceJSONDocument(name)
	if err != nil {
		return fmt.Errorf("lr_json_stringify: %w", err)
	}
	indent := ""
	if strings.EqualFold(strings.TrimSpace(params["format"]), "indented") {
		indent = "    "
	}
	text, err := jsonquery.Encode(document, indent)
	if err != nil {
		return fmt.Errorf("lr_json_stringify %s: %w", output, err)
	}
	vu.setResolvedSourceVariable(output, text)
	return nil
}

var sourceJSONTemplateToken = regexp.MustCompile(`\{\{[^{}]+\}\}`)
