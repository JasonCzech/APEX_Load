package enterprisearchive

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// htmlStepTarget is the placeholder target of an HTML-mode step. The runtime
// resolves the real URL from the page and never requests this one.
const htmlStepTarget = "#html-step"

// isHTMLStepFunction reports whether a function becomes an HTML-mode step
// when it names no explicit URL.
func isHTMLStepFunction(fn ParsedFunction) bool {
	switch fn.Name {
	case "web_link", "web_image":
		return strings.TrimSpace(fn.Args["URL"]) == ""
	case "web_image_check", "web_submit_form":
		return true
	}
	return false
}

// sourceHTMLStep builds params.html_step for an HTML-mode function, or
// returns why it cannot be expressed yet.
func sourceHTMLStep(fn ParsedFunction) (map[string]any, string) {
	arg := func(name string) string {
		value, _ := sourceNamedArg(fn.Args, name)
		return strings.TrimSpace(value)
	}
	for _, name := range []string{"Frame", "TargetFrame", "FrameName"} {
		if value := arg(name); value != "" && !strings.EqualFold(value, "_top") && !strings.EqualFold(value, "_self") {
			return nil, fmt.Sprintf("it targets frame %q", value)
		}
	}
	step := map[string]any{}
	if ordinal := arg("Ordinal"); ordinal != "" {
		number, err := strconv.Atoi(ordinal)
		if err != nil || number < 1 {
			return nil, fmt.Sprintf("its Ordinal=%s is not a positive number", ordinal)
		}
		step["ordinal"] = number
	}
	switch fn.Name {
	case "web_link":
		text, flags := sourceFlaggedArgument(fn, "Text")
		if strings.TrimSpace(text) == "" {
			return nil, "it has no Text= to find the link by"
		}
		step["kind"] = "link"
		step["text"] = convertParamReferences(text)
		if sourceFlagsInclude(flags, "IC") {
			step["case_insensitive"] = true
		}
	case "web_image", "web_image_check":
		if arg("MapName") != "" || arg("AreaAlt") != "" || arg("AreaOrdinal") != "" {
			return nil, "it clicks an image-map area"
		}
		alt, src := arg("Alt"), arg("Src")
		if alt == "" && src == "" {
			return nil, "it has no Alt= or Src= to find the image by"
		}
		step["kind"] = "image"
		if fn.Name == "web_image_check" {
			step["kind"] = "image_check"
		}
		if alt != "" {
			step["alt"] = convertParamReferences(alt)
		}
		if src != "" {
			step["src"] = convertParamReferences(src)
		}
	case "web_submit_form":
		fields, err := sourceFormItems(fn, "ITEMDATA", "")
		if err != "" {
			return nil, err
		}
		hidden, err := sourceFormItems(fn, "STARTHIDDENS", "ENDHIDDENS")
		if err != "" {
			return nil, err
		}
		step["kind"] = "form"
		step["fields"] = fields
		if len(hidden) > 0 {
			step["hidden"] = hidden
		}
		if action := arg("Action"); action != "" {
			step["action"] = convertParamReferences(action)
		}
	}
	return step, ""
}

// sourceFormItems reads the Name=/Value= items that follow a macro (ITEMDATA
// or STARTHIDDENS) up to its end macro (or the next macro).
func sourceFormItems(fn ParsedFunction, start, end string) ([]any, string) {
	var items []any
	active := false
	var name, value string
	hasName, hasValue := false, false
	flush := func() string {
		if !hasName && !hasValue {
			return ""
		}
		if !hasName {
			return "a form item has a Value= without a Name="
		}
		items = append(items, map[string]any{"name": convertParamReferences(name), "value": convertParamReferences(value)})
		name, value, hasName, hasValue = "", "", false, false
		return ""
	}
	for _, arg := range fn.CallArgs {
		if arg.Kind != "key_value" {
			switch {
			case sourceArgTokenEquals(arg, start):
				active = true
			case sourceArgTokenEquals(arg, "ENDITEM"):
				if reason := flush(); reason != "" {
					return nil, reason
				}
			case active && (end == "" || sourceArgTokenEquals(arg, end)) && !sourceArgTokenEquals(arg, "ENDITEM"):
				if reason := flush(); reason != "" {
					return nil, reason
				}
				active = false
			}
			continue
		}
		if !active {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(arg.Name)) {
		case "name":
			if hasName {
				if reason := flush(); reason != "" {
					return nil, reason
				}
			}
			name, hasName = arg.Value, true
		case "value":
			value, hasValue = arg.Value, true
		default:
			return nil, fmt.Sprintf("a form item uses %s=, which a page-context form submission does not support", arg.Name)
		}
	}
	if reason := flush(); reason != "" {
		return nil, reason
	}
	return items, ""
}

// convertHTMLStepAction converts an HTML-mode function into a request that
// resolves against the page at replay. It returns nil, with a warning, when
// the step cannot be expressed; the caller then keeps the review gate.
func convertHTMLStepAction(fn ParsedFunction, transaction, subTransaction string, opts ConvertOptions) (*dsl.Action, []string) {
	step, reason := sourceHTMLStep(fn)
	if reason != "" {
		return nil, []string{fmt.Sprintf("line %d: %s %q cannot be replayed against the page because %s", fn.LineNumber, fn.Name, convertutil.FirstNonEmpty(fn.StepName, fn.Name), reason)}
	}
	action := &dsl.Action{
		Name:     sanitizeName(convertutil.FirstNonEmpty(fn.StepName, fn.Name)),
		Type:     "GET",
		Method:   "GET",
		Target:   htmlStepTarget,
		Headers:  map[string]string{},
		Metadata: sourceFunctionMetadata(fn),
		Params:   map[string]any{httpprotocol.HTMLStepParam: step},
	}
	if fn.Name == "web_submit_form" {
		// The page's form decides the method; POST is the usual case.
		action.Type, action.Method = "POST", "POST"
	}
	if transaction != "" {
		action.Metadata["source_transaction"] = transaction
		if opts.PreserveTransactions {
			action.Name = sanitizeName(transaction) + "_" + action.Name
		}
	}
	if subTransaction != "" {
		action.Metadata["source_subtransaction"] = subTransaction
		if _, exists := action.Metadata["source_action_name"]; !exists {
			action.Metadata["source_action_name"] = subTransaction
		}
	}
	for name, value := range fn.Headers {
		if strings.EqualFold(name, "Content-Type") {
			continue
		}
		action.Headers[name] = convertParamReferences(value)
	}
	applySourceRefererHeader(action, fn)
	applySourceStepMode(action, fn)
	applyExtraResources(fn, action)
	return action, nil
}
