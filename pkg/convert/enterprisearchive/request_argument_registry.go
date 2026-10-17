package enterprisearchive

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

// Every named attribute of a VuGen request step is classified here, so an
// attribute the converter does not reproduce can never disappear silently.
// Attribute names follow the VuGen 26.1 Function Reference for web_url,
// web_custom_request, web_submit_data, web_submit_form and web_rest.
const (
	// requestBodyVariantReasonCode gates a body the converter cannot rebuild
	// exactly (RAW_BODY_START, or a BodyBinary, BodyUnicode or BodyFilePath
	// form request_body_variants.go refuses): the request would otherwise be
	// sent with an empty or partial body.
	requestBodyVariantReasonCode = "request_body_variant_unsupported"
	// requestArgumentReasonCode gates any other attribute that changes the
	// request on the wire in a way APEX does not reproduce yet.
	requestArgumentReasonCode = "request_argument_unsupported"
	// requestArgumentReviewReasonCode marks an attribute that does not change
	// the request but produces something the script may rely on later.
	requestArgumentReviewReasonCode = "request_argument_review"
)

type requestArgumentClass int

const (
	// requestArgumentMapped: the converter reproduces the attribute.
	requestArgumentMapped requestArgumentClass = iota + 1
	// requestArgumentInert: recording metadata or a response hint; it never
	// changes the request on the wire.
	requestArgumentInert
	// requestArgumentBody: a body form rebuilt by request_body_variants.go,
	// gated when it cannot be rebuilt exactly.
	requestArgumentBody
	// requestArgumentGated: changes the wire; not reproduced yet.
	requestArgumentGated
	// requestArgumentReview: no wire effect, but a side effect the script may
	// depend on (for example ResponseTime saving a parameter).
	requestArgumentReview
)

// requestArgumentFunctions are the request functions this registry governs.
// HTML-mode navigation (web_link, web_image, web_image_check) has its own
// gate, and web_service_call belongs to the SOAP importer.
var requestArgumentFunctions = map[string]bool{
	"web_url": true, "web_custom_request": true, "web_submit_data": true, "web_submit_form": true, "web_rest": true,
}

var requestArgumentClasses = map[string]requestArgumentClass{
	"url":               requestArgumentMapped,
	"action":            requestArgumentMapped,
	"method":            requestArgumentMapped,
	"referer":           requestArgumentMapped,
	"enctype":           requestArgumentMapped,
	"body":              requestArgumentMapped,
	"useragent":         requestArgumentMapped,
	"contenttype":       requestArgumentMapped,
	"resource":          requestArgumentInert,
	"reccontenttype":    requestArgumentInert,
	"snapshot":          requestArgumentInert,
	"mode":              requestArgumentInert,
	"targetframe":       requestArgumentInert,
	"resourcebytelimit": requestArgumentInert,
	"ftpascii":          requestArgumentInert,
	"bodybinary":        requestArgumentBody,
	"bodyunicode":       requestArgumentBody,
	"bodyfilepath":      requestArgumentBody,
	"contentencoding":   requestArgumentMapped, // gzip and deflate; any other value is gated
	"extraresbasedir":   requestArgumentGated,
	"responsetime":      requestArgumentReview,
}

// requestArgumentMacros are the bare argument macros that end the named
// attribute list or open a body form.
var requestArgumentListEnd = map[string]bool{
	"ITEMDATA": true, "EXTRARES": true, "HEADERS": true, "LAST": true, "ENDITEM": true, "ENDHEADER": true,
	"STARTHIDDENS": true, "ENDHIDDENS": true,
}

// isRequestArgumentListEnd reports whether a bare argument (macro or
// identifier) ends the named attribute list.
func isRequestArgumentListEnd(arg ParsedArgument) bool {
	for token := range requestArgumentListEnd {
		if sourceArgTokenEquals(arg, token) {
			return true
		}
	}
	return false
}

type requestArgumentReport struct {
	body     []string
	gated    []string
	review   []string
	unknown  []string
	userAgnt string
	// contentEncoding is the body compression ContentEncoding asks for.
	contentEncoding string
}

// classifyRequestArguments sorts a request step's named attributes.
func classifyRequestArguments(fn ParsedFunction) requestArgumentReport {
	var report requestArgumentReport
	if !requestArgumentFunctions[fn.Name] {
		return report
	}
	for _, arg := range fn.CallArgs {
		if arg.Kind != "key_value" {
			if sourceArgTokenEquals(arg, "RAW_BODY_START") || sourceArgTokenEquals(arg, "RAW_BODY_END") {
				report.body = appendUniqueFold(report.body, "RAW_BODY_START")
				continue
			}
			if isRequestArgumentListEnd(arg) {
				break
			}
			continue
		}
		if strings.TrimSpace(arg.Name) == "" {
			continue
		}
		name := strings.TrimSpace(arg.Name)
		switch class := requestArgumentClasses[strings.ToLower(name)]; class {
		case requestArgumentMapped, requestArgumentInert:
			if strings.EqualFold(name, "UserAgent") {
				report.userAgnt = strings.TrimSpace(arg.Value)
			}
			if strings.EqualFold(name, "ContentEncoding") {
				switch encoding := strings.ToLower(strings.TrimSpace(arg.Value)); encoding {
				case "gzip", "deflate":
					if report.contentEncoding != "" && report.contentEncoding != encoding {
						// Two codings for one body: which LoadRunner applies is unknown.
						report.gated = appendUniqueFold(report.gated, name+"="+arg.Value)
					}
					report.contentEncoding = encoding
				default:
					report.gated = appendUniqueFold(report.gated, name+"="+arg.Value)
				}
			}
		case requestArgumentBody:
			report.body = appendUniqueFold(report.body, name)
		case requestArgumentGated:
			report.gated = appendUniqueFold(report.gated, name)
		case requestArgumentReview:
			report.review = appendUniqueFold(report.review, name)
		default:
			if strings.EqualFold(name, "EncodeAtSign") && fn.Name == "web_submit_data" {
				// The form encoder already percent-encodes "@" (EncodeAtSign=Yes).
				if strings.EqualFold(strings.TrimSpace(arg.Value), "no") {
					report.gated = appendUniqueFold(report.gated, name+"=No")
				}
				continue
			}
			report.unknown = appendUniqueFold(report.unknown, name)
		}
	}
	return report
}

func appendUniqueFold(values []string, value string) []string {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}

// applyRequestArgumentReview gates or annotates a converted request action
// according to its source attributes. It returns the unknown attribute names
// so the caller can report them once per script, and whether execution was
// gated.
func applyRequestArgumentReview(action *dsl.Action, fn ParsedFunction, opts ConvertOptions) ([]string, bool) {
	if action == nil {
		return nil, false
	}
	report := classifyRequestArguments(fn)
	bodyRebuild := rebuildSourceBodyVariants(action, fn, opts.archiveFiles, opts.sourceEncoding)
	report.body = withoutFold(report.body, bodyRebuild.rebuilt)
	if report.userAgnt != "" {
		if _, exists := headerValueCaseInsensitive(action.Headers, "User-Agent"); !exists {
			value := convertParamReferences(report.userAgnt)
			if action.Headers == nil {
				action.Headers = make(map[string]string)
			}
			action.Headers["User-Agent"] = value
			if len(action.HeaderList) > 0 {
				action.HeaderList = append(action.HeaderList, dsl.HeaderEntry{Name: "User-Agent", Value: value})
			}
		}
	}
	if report.contentEncoding != "" {
		// The body is compressed on the wire, as LoadRunner sends it.
		if action.Params == nil {
			action.Params = make(map[string]any)
		}
		action.Params[httpprotocol.RequestContentEncodingParam] = report.contentEncoding
	}
	ref := fmt.Sprintf("%s:%d", fn.Filename, fn.LineNumber)
	step := convertutil.FirstNonEmpty(fn.StepName, fn.Name)
	var findings []importreview.Finding
	findings = append(findings, bodyRebuild.findings...)
	if len(report.body) > 0 {
		why := "which the importer cannot rebuild yet"
		if bodyRebuild.reason != "" {
			why = "which the importer cannot rebuild exactly: " + bodyRebuild.reason
		}
		findings = append(findings, importreview.Finding{
			Code: requestBodyVariantReasonCode, ReasonCode: requestBodyVariantReasonCode, Severity: diag.Blocker, Gate: importreview.GateExecution,
			Source: "enterprise_script_archive", SourceRef: ref, Resolution: "rebuild_body",
			Detail: fmt.Sprintf("%s %q sends its body through %s, %s; without the gate the request would be sent with an empty or partial body.",
				fn.Name, step, strings.Join(report.body, ", "), why),
		})
	}
	if len(report.gated) > 0 {
		findings = append(findings, importreview.Finding{
			Code: requestArgumentReasonCode, ReasonCode: requestArgumentReasonCode, Severity: diag.Blocker, Gate: importreview.GateExecution,
			Source: "enterprise_script_archive", SourceRef: ref, Resolution: "review_request_arguments",
			Detail: fmt.Sprintf("%s %q uses %s, which changes the request on the wire in a way APEX Load does not reproduce yet.",
				fn.Name, step, strings.Join(report.gated, ", ")),
		})
	}
	if len(report.review) > 0 {
		findings = append(findings, importreview.Finding{
			Code: requestArgumentReviewReasonCode, ReasonCode: requestArgumentReviewReasonCode, Severity: diag.Warning, Gate: importreview.GatePromotion,
			Source: "enterprise_script_archive", SourceRef: ref, Resolution: "review_request_arguments",
			Detail: fmt.Sprintf("%s %q uses %s, which APEX Load does not reproduce; anything later steps read from it will be missing.",
				fn.Name, step, strings.Join(report.review, ", ")),
		})
	}
	if len(findings) > 0 {
		importreview.ApplyToAction(action, findings)
	}
	if len(report.unknown) > 0 {
		sort.Strings(report.unknown)
		if action.Metadata == nil {
			action.Metadata = make(map[string]any)
		}
		action.Metadata["source_unmodelled_args"] = append([]string(nil), report.unknown...)
	}
	return report.unknown, len(report.body) > 0 || len(report.gated) > 0 || len(bodyRebuild.findings) > 0
}

// withoutFold returns values minus every entry of remove, ignoring case.
func withoutFold(values, remove []string) []string {
	if len(remove) == 0 {
		return values
	}
	kept := values[:0:0]
	for _, value := range values {
		drop := false
		for _, candidate := range remove {
			if strings.EqualFold(value, candidate) {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, value)
		}
	}
	return kept
}

// unmodelledRequestArgumentsWarning summarizes, once per script, the request
// attributes the importer does not know.
func unmodelledRequestArgumentsWarning(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s (%d)", name, counts[name]))
	}
	return "Request steps use attributes the importer does not model, so they were ignored: " + strings.Join(parts, ", ") +
		". Check whether they change the request; each affected action lists them in metadata.source_unmodelled_args."
}

// concatenateSourceBodySegments joins every Body= attribute in argument
// order, as VuGen does ("multiple body segments are concatenated together in
// the same order as they are passed"). It returns false when the step has at
// most one Body= segment.
func concatenateSourceBodySegments(fn *ParsedFunction) bool {
	var body, raw strings.Builder
	segments := 0
	rawOK := true
	for _, arg := range fn.CallArgs {
		if arg.Kind != "key_value" && isRequestArgumentListEnd(arg) {
			break
		}
		if arg.Kind != "key_value" || !strings.EqualFold(strings.TrimSpace(arg.Name), "Body") {
			continue
		}
		segments++
		body.WriteString(arg.Value)
		if rawOK {
			if content, ok := adjacentCStringContent(arg.Raw); ok {
				if _, value, found := strings.Cut(content, "="); found {
					raw.WriteString(value)
					continue
				}
			}
			rawOK = false
		}
	}
	if segments < 2 {
		return false
	}
	fn.Body = body.String()
	fn.Args["Body"] = fn.Body
	if rawOK {
		fn.RawBody = raw.String()
	} else {
		fn.RawBody = ""
	}
	return true
}

// applyDefaultCustomRequestContentType applies VuGen's web_custom_request
// rule once every other source of a Content-Type (EncType, HEADERS,
// web_add_header, the SOAP importer's envelope inference) has had its say: a
// POST without an EncType attribute is sent as
// application/x-www-form-urlencoded, while an empty EncType= suppresses the
// header.
func applyDefaultCustomRequestContentType(action *dsl.Action, fn ParsedFunction) {
	if action == nil {
		return
	}
	contentType := defaultCustomRequestContentType(fn)
	if contentType == "" {
		return
	}
	if _, exists := headerValueCaseInsensitive(action.Headers, "Content-Type"); exists {
		return
	}
	if action.Headers == nil {
		action.Headers = make(map[string]string)
	}
	action.Headers["Content-Type"] = contentType
	if len(action.HeaderList) > 0 {
		action.HeaderList = append(action.HeaderList, dsl.HeaderEntry{Name: "Content-Type", Value: contentType})
	}
}

func defaultCustomRequestContentType(fn ParsedFunction) string {
	if fn.Name != "web_custom_request" || !strings.EqualFold(strings.TrimSpace(fn.Method), "POST") {
		return ""
	}
	if _, present := sourceNamedArg(fn.Args, "EncType"); present {
		return ""
	}
	for _, key := range []string{"ContentType", "Content-Type"} {
		if _, present := sourceNamedArg(fn.Args, key); present {
			return ""
		}
	}
	return "application/x-www-form-urlencoded"
}
