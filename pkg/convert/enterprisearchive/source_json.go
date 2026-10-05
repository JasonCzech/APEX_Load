package enterprisearchive

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/jsonquery"
)

// sourceJSONArguments lists the arguments the runtime evaluates for each
// executable LoadRunner JSON helper, and whether each is required.
var sourceJSONArguments = map[string]map[string]bool{
	"lr_eval_json":       {"buffer": true, "jsonobject": true},
	"lr_json_get_values": {"jsonobject": true, "valueparam": true, "querystring": true, "selectall": false, "notfound": false},
	"lr_json_stringify":  {"jsonobject": true, "outputparam": true, "format": false},
}

// annotateSourceJSONAction gives an executable JSON helper the structured
// params the engine evaluates and the produced parameter that dependency
// review reads (save_param, plus save_all for SelectAll=Yes arrays). When the
// call uses a form APEX does not evaluate it returns a reason code and detail,
// so the caller gates it instead of letting it run with a guessed meaning.
// Durable import review keeps only the code, so each cause has its own.
func annotateSourceJSONAction(action *dsl.Action, fn ParsedFunction) (string, string) {
	allowed, known := sourceJSONArguments[fn.Name]
	if !known {
		return "source_json_helper_not_evaluated", fn.Name + " is not evaluated by APEX"
	}
	args := map[string]string{}
	for _, arg := range fn.CallArgs {
		switch arg.Kind {
		case "macro":
			continue
		case "key_value":
		default:
			return "source_json_argument_unsupported", fmt.Sprintf("%s argument %q is not a literal Key=Value string", fn.Name, arg.Raw)
		}
		key := strings.ToLower(strings.TrimSpace(arg.Name))
		if key == "buffer/file" {
			return "source_json_buffer_file_unsupported", "lr_eval_json Buffer/File reads a file that APEX does not load; save the JSON into a parameter and use Buffer={param}"
		}
		if _, ok := allowed[key]; !ok {
			return "source_json_argument_unsupported", fmt.Sprintf("%s argument %s is not evaluated by APEX", fn.Name, arg.Name)
		}
		if _, repeated := args[key]; repeated {
			return "source_json_argument_repeated", fmt.Sprintf("%s repeats argument %s", fn.Name, arg.Name)
		}
		args[key] = strings.TrimSpace(arg.Value)
	}
	for key, required := range allowed {
		if required && args[key] == "" {
			return "source_json_argument_missing", fmt.Sprintf("%s is missing a %s argument", fn.Name, key)
		}
	}
	action.Params["json_object"] = args["jsonobject"]
	switch fn.Name {
	case "lr_eval_json":
		action.Params["buffer"] = convertParamReferences(args["buffer"])
	case "lr_json_get_values":
		query := convertParamReferences(args["querystring"])
		if !strings.Contains(query, "{{") {
			if _, err := jsonquery.Parse(query); err != nil {
				if errors.Is(err, jsonquery.ErrUnsupported) {
					return "source_json_query_unsupported", fmt.Sprintf("lr_json_get_values QueryString %q uses JSONPath filters, slices or unions that APEX does not evaluate", query)
				}
				return "source_json_query_invalid", fmt.Sprintf("lr_json_get_values QueryString %q is not valid JSONPath: %v", query, err)
			}
		}
		selectAll := strings.ToLower(args["selectall"])
		if selectAll != "" && selectAll != "yes" && selectAll != "no" {
			return "source_json_option_invalid", "lr_json_get_values SelectAll must be Yes or No"
		}
		notFound := strings.ToLower(args["notfound"])
		if notFound != "" && notFound != "error" && notFound != "continue" {
			return "source_json_option_invalid", "lr_json_get_values NotFound must be Error or Continue"
		}
		action.Params["value_param"] = args["valueparam"]
		action.Params["query"] = query
		action.Params["select_all"] = selectAll
		action.Params["not_found"] = notFound
		action.Params["save_param"] = args["valueparam"]
		if selectAll == "yes" {
			action.Params["save_all"] = true
		}
	case "lr_json_stringify":
		format := strings.ToLower(args["format"])
		if format != "" && format != "auto" && format != "compact" && format != "indented" {
			return "source_json_option_invalid", "lr_json_stringify Format must be compact, indented or auto"
		}
		action.Params["output_param"] = args["outputparam"]
		action.Params["format"] = format
		action.Params["save_param"] = args["outputparam"]
	}
	return "", ""
}
