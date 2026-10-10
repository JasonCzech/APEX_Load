package sample

// curl.go — split from converter.go (same package, behavior-preserving).

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"golang.org/x/net/http/httpguts"
)

type curlFormPart struct {
	Name        string
	Value       string
	Filename    string
	ContentType string
	File        bool
	Review      bool
}

const curlReviewReasonsMetadataKey = "curl_review_reasons"

func parseCurl(content, responseContent string, diagnostics *diag.Collector) ([]exchange, []string, error) {
	tokens, err := shellSplit(content)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse curl command: %w", err)
	}
	if len(tokens) == 0 || tokens[0] != "curl" {
		return nil, nil, errors.New("curl sample must start with 'curl'")
	}
	ex := exchange{Method: "GET", Headers: map[string]string{}}
	formParts := []curlFormPart{}
	dataParts := []string{}
	fileAssets := []dsl.DataSource{}
	dataAsQuery := false
	methodExplicit := false
	location := "curl command"
	// versionFlag is the last HTTP version option; like curl, the last one wins.
	versionFlag := ""
	for index := 1; index < len(tokens); index++ {
		token := tokens[index]
		switch {
		case curlHTTPVersionFlags[token]:
			versionFlag = token
		case token == "-X" || token == "--request":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a method after -X/--request")
			}
			ex.Method = strings.ToUpper(tokens[index])
			methodExplicit = true
		case strings.HasPrefix(token, "--request="):
			ex.Method = strings.ToUpper(strings.TrimPrefix(token, "--request="))
			methodExplicit = true
		case strings.HasPrefix(token, "-X") && token != "-X":
			ex.Method = strings.ToUpper(strings.TrimPrefix(token, "-X"))
			methodExplicit = true
		case token == "-H" || token == "--header":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a header value after -H/--header")
			}
			if !addCurlHeader(&ex, tokens[index]) {
				markCurlReview(&ex, "invalid_curl_header")
			}
		case strings.HasPrefix(token, "--header="):
			if !addCurlHeader(&ex, strings.TrimPrefix(token, "--header=")) {
				markCurlReview(&ex, "invalid_curl_header")
			}
		case strings.HasPrefix(token, "-H") && token != "-H":
			if !addCurlHeader(&ex, strings.TrimPrefix(token, "-H")) {
				markCurlReview(&ex, "invalid_curl_header")
			}
		case isCurlDataFlag(token):
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a body value after data flag")
			}
			if isCurlDataURLEncodeFlag(token) {
				value, review := encodeCurlDataValue(tokens[index], diagnostics, location)
				dataParts = append(dataParts, value)
				if review {
					markCurlReview(&ex, "unsupported_curl_data_urlencode")
				}
			} else {
				dataParts = append(dataParts, curlDataValue(token, tokens[index], diagnostics, location, &fileAssets))
			}
		case isCurlDataFlagWithValue(token):
			flag, value, _ := strings.Cut(token, "=")
			if flag == "--data-urlencode" {
				encoded, review := encodeCurlDataValue(value, diagnostics, location)
				dataParts = append(dataParts, encoded)
				if review {
					markCurlReview(&ex, "unsupported_curl_data_urlencode")
				}
			} else {
				dataParts = append(dataParts, curlDataValue(flag, value, diagnostics, location, &fileAssets))
			}
		case strings.HasPrefix(token, "-d") && token != "-d":
			dataParts = append(dataParts, curlDataValue("-d", strings.TrimPrefix(token, "-d"), diagnostics, location, &fileAssets))
		case token == "--json":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a JSON value after --json")
			}
			dataParts = append(dataParts, tokens[index])
			addCurlJSONHeaders(&ex)
		case strings.HasPrefix(token, "--json="):
			dataParts = append(dataParts, strings.TrimPrefix(token, "--json="))
			addCurlJSONHeaders(&ex)
		case token == "-G" || token == "--get":
			dataAsQuery = true
		case token == "-F" || token == "--form" || token == "--form-string":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a form value after -F/--form")
			}
			part := parseCurlFormPart(tokens[index], token == "--form-string", diagnostics, location, &fileAssets)
			formParts = append(formParts, part)
			if part.Review {
				markCurlReview(&ex, "invalid_curl_multipart_metadata")
			}
		case strings.HasPrefix(token, "--form="):
			part := parseCurlFormPart(strings.TrimPrefix(token, "--form="), false, diagnostics, location, &fileAssets)
			formParts = append(formParts, part)
			if part.Review {
				markCurlReview(&ex, "invalid_curl_multipart_metadata")
			}
		case strings.HasPrefix(token, "--form-string="):
			part := parseCurlFormPart(strings.TrimPrefix(token, "--form-string="), true, diagnostics, location, &fileAssets)
			formParts = append(formParts, part)
			if part.Review {
				markCurlReview(&ex, "invalid_curl_multipart_metadata")
			}
		case strings.HasPrefix(token, "-F") && token != "-F":
			part := parseCurlFormPart(strings.TrimPrefix(token, "-F"), false, diagnostics, location, &fileAssets)
			formParts = append(formParts, part)
			if part.Review {
				markCurlReview(&ex, "invalid_curl_multipart_metadata")
			}
		case token == "--url":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a URL after --url")
			}
			ex.URL = tokens[index]
		case strings.HasPrefix(token, "--url="):
			ex.URL = strings.TrimPrefix(token, "--url=")
		case token == "-A" || token == "--user-agent":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a value after -A/--user-agent")
			}
			addCurlGeneratedHeader(&ex, "User-Agent", tokens[index])
		case strings.HasPrefix(token, "--user-agent="):
			addCurlGeneratedHeader(&ex, "User-Agent", strings.TrimPrefix(token, "--user-agent="))
		case strings.HasPrefix(token, "-A") && token != "-A":
			addCurlGeneratedHeader(&ex, "User-Agent", strings.TrimPrefix(token, "-A"))
		case token == "-e" || token == "--referer":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a value after -e/--referer")
			}
			addCurlGeneratedHeader(&ex, "Referer", tokens[index])
		case strings.HasPrefix(token, "--referer="):
			addCurlGeneratedHeader(&ex, "Referer", strings.TrimPrefix(token, "--referer="))
		case strings.HasPrefix(token, "-e") && token != "-e":
			addCurlGeneratedHeader(&ex, "Referer", strings.TrimPrefix(token, "-e"))
		case token == "--max-time":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a value after --max-time")
			}
			var valid bool
			ex.TimeoutMs, valid = parseCurlSecondsMs(tokens[index])
			if !valid {
				markCurlReview(&ex, "invalid_curl_timeout")
				diagnostics.Add(diag.Warning, "cURL timeout", location, "an invalid timeout was omitted from a blocked draft")
			}
		case strings.HasPrefix(token, "--max-time="):
			var valid bool
			ex.TimeoutMs, valid = parseCurlSecondsMs(strings.TrimPrefix(token, "--max-time="))
			if !valid {
				markCurlReview(&ex, "invalid_curl_timeout")
				diagnostics.Add(diag.Warning, "cURL timeout", location, "an invalid timeout was omitted from a blocked draft")
			}
		case token == "-b" || token == "--cookie":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a value after -b/--cookie")
			}
			addCurlGeneratedHeader(&ex, "Cookie", tokens[index])
		case strings.HasPrefix(token, "--cookie="):
			addCurlGeneratedHeader(&ex, "Cookie", strings.TrimPrefix(token, "--cookie="))
		case strings.HasPrefix(token, "-b") && token != "-b":
			addCurlGeneratedHeader(&ex, "Cookie", strings.TrimPrefix(token, "-b"))
		case token == "-u" || token == "--user":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a value after -u/--user")
			}
			ex.SourceSecrets = credentialpolicy.AppendSourceCredentialValue(ex.SourceSecrets, tokens[index])
			addCurlGeneratedHeader(&ex, "Authorization", curlBasicAuthHeader(tokens[index]))
			diagnostics.Add(diag.Info, "cURL basic auth", location, "basic auth credentials were converted to a governed Authorization header")
		case strings.HasPrefix(token, "--user="):
			credentials := strings.TrimPrefix(token, "--user=")
			ex.SourceSecrets = credentialpolicy.AppendSourceCredentialValue(ex.SourceSecrets, credentials)
			addCurlGeneratedHeader(&ex, "Authorization", curlBasicAuthHeader(credentials))
			diagnostics.Add(diag.Info, "cURL basic auth", location, "basic auth credentials were converted to a Basic Authorization header; validate secret handling before replay")
		case strings.HasPrefix(token, "-u") && token != "-u":
			credentials := strings.TrimPrefix(token, "-u")
			ex.SourceSecrets = credentialpolicy.AppendSourceCredentialValue(ex.SourceSecrets, credentials)
			addCurlGeneratedHeader(&ex, "Authorization", curlBasicAuthHeader(credentials))
			diagnostics.Add(diag.Info, "cURL basic auth", location, "basic auth credentials were converted to a Basic Authorization header; validate secret handling before replay")
		case token == "-I" || token == "--head":
			ex.Method = "HEAD"
		case token == "-L" || token == "--location" || token == "--location-trusted":
			diagnostics.Add(diag.Info, "cURL redirect option", location, "redirect-following option "+token+" is not modeled in the generated script; configure redirect policy before replay if required")
			markCurlReview(&ex, "unsupported_curl_redirect")
		case token == "--compressed":
			if headerLookup(ex.Headers, "Accept-Encoding") == "" {
				addCurlGeneratedHeader(&ex, "Accept-Encoding", "gzip, deflate, br")
			}
		case isCurlFlag(token):
			diagnostics.Add(diag.Warning, "cURL option", location, "a source option is not modeled and requires review before replay")
			markCurlReview(&ex, "unsupported_curl_option")
			if _, value, found := strings.Cut(token, "="); found {
				ex.SourceSecrets = credentialpolicy.AppendSourceCredentialValue(ex.SourceSecrets, value)
			}
			if curlFlagLikelyTakesValue(token) && index+1 < len(tokens) && !isCurlFlag(tokens[index+1]) {
				index++
				ex.SourceSecrets = credentialpolicy.AppendSourceCredentialValue(ex.SourceSecrets, tokens[index])
			}
		default:
			if !strings.HasPrefix(token, "-") && ex.URL == "" {
				ex.URL = token
			} else if !strings.HasPrefix(token, "-") {
				diagnostics.Add(diag.Info, "cURL argument", location, "an extra positional argument was not converted")
				markCurlReview(&ex, "unsupported_curl_argument")
				ex.SourceSecrets = credentialpolicy.AppendSourceCredentialValue(ex.SourceSecrets, token)
			}
		}
	}
	if ex.URL == "" {
		return nil, nil, errors.New("curl sample did not include a URL")
	}
	applyCurlHTTPVersion(&ex, versionFlag, curlTransportFactsFrom(tokens[1:]), diagnostics, location)
	if len(dataParts) > 0 {
		if dataAsQuery {
			ex.URL = addCurlQueryData(ex.URL, dataParts)
			if !methodExplicit {
				ex.Method = "GET"
			}
			if ex.Method == "GET" {
				deleteHeader(ex.Headers, "Content-Type")
				ex.HeaderList = filterHeaderList(ex.HeaderList, "Content-Type")
			}
		} else {
			ex.Body = strings.Join(dataParts, "&")
			if !methodExplicit && ex.Method == "GET" {
				ex.Method = "POST"
			}
		}
	}
	if len(formParts) > 0 {
		if strings.TrimSpace(ex.Body) != "" {
			diagnostics.Add(diag.Warning, "cURL mixed body", location, "both data and multipart form flags were present; multipart form body was used and data flags require review")
			markCurlReview(&ex, "unsupported_curl_mixed_body")
			for _, value := range dataParts {
				ex.SourceSecrets = credentialpolicy.AppendSourceCredentialValue(ex.SourceSecrets, value)
			}
		}
		contentType := headerLookup(ex.Headers, "Content-Type")
		boundary := multipartBoundaryOrDefault(contentType, defaultCurlMultipartBoundary)
		if curlMultipartBoundaryOccurs(boundary, formParts) {
			for attempt := 1; ; attempt++ {
				candidate := defaultCurlMultipartBoundary + "-" + strconv.Itoa(attempt)
				if !curlMultipartBoundaryOccurs(candidate, formParts) {
					boundary = candidate
					break
				}
			}
			markCurlReview(&ex, "curl_multipart_boundary_rewritten")
		}
		ex.Body = curlMultipartBody(formParts, boundary)
		ensureMultipartContentTypeBoundary(ex.Headers, boundary)
		ex.HeaderList = reconcileMultipartBoundaryInList(ex.HeaderList, ex.Headers)
		if ex.Method == "GET" {
			ex.Method = "POST"
		}
	}
	var warnings []string
	if strings.TrimSpace(responseContent) != "" {
		resp, respWarnings, err := parseRawResponse(responseContent)
		if err != nil {
			return nil, nil, err
		}
		ex.Response = resp
		warnings = append(warnings, respWarnings...)
	}
	ex.FileAssets = fileAssets
	return []exchange{ex}, warnings, nil
}

func curlBasicAuthHeader(credentials string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials))
}

func addCurlHeader(ex *exchange, raw string) bool {
	key, value, ok := splitHeader(raw)
	if ok {
		ex.Headers[key] = value
		ex.HeaderList = append(ex.HeaderList, dsl.HeaderEntry{Name: key, Value: value})
	}
	return ok
}

func addCurlJSONHeaders(ex *exchange) {
	if headerLookup(ex.Headers, "Content-Type") == "" {
		addCurlGeneratedHeader(ex, "Content-Type", "application/json")
	}
	if headerLookup(ex.Headers, "Accept") == "" {
		addCurlGeneratedHeader(ex, "Accept", "application/json")
	}
}

func addCurlGeneratedHeader(ex *exchange, name, value string) {
	if ex == nil {
		return
	}
	ex.Headers[name] = value
	ex.HeaderList = append(ex.HeaderList, dsl.HeaderEntry{Name: name, Value: value})
}

func appendCurlData(ex *exchange, value string) {
	if ex.Body == "" {
		ex.Body = value
	} else {
		ex.Body += "&" + value
	}
	if ex.Method == "GET" {
		ex.Method = "POST"
	}
}

func isCurlDataURLEncodeFlag(token string) bool {
	return token == "--data-urlencode"
}

func isCurlDataFlag(token string) bool {
	switch token {
	case "-d", "--data", "--data-raw", "--data-binary", "--data-ascii", "--data-urlencode":
		return true
	default:
		return false
	}
}

func curlDataValue(flag string, raw string, diagnostics *diag.Collector, location string, assets *[]dsl.DataSource) string {
	if curlDataFlagReadsFile(flag) && strings.HasPrefix(raw, "@") {
		source := strings.TrimPrefix(raw, "@")
		name := sanitizeIdentifier(strings.TrimSuffix(baseName(source), fileExtension(baseName(source))))
		if name == "" || name == "value" {
			name = "body"
		}
		filename, _ := credentialpolicy.SanitizeMultipartDispositionToken(baseName(source))
		appendFileAsset(assets, fileAssetDataSource(name, filename, ""))
		diagnostics.Add(diag.Warning, "cURL data file", location, "a data flag references a local file; bind the generated file asset before replay")
		return "{{file." + name + "}}"
	}
	return raw
}

func curlDataFlagReadsFile(flag string) bool {
	switch flag {
	case "-d", "--data", "--data-binary", "--data-ascii":
		return true
	default:
		return false
	}
}

func encodeCurlDataValue(raw string, diagnostics *diag.Collector, location string) (string, bool) {
	review := false
	if strings.Contains(raw, "@") && !strings.Contains(raw, "=") {
		diagnostics.Add(diag.Warning, "cURL data-urlencode", location, "--data-urlencode file-reference syntax was approximated as an encoded literal; review the generated query/body value")
		review = true
	}
	if name, value, ok := strings.Cut(raw, "="); ok && name != "" {
		return url.QueryEscape(name) + "=" + url.QueryEscape(value), review
	}
	if strings.HasPrefix(raw, "=") {
		return url.QueryEscape(strings.TrimPrefix(raw, "=")), review
	}
	return url.QueryEscape(raw), review
}

func addCurlQueryData(rawURL string, dataParts []string) string {
	if len(dataParts) == 0 {
		return rawURL
	}
	separator := "?"
	if strings.Contains(rawURL, "?") {
		separator = "&"
		if strings.HasSuffix(rawURL, "?") || strings.HasSuffix(rawURL, "&") {
			separator = ""
		}
	}
	return rawURL + separator + strings.Join(dataParts, "&")
}

func parseCurlSecondsMs(value string) (int, bool) {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) ||
		seconds <= 0 || seconds > float64(math.MaxInt)/1000 {
		return 0, false
	}
	return int(seconds * 1000), true
}

func isCurlDataFlagWithValue(token string) bool {
	for _, prefix := range []string{"--data=", "--data-raw=", "--data-binary=", "--data-ascii=", "--data-urlencode="} {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}

func fileExtension(name string) string {
	if index := strings.LastIndex(name, "."); index >= 0 {
		return name[index:]
	}
	return ""
}

func parseCurlFormPart(raw string, forceLiteral bool, diagnostics *diag.Collector, location string, assets *[]dsl.DataSource) curlFormPart {
	name, value, ok := strings.Cut(raw, "=")
	name = strings.TrimSpace(name)
	review := false
	if !ok || name == "" {
		diagnostics.Add(diag.Warning, "cURL multipart form", location, "a form part did not use supported name=value syntax and was represented with a placeholder name")
		name = "field"
		value = raw
		review = true
	}
	name, changed := credentialpolicy.SanitizeMultipartDispositionToken(name)
	review = review || changed
	part := curlFormPart{Name: name, Value: value, Review: review}
	segments := strings.Split(value, ";")
	payload := segments[0]
	for _, segment := range segments[1:] {
		key, val, ok := strings.Cut(segment, "=")
		if !ok {
			part.Review = true
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "type":
			part.ContentType = strings.Trim(strings.TrimSpace(val), `"`)
		case "filename":
			part.Filename = strings.Trim(strings.TrimSpace(val), `"`)
		default:
			part.Review = true
		}
	}
	if part.ContentType != "" && !httpguts.ValidHeaderFieldValue(part.ContentType) {
		part.ContentType = ""
		part.Review = true
	}
	part.Filename, changed = credentialpolicy.SanitizeMultipartDispositionToken(part.Filename)
	part.Review = part.Review || changed
	if !forceLiteral && (strings.HasPrefix(payload, "@") || strings.HasPrefix(payload, "<")) {
		source := strings.TrimLeft(payload, "@<")
		if part.Filename == "" {
			part.Filename = baseName(source)
		}
		if part.Filename == "" {
			part.Filename = "upload.bin"
		}
		part.Filename, changed = credentialpolicy.SanitizeMultipartDispositionToken(part.Filename)
		part.Review = part.Review || changed
		part.Value = "{{file." + sanitizeIdentifier(name) + "}}"
		part.File = true
		appendFileAsset(assets, fileAssetDataSource(name, part.Filename, part.ContentType))
		diagnostics.Add(diag.Warning, "cURL multipart file", location, "a multipart part references a local file; bind the generated file asset before replay")
		return part
	}
	part.Value = payload
	if part.Review {
		diagnostics.Add(diag.Warning, "cURL multipart metadata", location, "invalid or unsupported multipart metadata was replaced in a blocked draft")
	}
	return part
}

func curlMultipartBody(parts []curlFormPart, boundary string) string {
	var b strings.Builder
	for _, part := range parts {
		name := escapeMultipartQuoted(part.Name)
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString(`Content-Disposition: form-data; name="` + name + `"`)
		if part.File {
			b.WriteString(`; filename="` + escapeMultipartQuoted(part.Filename) + `"`)
		}
		b.WriteString("\r\n")
		if part.ContentType != "" {
			b.WriteString("Content-Type: " + part.ContentType + "\r\n")
		}
		b.WriteString("\r\n")
		b.WriteString(part.Value + "\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.String()
}

func appendFileAsset(assets *[]dsl.DataSource, asset dsl.DataSource) {
	if assets == nil || strings.TrimSpace(asset.Name) == "" {
		return
	}
	for i := range *assets {
		if strings.EqualFold(strings.TrimSpace((*assets)[i].Name), strings.TrimSpace(asset.Name)) {
			if (*assets)[i].FileName == "" {
				(*assets)[i].FileName = asset.FileName
			}
			if (*assets)[i].ContentType == "" {
				(*assets)[i].ContentType = asset.ContentType
			}
			return
		}
	}
	*assets = append(*assets, asset)
}

func escapeMultipartQuoted(value string) string {
	return credentialpolicy.EscapeMultipartDispositionToken(value)
}

func curlMultipartBoundaryOccurs(boundary string, parts []curlFormPart) bool {
	for _, part := range parts {
		if strings.Contains(part.Name, boundary) ||
			strings.Contains(part.Filename, boundary) ||
			strings.Contains(part.ContentType, boundary) ||
			strings.Contains(part.Value, boundary) {
			return true
		}
	}
	return false
}

// curlHTTPVersionFlags are curl's HTTP version options. Each takes no value.
var curlHTTPVersionFlags = map[string]bool{
	"--http1.0": true, "-0": true, "--http1.1": true, "--http2": true,
	"--http2-prior-knowledge": true, "--http3": true, "--http3-only": true,
}

// curlTransportFacts are the source options that decide whether exact
// HTTP/3 is representable: a proxy route, and the TLS maximum.
type curlTransportFacts struct {
	proxy  bool
	tlsMax string
}

func curlTransportFactsFrom(tokens []string) curlTransportFacts {
	var facts curlTransportFacts
	for index, token := range tokens {
		name, value, inline := strings.Cut(token, "=")
		switch {
		case name == "-x" || name == "--proxy" || name == "--preproxy" || strings.HasPrefix(name, "--socks"):
			facts.proxy = true
		case strings.HasPrefix(token, "-x") && len(token) > 2:
			facts.proxy = true
		case name == "--tls-max":
			if !inline && index+1 < len(tokens) {
				value = tokens[index+1]
			}
			facts.tlsMax = strings.TrimSpace(value)
		}
	}
	return facts
}

// HTTP/3 review codes for cURL sources.
const (
	// curlHTTP3RaceNotModeled: --http3 races HTTP/3 against TCP; native replay
	// does not model the race. The finding proposes auto+h3 as an explicit,
	// approximate rewrite the author can accept.
	curlHTTP3RaceNotModeled = "curl_http3_race_not_modeled"
	// http3RequiresHTTPS: --http3-only on an http:// URL, which curl refuses.
	http3RequiresHTTPS = "http3_requires_https"
	// http3WithProxy: --http3-only through a proxy; HTTP/3 has no proxy route.
	http3WithProxy = "http3_with_proxy"
	// http3RequiresTLS13: --http3-only with a TLS maximum below 1.3.
	http3RequiresTLS13 = "http3_requires_tls13"
)

// HTTPVersionRewriteProposalKey records an explicit, approximate policy the
// author may accept for a gated source version; accepting it is an edit, and
// the gate stays until then.
const HTTPVersionRewriteProposalKey = "http_version_rewrite_proposal"

// applyCurlHTTPVersion maps the last curl version option to a replay policy,
// or keeps a review gate when the runtime cannot reproduce it:
//
//	--http1.1                 exact HTTP/1.1
//	--http2-prior-knowledge   exact HTTP/2 (prior-knowledge h2c) on http://;
//	                          automatic on https://, where curl still negotiates
//	--http2 on https://       automatic negotiation, as curl's ALPN offer
//	--http2 on http://        gated: curl sends Upgrade: h2c, which is not modeled
//	--http1.0, -0             gated: HTTP/1.0 is not a native replay version
//	--http3-only on https://  exact HTTP/3 (3); gated through a proxy, with a
//	                          TLS maximum below 1.3, or on http://
//	--http3 on https://       gated: the HTTP/3-versus-TCP race is not modeled;
//	                          auto+h3 is proposed as an approximate rewrite
//	--http3 on http://        no effect, as in curl (HTTP/3 is HTTPS only)
func applyCurlHTTPVersion(ex *exchange, flag string, facts curlTransportFacts, diagnostics *diag.Collector, location string) {
	if flag == "" {
		return
	}
	cleartext := strings.HasPrefix(strings.ToLower(strings.TrimSpace(ex.URL)), "http://")
	switch flag {
	case "--http1.1":
		ex.HTTPVersion = httpprotocol.VersionHTTP11
		diagnostics.Add(diag.Info, "cURL HTTP version", location, "--http1.1 maps to the exact HTTP/1.1 replay policy")
	case "--http2-prior-knowledge":
		if !cleartext {
			// curl applies prior knowledge only to non-TLS requests; HTTPS
			// still negotiates through ALPN and can fall back to HTTP/1.1.
			diagnostics.Add(diag.Info, "cURL HTTP version", location, "--http2-prior-knowledge on an https:// URL maps to automatic negotiation, as curl still negotiates TLS requests through ALPN")
			return
		}
		ex.HTTPVersion = httpprotocol.VersionHTTP2
		diagnostics.Add(diag.Info, "cURL HTTP version", location, "--http2-prior-knowledge maps to the exact HTTP/2 replay policy (prior-knowledge h2c)")
	case "--http2":
		if cleartext {
			diagnostics.Add(diag.Warning, "cURL HTTP version", location, "--http2 on an http:// URL makes curl attempt an Upgrade: h2c, which native replay does not model; choose HTTP/1.1 or prior-knowledge HTTP/2 before replay")
			markCurlReview(ex, "curl_http2_upgrade_not_modeled")
			return
		}
		diagnostics.Add(diag.Info, "cURL HTTP version", location, "--http2 maps to automatic negotiation (HTTP/2 with HTTP/1.1 fallback), as curl's ALPN offer does")
	case "--http1.0", "-0":
		diagnostics.Add(diag.Warning, "cURL HTTP version", location, flag+" requests HTTP/1.0, which native replay does not support; choose HTTP/1.1 before replay")
		markCurlReview(ex, "curl_http10_not_supported")
	case "--http3-only":
		switch {
		case cleartext:
			diagnostics.Add(diag.Warning, "cURL HTTP version", location, "--http3-only on an http:// URL cannot run: HTTP/3 serves only https origins; correct the URL or the option before replay")
			markCurlReview(ex, http3RequiresHTTPS)
		case facts.proxy:
			diagnostics.Add(diag.Warning, "cURL HTTP version", location, "--http3-only requires HTTP/3, which no proxy route carries; remove the proxy or choose another HTTP version before replay")
			markCurlReview(ex, http3WithProxy)
		case facts.tlsMax != "" && facts.tlsMax != "1.3" && !strings.EqualFold(facts.tlsMax, "default"):
			diagnostics.Add(diag.Warning, "cURL HTTP version", location, "--http3-only requires TLS 1.3, but --tls-max is lower; correct one of them before replay")
			markCurlReview(ex, http3RequiresTLS13)
		default:
			ex.HTTPVersion = httpprotocol.VersionHTTP3
			diagnostics.Add(diag.Info, "cURL HTTP version", location, "--http3-only maps to exact HTTP/3 (3), which fails rather than falls back; it runs where HTTP/3 execution is enabled")
		}
	case "--http3":
		if cleartext {
			diagnostics.Add(diag.Info, "cURL HTTP version", location, "--http3 has no effect on an http:// URL (HTTP/3 serves only https origins); the request uses automatic negotiation")
			return
		}
		diagnostics.Add(diag.Warning, "cURL HTTP version", location, "--http3 races HTTP/3 against TCP, which native replay does not model; accept the approximate rewrite to auto+h3 (TCP until the origin advertises HTTP/3) or choose another HTTP version before replay")
		markCurlReview(ex, curlHTTP3RaceNotModeled)
		if ex.Metadata == nil {
			ex.Metadata = make(map[string]any)
		}
		ex.Metadata[HTTPVersionRewriteProposalKey] = map[string]any{"version": 1, "policy": httpprotocol.VersionAutoH3, "reason_code": curlHTTP3RaceNotModeled, "approximate": true}
	}
}

func markCurlReview(ex *exchange, reason string) {
	if ex == nil || strings.TrimSpace(reason) == "" {
		return
	}
	if ex.Metadata == nil {
		ex.Metadata = make(map[string]any)
	}
	reasons, _ := ex.Metadata[curlReviewReasonsMetadataKey].([]string)
	for _, existing := range reasons {
		if existing == reason {
			return
		}
	}
	reasons = append(reasons, reason)
	sort.Strings(reasons)
	ex.Metadata[curlReviewReasonsMetadataKey] = reasons
}

func isCurlFlag(token string) bool {
	return strings.HasPrefix(token, "-") && token != "-"
}

func curlFlagLikelyTakesValue(token string) bool {
	if strings.Contains(token, "=") {
		return false
	}
	switch token {
	case "--connect-timeout", "--max-time", "--retry", "--retry-delay", "--proxy", "--cacert", "--cert", "--key", "--resolve", "--interface", "--limit-rate", "--request-target", "--output", "-o", "--write-out", "-w",
		"--tls-max", "--preproxy", "--socks4", "--socks4a", "--socks5", "--socks5-hostname", "--proxy-user":
		return true
	default:
		return len(token) == 2 && token != "-L" && token != "-I" && token != "-k" && token != "-s" && token != "-v"
	}
}

func shellSplit(input string) ([]string, error) {
	input = normalizeCurlContinuations(input)
	var tokens []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, char := range input {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}
		switch char {
		case '\\':
			escaped = true
		case '\'', '"':
			if quote == 0 {
				quote = char
				continue
			}
			if quote == char {
				quote = 0
				continue
			}
			current.WriteRune(char)
		case ' ', '\n', '\t':
			if quote != 0 {
				current.WriteRune(char)
				continue
			}
			flush()
		default:
			current.WriteRune(char)
		}
	}
	if escaped || quote != 0 {
		return nil, errors.New("unterminated quoted string in curl sample")
	}
	flush()
	return tokens, nil
}

func normalizeCurlContinuations(input string) string {
	input = strings.ReplaceAll(input, "\\\r\n", " ")
	return strings.ReplaceAll(input, "\\\n", " ")
}
