package enterprisearchive

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

func convertSourceVerification(fn ParsedFunction) dsl.Action {
	operation := "register"
	if strings.HasSuffix(fn.Name, "_pause") {
		operation = "pause"
	}
	if strings.HasSuffix(fn.Name, "_resume") {
		operation = "resume"
	}
	id := firstNonEmptyArg(fn, "ID")
	if id == "" {
		id = fmt.Sprintf("%s:%d", fn.Filename, fn.LineNumber)
	}
	a := dsl.Action{Name: "Verification_" + sanitizeName(id), Type: "source_verification", Params: map[string]any{"operation": operation, "id": id}, Metadata: sourceFunctionMetadata(fn)}
	if operation != "register" {
		if firstNonEmptyArg(fn, "ID") == "" && len(fn.RawArgs) > 0 {
			a.Params["id"] = fn.RawArgs[0]
		}
		return a
	}
	text, flags := sourceFlaggedArgument(fn, "Text")
	scope := strings.ToLower(firstNonEmptyArg(fn, "Search"))
	if scope == "" {
		scope = "noresource"
	}
	a.Params["text"] = convertParamReferences(unescapeSourceCString(text))
	a.Params["scope"] = scope
	a.Params["case_insensitive"] = sourceFlagsInclude(flags, "IC")
	failureMode := firstNonEmptyArg(fn, "Fail")
	a.Params["fail_if_found"] = !strings.EqualFold(failureMode, "NotFound")
	unsupportedFailure := failureMode != "" && !strings.EqualFold(failureMode, "Found") && !strings.EqualFold(failureMode, "NotFound")
	if text == "" || sourceFlagsInclude(flags, "BIN") || unsupportedFailure {
		importreview.ApplyToAction(&a, []importreview.Finding{{Code: "import_review_required", ReasonCode: "verification_form_unsupported", Severity: diag.Blocker, Detail: "This verification form requires a replacement text check.", Gate: importreview.GateExecution}})
	}
	return a
}

func convertSourceVerifications(fn ParsedFunction) []dsl.Action {
	a := convertSourceVerification(fn)
	if fn.Name == "web_global_verification" {
		return []dsl.Action{a}
	}
	out := []dsl.Action{}
	for _, arg := range fn.CallArgs {
		if strings.EqualFold(arg.Name, "ID") {
			next := a
			next.Params = map[string]any{"operation": a.Params["operation"], "id": convertParamReferences(arg.Value)}
			next.Name = "Verification_" + sanitizeName(arg.Value)
			out = append(out, next)
		}
	}
	if len(out) == 0 {
		return []dsl.Action{a}
	}
	return out
}
