package enterprisearchive

// action_convert.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// convertAction converts a single web_ function to a DSL Action.
func convertAction(fn ParsedFunction, baseURL, transaction string, subTransaction string, opts ConvertOptions, previousRequestURL string) (*dsl.Action, []string) {
	var warnings []string

	action := &dsl.Action{
		Headers:  make(map[string]string),
		Metadata: sourceFunctionMetadata(fn),
	}

	applyExtraResources(fn, action)

	// Determine name
	name := sanitizeName(fn.StepName)
	if transaction != "" {
		action.Metadata["source_transaction"] = transaction
		if opts.PreserveTransactions {
			name = sanitizeName(transaction) + "_" + name
		}
	}
	if subTransaction != "" {
		action.Metadata["source_subtransaction"] = subTransaction
		if _, exists := action.Metadata["source_action_name"]; !exists {
			action.Metadata["source_action_name"] = subTransaction
		}
	}
	action.Name = name

	// Set method and type
	action.Method = fn.Method
	action.Type = fn.Method
	if action.Type == "" {
		action.Type = "GET"
		action.Method = "GET"
	}

	// Determine target URL (relative to base)
	rawURL := sourceRequestContextURL(fn, previousRequestURL)
	generatedSOAPEndpoint := false
	if rawURL == "" && fn.Name == "web_service_call" {
		rawURL = sourceSOAPEndpointPlaceholder(fn)
		generatedSOAPEndpoint = rawURL != ""
	}
	if rawURL == "" {
		warnings = append(warnings, fmt.Sprintf("line %d: %s '%s' has no URL, skipping", fn.LineNumber, fn.Name, fn.StepName))
		return nil, warnings
	}
	if generatedSOAPEndpoint {
		action.Metadata["soap_endpoint_placeholder"] = rawURL
		action.Metadata["review_required"] = true
		warnings = append(warnings, fmt.Sprintf("line %d: web_service_call '%s' has no inline endpoint; using %s until WSDL/service settings are mapped", fn.LineNumber, fn.StepName, rawURL))
	}
	if fn.URL == "" && fn.Name == "web_submit_form" {
		action.Metadata["inferred_url_from"] = "source_form_context"
		action.Metadata["inferred_url_source"] = convertutil.FirstNonEmpty(firstNonEmptyArg(fn, "Referer"), previousRequestURL)
	}
	applyHTMLFormReview(action, fn)

	target := makeRelative(rawURL, baseURL)
	action.Target = target
	// Credential projection validates this provenance URL alongside the wire
	// target. Use the same template dialect as the target so a valid LR host
	// parameter is not mistaken for a literal, malformed authority. The source
	// review retains the original C spelling and its line mapping.
	action.Metadata["original_url"] = convertParamReferences(rawURL)

	// Apply headers
	for _, header := range fn.HeaderList {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		value := convertParamReferences(header.Value)
		action.HeaderList = append(action.HeaderList, dsl.HeaderEntry{Name: name, Value: value})
	}
	if len(action.HeaderList) > 0 {
		action.Headers = action.HeaderList.ToMap()
	}
	for k, v := range fn.Headers {
		if _, exists := headerValueCaseInsensitive(action.Headers, k); exists {
			continue
		}
		action.Headers[k] = convertParamReferences(v)
	}
	applySourceRefererHeader(action, fn)
	if contentType := requestContentTypeFromFunction(fn); contentType != "" {
		if _, exists := headerValueCaseInsensitive(action.Headers, "Content-Type"); !exists {
			value := convertParamReferences(contentType)
			action.Headers["Content-Type"] = value
			if len(action.HeaderList) > 0 {
				action.HeaderList = append(action.HeaderList, dsl.HeaderEntry{Name: "Content-Type", Value: value})
			}
		}
	}

	// Apply body
	if fn.Body != "" {
		// The C parser and the Java string parser each decode source literals
		// exactly once; decoding again would corrupt literal backslashes.
		action.Body = fn.Body
		// Convert source parameter syntax {ParamName} to template syntax.
		// Known .prm-backed parameters are rewritten from var to param after all archive files are parsed.
		action.Body = convertParamReferences(action.Body)
	}
	if err := encodeImportedBody(action, fn, opts.sourceEncoding); err != nil {
		action.Metadata["review_required"] = true
		action.Metadata["source_encoding_error"] = err.Error()
		warnings = append(warnings, fmt.Sprintf("line %d: source body encoding requires review: %s", fn.LineNumber, err))
		// Fail closed rather than sending a lossy replacement payload.
		action.Type = "unsupported"
		action.Body = ""
	}
	if fn.itemDataURLForm {
		// Literal form spans are encoded during import. Runtime templates must be
		// encoded after their values are resolved so delimiters in parameter data
		// cannot become new fields. The engine consumes this durable marker while
		// preserving the ordinary {{var.*}}/{{param.*}} representation in the DSL.
		action.Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey] = dsl.ImportURLFormTemplateValueEncodingQueryComponent
	}
	if secretName := fn.Args[maskedBodySecretArgKey]; secretName != "" {
		// Safe-by-default invariant: a masked credential payload must never
		// reach action.Body verbatim. If the masked lr_unmask(...) call landed
		// in the body (e.g. "Body=lr_unmask(...)"), replace it with the
		// governed secret reference regardless of opts — every entry point
		// (Convert/ConvertDatabase/ConvertNetwork as well as ConvertZip) now
		// produces the reference, never the raw masked payload. The flag only
		// governs the cosmetic choice of materializing the reference into an
		// otherwise-empty body (the ConvertZip http/soap behavior).
		if _, masked := extractLRUnmaskPayload(action.Body); masked {
			action.Body = maskedBodySecretReference(secretName)
		} else if opts.UseMaskedBodySecretReferences {
			action.Body = maskedBodySecretReference(secretName)
		}
		action.Metadata["source_secret_ref"] = secretName
		action.Metadata["source_secret_function"] = convertutil.FirstNonEmpty(fn.Args[maskedBodyFuncArgKey], "lr_unmask")
		action.Metadata["body_gated_by_secret"] = true
		importreview.ApplyToAction(action, []importreview.Finding{{Code: "restored_body_required", ReasonCode: "restored_body_required", Severity: diag.Blocker, Gate: importreview.GateExecution, Source: "enterprise_script_archive", SourceRef: fmt.Sprintf("%s:%d", fn.Filename, fn.LineNumber), Resolution: "restore_body", Detail: "Supply the restored body or a project secret containing it."}})
	}
	multipartRebuilt, multipartReason, multipartFindings := rebuildSourceMultipart(action, fn, opts)
	if len(multipartFindings) > 0 {
		action.Body = ""
		action.BodyEncoding = ""
		deleteHeaderCaseInsensitive(action.Headers, "Content-Type")
		action.HeaderList = filterHeaderListCaseInsensitive(action.HeaderList, "Content-Type")
		action.Metadata["source_itemdata_encoding"] = "review_required"
		importreview.ApplyToAction(action, multipartFindings)
	} else if reason := strings.TrimSpace(fn.itemDataReviewReason); reason != "" && !multipartRebuilt {
		// The source review retains the original ITEMDATA call. Do not fabricate a
		// raw, JSON, or multipart body from only the Name/Value subset and do not
		// advertise its EncType on an empty/differently serialized payload.
		action.Body = ""
		deleteHeaderCaseInsensitive(action.Headers, "Content-Type")
		action.HeaderList = filterHeaderListCaseInsensitive(action.HeaderList, "Content-Type")
		action.Metadata["source_itemdata_encoding"] = "review_required"
		location := strings.TrimSpace(fn.Filename)
		if fn.LineNumber > 0 {
			location = fmt.Sprintf("%s:%d", location, fn.LineNumber)
		}
		detail := "The source ITEMDATA encoding or field shape cannot be serialized faithfully as an APEX URL-encoded body; rebuild the request body before execution."
		if multipartReason != "" {
			detail = "The multipart ITEMDATA cannot be serialized exactly: " + multipartReason + "; rebuild the request body before execution."
		}
		importreview.ApplyToAction(action, []importreview.Finding{{
			Code:       "import_review_required",
			Severity:   diag.Blocker,
			Element:    "web_submit_data ITEMDATA serialization",
			Detail:     detail,
			Location:   location,
			Source:     "enterprise_script_archive",
			ReasonCode: reason,
			SourceRef:  location,
			Resolution: "rebuild_body_and_remove_gate",
			Gate:       importreview.GateExecution,
		}})
		warnings = append(warnings, fmt.Sprintf("line %d: %s %q uses an unsupported ITEMDATA encoding or field shape; the request was preserved for review and blocked from execution", fn.LineNumber, fn.Name, fn.StepName))
	}

	warnings = append(warnings, annotateOpaqueTransformArgs(action, fn)...)
	// Convert parameter references in URL/target too
	action.Target = convertParamReferences(action.Target)

	return action, warnings
}

// applySourceRefererHeader sends a step's Referer= argument as the Referer
// header, as LoadRunner does. An empty Referer= sends none, and a Referer the
// step already sets explicitly (HEADERS or the header list) is kept.
func applySourceRefererHeader(action *dsl.Action, fn ParsedFunction) {
	if action == nil {
		return
	}
	referer, ok := sourceNamedArg(fn.Args, "Referer")
	referer = strings.TrimSpace(referer)
	if !ok || referer == "" {
		return
	}
	if _, exists := headerValueCaseInsensitive(action.Headers, "Referer"); exists {
		return
	}
	value := convertParamReferences(referer)
	if action.Headers == nil {
		action.Headers = make(map[string]string)
	}
	action.Headers["Referer"] = value
	if len(action.HeaderList) > 0 {
		action.HeaderList = append(action.HeaderList, dsl.HeaderEntry{Name: "Referer", Value: value})
	}
}

func filterHeaderListCaseInsensitive(headers dsl.HeaderList, name string) dsl.HeaderList {
	filtered := headers[:0]
	for _, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header.Name), name) {
			continue
		}
		filtered = append(filtered, header)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// reOpaqueSourceTransformCall matches request arguments produced by opaque
// source transforms: LR masking/encryption (lr_unmask / lr_decrypt /
// lr_decrypt_ex) may require an unavailable decoder or encryption key, so the plaintext is
// unrecoverable at import time. lr_decrypt_ex precedes lr_decrypt so the
// longer name wins the alternation.
var reOpaqueSourceTransformCall = regexp.MustCompile(`\b(lr_unmask|lr_decrypt_ex|lr_decrypt)\s*\(`)

// annotateOpaqueTransformArgs flags request arguments built from opaque
// transform calls so they never import as a silently empty body. The body is
// deliberately left empty — replaying machine-key-bound ciphertext would send
// garbage — and the original call text (including the masked hex) is preserved
// in metadata.masked_body_raw so the payload can be re-entered or re-masked
// externally.
func annotateOpaqueTransformArgs(action *dsl.Action, fn ParsedFunction) []string {
	var names []string
	var raws []string
	for _, arg := range fn.CallArgs {
		// Only call-shaped arguments count: a quoted string literal that
		// mentions the function name is data, not a call.
		if arg.Kind != "nested_call" && arg.Kind != "expression" {
			continue
		}
		match := reOpaqueSourceTransformCall.FindStringSubmatch(arg.Raw)
		if len(match) < 2 {
			continue
		}
		names = append(names, match[1])
		raws = append(raws, arg.Raw)
	}
	if len(names) == 0 {
		return nil
	}

	protection := "ordinary LR masking; restore the value in VuGen"
	action.Metadata["source_protection"] = "masking"
	for _, name := range names {
		if !strings.EqualFold(name, "lr_unmask") {
			protection = "encrypted payload; restore it using the original encryption configuration and key"
			action.Metadata["source_protection"] = "encrypted"
		}
	}
	importreview.ApplyToAction(action, []importreview.Finding{{Code: "restored_body_required", ReasonCode: "restored_body_required", Severity: diag.Blocker, Gate: importreview.GateExecution, Source: "enterprise_script_archive", SourceRef: fmt.Sprintf("%s:%d", fn.Filename, fn.LineNumber), Resolution: "restore_body", Detail: protection}})
	action.Metadata["masked_body"] = true
	action.Metadata["masked_body_function"] = names[0]
	action.Metadata["masked_body_raw"] = raws[0]
	if len(raws) > 1 {
		action.Metadata["masked_body_calls"] = raws
	}
	action.Metadata["review_required"] = true

	bodyNote := "the request body was left empty"
	if action.Body != "" {
		bodyNote = "the converted body may be incomplete"
	}
	return []string{fmt.Sprintf(
		"%s line %d: %s '%s' passes %s(...) as a request argument; APEX has no verified decoder for this %s — %s and the action requires manual review (original call preserved in metadata.masked_body_raw)",
		fn.Filename, fn.LineNumber, fn.Name, fn.StepName, strings.Join(uniqueSourceStrings(names), ", "), protection, bodyNote)}
}

func redactOpaqueTransformMetadata(action *dsl.Action) {
	if action == nil || action.Metadata == nil {
		return
	}
	functionName := metadataString(action.Metadata, "masked_body_function")
	if functionName == "" {
		functionName = metadataString(action.Metadata, "source_secret_function")
	}
	if functionName == "" {
		return
	}
	redacted := functionName + `("[REDACTED]")`
	if _, ok := action.Metadata["masked_body_raw"]; ok {
		action.Metadata["masked_body_raw"] = redacted
	}
	if _, ok := action.Metadata["masked_body_calls"]; ok {
		action.Metadata["masked_body_calls"] = []string{redacted}
	}
}

func uniqueSourceStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func sourceRequestContextURL(fn ParsedFunction, previousRequestURL string) string {
	if strings.TrimSpace(fn.URL) != "" {
		return strings.TrimSpace(fn.URL)
	}
	if fn.Name != "web_submit_form" {
		return ""
	}
	return inferSourceSubmitFormURL(fn, previousRequestURL)
}

func rawURLForCookieMatch(fn ParsedFunction, currentRequestURL string) string {
	if currentRequestURL != "" {
		return currentRequestURL
	}
	return sourceRequestContextURL(fn, "")
}

func applyPersistentCookies(action *dsl.Action, cookies []ParsedCookie, rawURL string) {
	if action == nil || len(cookies) == 0 {
		return
	}
	matches := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == "" || !cookieMatchesURL(cookie, rawURL) {
			continue
		}
		matches = append(matches, cookie.Name+"="+convertParamReferences(cookie.Value))
	}
	if len(matches) == 0 {
		return
	}
	if action.Headers == nil {
		action.Headers = make(map[string]string)
	}
	if existing, ok := headerValueCaseInsensitive(action.Headers, "Cookie"); ok && strings.TrimSpace(existing) != "" {
		matches = append([]string{existing}, matches...)
		deleteHeaderCaseInsensitive(action.Headers, "Cookie")
	}
	action.Headers["Cookie"] = strings.Join(matches, "; ")
}

// isHTMLModeNavigationFunction reports whether a request-shaped source function is
// an HTML-mode navigation step (web_link/web_image/web_image_check) whose target is
// resolved from page context rather than carried inline.
func isHTMLModeNavigationFunction(name string) bool {
	switch name {
	case "web_link", "web_image", "web_image_check":
		return true
	default:
		return false
	}
}

// sourceCookieRemovalName resolves the cookie name targeted by web_remove_cookie.
// The first positional argument may be a bare name ("SessionID") or a "Name=Value"
// pair; "ALL" targets the whole jar.
func sourceCookieRemovalName(fn ParsedFunction) string {
	args := sourceControlArgs(fn)
	if len(args) == 0 {
		return ""
	}
	first := cleanConfigValue(args[0])
	if name, _, ok := strings.Cut(first, "="); ok {
		return strings.TrimSpace(name)
	}
	return strings.TrimSpace(first)
}

// removePersistentCookie drops the cookie named by web_remove_cookie (or every
// cookie for "ALL") from the importer's persistent set so it stops attaching to
// downstream requests. It reports whether anything was removed.
func removePersistentCookie(cookies *[]ParsedCookie, fn ParsedFunction) bool {
	name := sourceCookieRemovalName(fn)
	if name == "" || cookies == nil {
		return false
	}
	if strings.EqualFold(name, "ALL") {
		had := len(*cookies) > 0
		*cookies = nil
		return had
	}
	filtered := (*cookies)[:0]
	removed := false
	for _, cookie := range *cookies {
		if strings.EqualFold(cookie.Name, name) {
			removed = true
			continue
		}
		filtered = append(filtered, cookie)
	}
	*cookies = filtered
	return removed
}

// upsertPersistentCookie mirrors the source cookie jar: adding a cookie whose
// name, domain and path match an earlier one replaces its value instead of
// sending both. Scripts that refresh RUM cookies before every request
// otherwise grow a Cookie header with dozens of stale copies.
func upsertPersistentCookie(cookies []ParsedCookie, cookie ParsedCookie) []ParsedCookie {
	for index := range cookies {
		if cookies[index].Name == cookie.Name &&
			normalizedCookieDomain(cookies[index].Domain) == normalizedCookieDomain(cookie.Domain) &&
			normalizedCookiePath(cookies[index].Path) == normalizedCookiePath(cookie.Path) {
			cookies[index] = cookie
			return cookies
		}
	}
	return append(cookies, cookie)
}

func normalizedCookieDomain(domain string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

func normalizedCookiePath(path string) string {
	if path = strings.TrimSpace(path); path == "" {
		return "/"
	}
	return path
}

func cookieMatchesURL(cookie ParsedCookie, rawURL string) bool {
	if strings.TrimSpace(cookie.Domain) == "" && strings.TrimSpace(cookie.Path) == "" {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" {
		return true
	}
	domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cookie.Domain)), ".")
	host := strings.ToLower(parsed.Hostname())
	if domain != "" && host != domain && !strings.HasSuffix(host, "."+domain) {
		return false
	}
	path := strings.TrimSpace(cookie.Path)
	if path == "" {
		return true
	}
	requestPath := parsed.EscapedPath()
	if requestPath == "" {
		requestPath = "/"
	}
	return strings.HasPrefix(requestPath, path)
}

func inferSourceSubmitFormURL(fn ParsedFunction, previousRequestURL string) string {
	candidate := cleanSourceSubmitFormStepName(fn.StepName)
	if candidate == "" {
		return ""
	}
	if isAbsoluteHTTPURL(candidate) {
		return candidate
	}
	base := convertutil.FirstNonEmpty(firstNonEmptyArg(fn, "Referer"), previousRequestURL)
	if base == "" {
		return candidate
	}
	return resolveSourceRelativeURL(candidate, base)
}

func cleanSourceSubmitFormStepName(stepName string) string {
	candidate := strings.TrimSpace(cleanConfigValue(stepName))
	if candidate == "" {
		return ""
	}
	withoutOrdinal := sourceReplayOrdinalSuffix.ReplaceAllString(candidate, "")
	if withoutOrdinal != candidate && strings.Contains(withoutOrdinal, ".") {
		candidate = withoutOrdinal
	}
	return candidate
}

func isAbsoluteHTTPURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func resolveSourceRelativeURL(candidate, base string) string {
	if !strings.ContainsAny(base, "{}") {
		if parsedBase, err := url.Parse(base); err == nil && parsedBase.Scheme != "" {
			if parsedRelative, relErr := url.Parse(candidate); relErr == nil {
				return parsedBase.ResolveReference(parsedRelative).String()
			}
		}
	}

	base = stripURLQueryAndFragment(strings.TrimSpace(base))
	if base == "" {
		return candidate
	}
	if strings.HasPrefix(candidate, "/") {
		if schemeIndex := strings.Index(base, "://"); schemeIndex >= 0 {
			hostStart := schemeIndex + len("://")
			if slashIndex := strings.Index(base[hostStart:], "/"); slashIndex >= 0 {
				return base[:hostStart+slashIndex] + candidate
			}
			return base + candidate
		}
		return candidate
	}
	if !strings.HasSuffix(base, "/") {
		if slashIndex := strings.LastIndex(base, "/"); slashIndex >= 0 {
			base = base[:slashIndex+1]
		} else {
			base += "/"
		}
	}
	return base + candidate
}

func stripURLQueryAndFragment(value string) string {
	if index := strings.IndexAny(value, "?#"); index >= 0 {
		return value[:index]
	}
	return value
}
