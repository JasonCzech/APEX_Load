package enterprisearchive

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// HTML-mode (context-sensitive) VuGen functions act on the page the previous
// step loaded. Until the HTTP runtime keeps that page (WP2.1-WP2.3), the
// importer must say so instead of guessing a request.
const (
	// htmlNavigationReasonCode gates a web_link or web_image click whose target
	// is resolved from the previous page. Dropping the click would replay a
	// flow the server never sees, so execution is blocked.
	htmlNavigationReasonCode = "html_navigation_requires_page_context"
	// htmlFormReasonCode marks a web_submit_form built without its page. The
	// request may still work (forms without hidden fields), so replay stays
	// available for debugging and only promotion is blocked.
	htmlFormReasonCode = "html_form_requires_page_context"
)

// htmlNavigationSelectorArgs are the attributes VuGen uses to find the link,
// image or image-map area on the current page.
var htmlNavigationSelectorArgs = []string{"Text", "Alt", "Src", "Ordinal", "Frame", "TargetFrame", "MapName", "AreaAlt", "AreaOrdinal"}

// htmlNavigationGateAction returns the execution-gated review action that
// stands in for an unresolved web_link or web_image at its source position.
func htmlNavigationGateAction(fn ParsedFunction) dsl.Action {
	step := convertutil.FirstNonEmpty(fn.StepName, fn.Name)
	parts := make([]string, 0, len(htmlNavigationSelectorArgs))
	for _, name := range htmlNavigationSelectorArgs {
		if value, ok := sourceNamedArg(fn.Args, name); ok && strings.TrimSpace(value) != "" {
			parts = append(parts, fmt.Sprintf("%s=%q", name, value))
		}
	}
	kind := "link"
	if fn.Name == "web_image" {
		kind = "image link"
	}
	described := "no selector attributes"
	if len(parts) > 0 {
		described = strings.Join(parts, ", ")
	}
	// The review placeholder keeps the recorded selector arguments
	// (disclosure-safe) in params.args, so they are not copied here.
	action := dsl.Action{
		Name:     sanitizeName("Resolve " + step),
		Type:     "source_review",
		Params:   map[string]any{"function": fn.Name},
		Metadata: sourceFunctionMetadata(fn),
	}
	importreview.ApplyToAction(&action, []importreview.Finding{{
		Code:       htmlNavigationReasonCode,
		ReasonCode: htmlNavigationReasonCode,
		Severity:   diag.Blocker,
		Gate:       importreview.GateExecution,
		Source:     "enterprise_script_archive",
		SourceRef:  fmt.Sprintf("%s:%d", fn.Filename, fn.LineNumber),
		Resolution: "replace_with_explicit_request",
		Detail: fmt.Sprintf("%s %q clicks the %s matching %s on the page the previous step loaded. "+
			"APEX Load cannot resolve that target from the script alone; replace this step with an explicit request or re-record the script in URL mode.",
			fn.Name, step, kind, described),
	}})
	return action
}

// applyHTMLFormReview marks a web_submit_form request as an approximation:
// VuGen takes the form's action URL, method and hidden fields from the page
// at replay, while this request was built from the script alone.
func applyHTMLFormReview(action *dsl.Action, fn ParsedFunction) {
	if action == nil || fn.Name != "web_submit_form" {
		return
	}
	importreview.ApplyToAction(action, []importreview.Finding{{
		Code:       htmlFormReasonCode,
		ReasonCode: htmlFormReasonCode,
		Severity:   diag.Warning,
		Gate:       importreview.GatePromotion,
		Source:     "enterprise_script_archive",
		SourceRef:  fmt.Sprintf("%s:%d", fn.Filename, fn.LineNumber),
		Resolution: "verify_form_submission",
		Detail: "web_submit_form submits a form found on the previous page; VuGen fills in that form's hidden fields, action URL and method at replay. " +
			"This request was built from the script alone (target inferred from the step name, only the listed fields), so hidden fields such as anti-forgery tokens are missing. Verify it against the application before promotion.",
	}})
}
