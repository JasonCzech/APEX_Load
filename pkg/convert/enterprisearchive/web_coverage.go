package enterprisearchive

// web_coverage.go — the published LoadRunner Web (HTTP/HTML) function coverage
// matrix. It joins the embedded registry (name/family/support_mode) with a
// code-owned target+notes map so the same data backs both the generated
// evaluation doc (docs/LOADRUNNER_COVERAGE.md, via cmd/webcoverage)
// and the GET enterprise support-matrix endpoint.

//go:generate go run ../../../cmd/webcoverage -out ../../../docs/LOADRUNNER_COVERAGE.md

import (
	"fmt"
	"sort"
	"strings"
)

// WebFunctionCoverage is one row of the Web (HTTP/HTML) coverage matrix.
type WebFunctionCoverage struct {
	Function    string `json:"function"`
	Family      string `json:"family"`
	SupportMode string `json:"support_mode"`
	Target      string `json:"target"`
	Notes       string `json:"notes"`
}

type webCoverageDetail struct {
	target string
	notes  string
}

// webFunctionCoverageDetail documents, per published function, what the converter
// translates it into and any fidelity caveat. Keep it in sync with the handlers;
// the freshness test fails if a registry function is missing a detail entry.
var webFunctionCoverageDetail = map[string]webCoverageDetail{
	"web_url":                        {"HTTP request action", "GET/POST request from URL=."},
	"web_submit_data":                {"HTTP request action", "Method= + ITEMDATA form body."},
	"web_submit_form":                {"HTTP form step (page context)", "Submits the form on the current page that has every scripted field (then Action=, then Ordinal=): the page's live hidden fields, action URL, method and encoding, with scripted values overriding and STARTHIDDENS values filling names the page lacks. Frame-targeted forms keep the navigation gate."},
	"web_custom_request":             {"HTTP request action", "Arbitrary method/body."},
	"web_rest":                       {"HTTP request action", "REST method/body/HEADERS block."},
	"web_link":                       {"HTTP link step (page context)", "Follows the link on the current page whose visible text matches Text= (Ordinal=, /IC). A non-VuGen explicit URL= becomes a GET. Frame-targeted links keep the navigation gate."},
	"web_image":                      {"HTTP image step (page context)", "Clicks the image on the current page matching Alt=/Src= (Ordinal=): follows its anchor, its single image-map area, or submits the form of an input image. Image-map areas (MapName/AreaAlt) and frames keep the navigation gate."},
	"web_image_check":                {"HTTP page check (page context)", "Passes when the current page has an image matching Alt=/Src=; no request is sent. Honors the legacy image-and-text-check setting."},
	"web_reg_save_param":             {"Extractor (boundary/regex)", "LB=/RB= + ORD= on the next request."},
	"web_reg_save_param_ex":          {"Extractor (boundary)", "Extended boundary form."},
	"web_reg_save_param_regexp":      {"Extractor (regex)", "RegExp= + ordinal."},
	"web_reg_save_param_json":        {"Extractor (jsonpath)", "QueryString=/JSONPath=."},
	"web_reg_save_param_xpath":       {"Extractor (xpath)", "QueryString=/XPath=."},
	"web_reg_save_param_attrib":      {"Extractor (xpath)", "TagName=+Extract= (or Tag=/Attribute=) → //tag/@attr; element attributes before SEARCH_FILTERS become [@name='value'] predicates."},
	"web_reg_find":                   {"Assertion / count extractor", "Text= + optional SaveCount extractor; SaveCount without Fail is non-failing."},
	"web_find":                       {"Assertion (body_contains)", "Deprecated inline check; What= aliased to Text=."},
	"web_global_verification":        {"source_verification action", "Positional per-VUser registration with LR scope, case, failure mode and ID."},
	"web_global_verification_pause":  {"source_verification action", "Positional pause/resume of named per-VUser verification checks."},
	"web_global_verification_resume": {"source_verification action", "Positional pause/resume of named per-VUser verification checks."},
	"web_add_header":                 {"Request headers", "Applied to the next request."},
	"web_add_auto_header":            {"Request headers", "Persistent until reverted."},
	"web_revert_auto_header":         {"Request headers", "Removes one persistent auto header."},
	"web_remove_auto_header":         {"Request headers", "Removes one persistent auto header."},
	"web_cleanup_auto_headers":       {"Request headers", "Clears all persistent auto headers."},
	"web_save_header":                {"Import review", "Saving response headers to a parameter has no extractor equivalent."},
	"web_add_cookie":                 {"cookie_set action", "Positional write to the VUser's cookie jar; a later Set-Cookie from the server replaces it."},
	"web_add_cookie_ex":              {"cookie_set action", "Positional write to the VUser's cookie jar; a later Set-Cookie from the server replaces it."},
	"web_reg_add_cookie":             {"Cookie headers", "Registered cookie attached downstream."},
	"web_remove_cookie":              {"cookie_set action (remove)", "Expires a cookie added with web_add_cookie; other names (or ALL) stop a registered cookie attaching downstream."},
	"web_cleanup_cookies":            {"cookie_clear action", "Executable positional cookie clear."},
	"web_set_user":                   {"config.http.auth (hosts-scoped)", "Basic credentials sent only to the host:port it names (config.http.auth.hosts); a second, different credential set is execution-gated."},
	"web_set_timeout":                {"config.http timeouts", "Connect/receive/SSL timeouts."},
	"web_set_sockets_option":         {"config.http (TLS/conns)", "SSL version, keep-alive, max connections."},
	"web_set_option":                 {"config.web_runtime / config.http", "Known options mapped to config; others preserved as attributes."},
	"web_set_max_retries":            {"config.error_handling.max_retries", "Retry count."},
	"web_disable_keep_alive":         {"config.http.keep_alive=false", "Disables persistent connections."},
	"web_enable_keep_alive":          {"config.http.keep_alive=true", "Enables persistent connections."},
	"web_set_certificate":            {"Import review", "Selects a cert by store index; no file path to map."},
	"web_set_certificate_ex":         {"config.http.tls client cert", "CertFilePath=/KeyFilePath=."},
	"web_set_proxy":                  {"config.http.proxy", "Custom proxy URL."},
	"web_set_secure_proxy":           {"config.http.proxy", "Custom HTTPS proxy URL."},
	"web_set_proxy_bypass":           {"config.http.proxy.no_proxy", "Bypass hosts."},
	"web_set_proxy_bypass_local":     {"config.http.proxy.no_proxy", "Adds local bypass hosts."},
	"web_set_pac":                    {"Import review", "PAC auto-config is not executed."},
	"web_set_max_html_param_len":     {"config.web_runtime attribute", "Native no-op compatibility attribute."},
	"web_cache_cleanup":              {"source_runtime action", "Executable positional clear of the simulated HTTP response cache."},
	"web_load_cache":                 {"config.web_runtime attribute", "No client cache; native no-op."},
	"web_dump_cache":                 {"config.web_runtime attribute", "No client cache; native no-op."},
	"web_add_filter":                 {"config.web_runtime attribute", "No auto-resource fetch; native no-op."},
	"web_add_auto_filter":            {"config.web_runtime attribute", "No auto-resource fetch; native no-op."},
	"web_remove_auto_filter":         {"config.web_runtime attribute", "No auto-resource fetch; native no-op."},
	"web_concurrent_start":           {"Concurrent http_sequence boundary", "Only matched same-scope groups of 1–256 independent HTTP requests; a request wrapped in its own LR_AUTO transaction records that transaction, timed by the request; barriers, conflicts, waits and malformed boundaries require review."},
	"web_concurrent_end":             {"Concurrent http_sequence boundary", "Closes a validated explicit source group; unsupported spans remain execution-gated review cases."},
	"web_convert_param":              {"source_parameter action", "Encoding/format conversion."},
	"web_convert_date_param":         {"source_parameter action", "Rewrites the date parameter in place from SourceFormat to TargetFormat (strftime specifiers); unsupported specifiers keep review."},
	"web_convert_from_formatted":     {"Import review", "Formatted-string parse is preserved for review; no APEX runtime equivalent yet."},
	"web_convert_to_formatted":       {"Import review", "Formatted-string build is preserved for review; no APEX runtime equivalent yet."},
	"web_sjis_to_euc_param":          {"Import review", "SJIS to EUC conversion is preserved for review; no APEX runtime equivalent yet."},
	"web_save_param_length":          {"source_parameter action", "Saves the decimal length of a parameter as <Param>_Length; Base=Hexadecimal keeps review."},
	"web_save_timestamp_param":       {"source_parameter action", "Saves the current time in milliseconds since the Unix epoch."},
	"web_get_int_property":           {"Import review", "HTTP property capture is preserved for review; no APEX runtime equivalent yet."},
	"web_util_set_request_body":      {"Import review", "Next-request body overrides are preserved for review; no APEX runtime equivalent yet."},
	"web_util_set_request_url":       {"Import review", "Next-request URL overrides are preserved for review; no APEX runtime equivalent yet."},
	"web_report_data_point":          {"source_metric action", "Custom data point, like lr_user_data_point."},
	"web_reg_async_attributes":       {"Import review", "Async polling/push has no APEX runtime equivalent."},
	"web_stop_async":                 {"Import review", "Async polling/push has no APEX runtime equivalent."},
	"web_js_run":                     {"Import review", "TruClient JavaScript step is not executed."},
	"web_js_reset":                   {"Import review", "TruClient JavaScript step is not executed."},
	"web_sync":                       {"Import review", "Async page-sync has no APEX runtime equivalent."},
	"web_stream_open":                {"Import review", "Media streaming has no APEX runtime equivalent."},
	"web_stream_close":               {"Import review", "Media streaming has no APEX runtime equivalent."},
	"web_stream_play":                {"Import review", "Media streaming has no APEX runtime equivalent."},
	"web_stream_pause":               {"Import review", "Media streaming has no APEX runtime equivalent."},
	"web_stream_stop":                {"Import review", "Media streaming has no APEX runtime equivalent."},
	"web_stream_seek":                {"Import review", "Media streaming has no APEX runtime equivalent."},
	"web_stream_wait":                {"Import review", "Media streaming has no APEX runtime equivalent."},
	"web_stream_get_param_double":    {"Import review", "Media streaming has no APEX runtime equivalent."},
	"web_stream_get_param_int":       {"Import review", "Media streaming has no APEX runtime equivalent."},
	"web_stream_get_param_string":    {"Import review", "Media streaming has no APEX runtime equivalent."},
	"web_stream_set_param_double":    {"Import review", "Media streaming has no APEX runtime equivalent."},
	"web_stream_set_param_int":       {"Import review", "Media streaming has no APEX runtime equivalent."},
}

// WebHTTPFunctionCoverage returns the full published Web (HTTP/HTML) coverage
// matrix, sorted alphabetically by function name.
func WebHTTPFunctionCoverage() []WebFunctionCoverage {
	defs := allWebHTTPFunctionDefinitions()
	rows := make([]WebFunctionCoverage, 0, len(defs))
	for _, def := range defs {
		detail := webFunctionCoverageDetail[def.Name]
		rows = append(rows, WebFunctionCoverage{
			Function:    def.Name,
			Family:      def.Family,
			SupportMode: string(def.SupportMode),
			Target:      detail.target,
			Notes:       detail.notes,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Function < rows[j].Function })
	return rows
}

// RenderWebHTTPFunctionCoverageMarkdown renders the coverage matrix as the
// committed evaluation document. The byte output is pinned by the freshness test.
func RenderWebHTTPFunctionCoverageMarkdown() string {
	rows := WebHTTPFunctionCoverage()

	counts := map[string]int{}
	for _, row := range rows {
		counts[row.SupportMode]++
	}

	var b strings.Builder
	b.WriteString("## Web function coverage\n\n")
	b.WriteString("Evaluation of the complete published OpenText/Micro Focus VuGen 26.1 **Web (HTTP/HTML)** ")
	b.WriteString("protocol function library against the APEX Load conversion engine ")
	b.WriteString("(`pkg/convert/enterprisearchive`). Source: VuGen Function Reference, ")
	b.WriteString("\"Alphabetical Listing of Web Functions\".\n\n")

	fmt.Fprintf(&b, "**Total published functions:** %d\n\n", len(rows))
	b.WriteString("| Disposition | Count | Meaning |\n")
	b.WriteString("|---|---|---|\n")
	fmt.Fprintf(&b, "| `executable` | %d | Translated to an executable DSL action or config mapping. |\n", counts["executable"])
	fmt.Fprintf(&b, "| `native_noop` | %d | Preserved as a runtime-compatibility attribute; a guaranteed no-op in APEX Load (no client cache / no auto-resource fetch). |\n", counts["native_noop"])
	fmt.Fprintf(&b, "| `review_only` | %d | Preserved in the import-review artifact; no APEX runtime equivalent, so it is not executed (never fabricated). |\n\n", counts["review_only"])

	b.WriteString("Every published function resolves to one of the dispositions above — none falls through to a `custom_call` review stub. This is enforced by `TestWebHTTPFunctionRegistryResolvesEveryPublishedFunction`.\n\n")

	b.WriteString("| Function | Family | Support mode | Target | Notes |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s |\n",
			row.Function, row.Family, row.SupportMode, row.Target, row.Notes)
	}
	return b.String()
}
