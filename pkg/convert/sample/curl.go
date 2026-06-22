package sample

// curl.go — split from converter.go (same package, behavior-preserving).

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type curlFormPart struct {
	Name        string
	Value       string
	Filename    string
	ContentType string
	File        bool
}

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
	for index := 1; index < len(tokens); index++ {
		token := tokens[index]
		switch {
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
			addCurlHeader(&ex, tokens[index])
		case strings.HasPrefix(token, "--header="):
			addCurlHeader(&ex, strings.TrimPrefix(token, "--header="))
		case strings.HasPrefix(token, "-H") && token != "-H":
			addCurlHeader(&ex, strings.TrimPrefix(token, "-H"))
		case isCurlDataFlag(token):
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a body value after data flag")
			}
			if isCurlDataURLEncodeFlag(token) {
				dataParts = append(dataParts, encodeCurlDataValue(tokens[index], diagnostics, location))
			} else {
				dataParts = append(dataParts, curlDataValue(token, tokens[index], diagnostics, location, &fileAssets))
			}
		case isCurlDataFlagWithValue(token):
			flag, value, _ := strings.Cut(token, "=")
			if flag == "--data-urlencode" {
				dataParts = append(dataParts, encodeCurlDataValue(value, diagnostics, location))
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
			addCurlJSONHeaders(ex.Headers)
		case strings.HasPrefix(token, "--json="):
			dataParts = append(dataParts, strings.TrimPrefix(token, "--json="))
			addCurlJSONHeaders(ex.Headers)
		case token == "-G" || token == "--get":
			dataAsQuery = true
		case token == "-F" || token == "--form" || token == "--form-string":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a form value after -F/--form")
			}
			formParts = append(formParts, parseCurlFormPart(tokens[index], token == "--form-string", diagnostics, location, &fileAssets))
		case strings.HasPrefix(token, "--form="):
			formParts = append(formParts, parseCurlFormPart(strings.TrimPrefix(token, "--form="), false, diagnostics, location, &fileAssets))
		case strings.HasPrefix(token, "--form-string="):
			formParts = append(formParts, parseCurlFormPart(strings.TrimPrefix(token, "--form-string="), true, diagnostics, location, &fileAssets))
		case strings.HasPrefix(token, "-F") && token != "-F":
			formParts = append(formParts, parseCurlFormPart(strings.TrimPrefix(token, "-F"), false, diagnostics, location, &fileAssets))
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
			if index < len(tokens) {
				ex.Headers["User-Agent"] = tokens[index]
			}
		case strings.HasPrefix(token, "--user-agent="):
			ex.Headers["User-Agent"] = strings.TrimPrefix(token, "--user-agent=")
		case strings.HasPrefix(token, "-A") && token != "-A":
			ex.Headers["User-Agent"] = strings.TrimPrefix(token, "-A")
		case token == "-e" || token == "--referer":
			index++
			if index < len(tokens) {
				ex.Headers["Referer"] = tokens[index]
			}
		case strings.HasPrefix(token, "--referer="):
			ex.Headers["Referer"] = strings.TrimPrefix(token, "--referer=")
		case strings.HasPrefix(token, "-e") && token != "-e":
			ex.Headers["Referer"] = strings.TrimPrefix(token, "-e")
		case token == "--max-time":
			index++
			if index < len(tokens) {
				ex.TimeoutMs = parseCurlSecondsMs(tokens[index])
			}
		case strings.HasPrefix(token, "--max-time="):
			ex.TimeoutMs = parseCurlSecondsMs(strings.TrimPrefix(token, "--max-time="))
		case token == "-b" || token == "--cookie":
			index++
			if index < len(tokens) {
				ex.Headers["Cookie"] = tokens[index]
			}
		case strings.HasPrefix(token, "--cookie="):
			ex.Headers["Cookie"] = strings.TrimPrefix(token, "--cookie=")
		case strings.HasPrefix(token, "-b") && token != "-b":
			ex.Headers["Cookie"] = strings.TrimPrefix(token, "-b")
		case token == "-u" || token == "--user":
			index++
			if index < len(tokens) {
				ex.Headers["Authorization"] = curlBasicAuthHeader(tokens[index])
				diagnostics.Add(diag.Info, "cURL basic auth", location, "basic auth credentials were converted to a Basic Authorization header; validate secret handling before replay")
			}
		case strings.HasPrefix(token, "--user="):
			ex.Headers["Authorization"] = curlBasicAuthHeader(strings.TrimPrefix(token, "--user="))
			diagnostics.Add(diag.Info, "cURL basic auth", location, "basic auth credentials were converted to a Basic Authorization header; validate secret handling before replay")
		case strings.HasPrefix(token, "-u") && token != "-u":
			ex.Headers["Authorization"] = curlBasicAuthHeader(strings.TrimPrefix(token, "-u"))
			diagnostics.Add(diag.Info, "cURL basic auth", location, "basic auth credentials were converted to a Basic Authorization header; validate secret handling before replay")
		case token == "-I" || token == "--head":
			ex.Method = "HEAD"
		case token == "-L" || token == "--location" || token == "--location-trusted":
			diagnostics.Add(diag.Info, "cURL redirect option", location, "redirect-following option "+token+" is not modeled in the generated script; configure redirect policy before replay if required")
		case token == "--compressed":
			if headerLookup(ex.Headers, "Accept-Encoding") == "" {
				ex.Headers["Accept-Encoding"] = "gzip, deflate, br"
			}
		case isCurlFlag(token):
			diagnostics.Add(diag.Warning, "cURL option", location, "option "+token+" is not modeled by the sample importer; review equivalent runtime behavior before replay")
			if curlFlagLikelyTakesValue(token) && index+1 < len(tokens) && !isCurlFlag(tokens[index+1]) {
				index++
			}
		default:
			if !strings.HasPrefix(token, "-") && ex.URL == "" {
				ex.URL = token
			} else if !strings.HasPrefix(token, "-") {
				diagnostics.Add(diag.Info, "cURL argument", location, "extra positional argument "+token+" was not converted")
			}
		}
	}
	if ex.URL == "" {
		return nil, nil, errors.New("curl sample did not include a URL")
	}
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
		}
		contentType := headerLookup(ex.Headers, "Content-Type")
		boundary := multipartBoundaryOrDefault(contentType, defaultCurlMultipartBoundary)
		ex.Body = curlMultipartBody(formParts, boundary)
		ensureMultipartContentTypeBoundary(ex.Headers, boundary)
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

func addCurlHeader(ex *exchange, raw string) {
	key, value, ok := splitHeader(raw)
	if ok {
		ex.Headers[key] = value
		ex.HeaderList = append(ex.HeaderList, dsl.HeaderEntry{Name: key, Value: value})
	}
}

func addCurlJSONHeaders(headers map[string]string) {
	if headerLookup(headers, "Content-Type") == "" {
		headers["Content-Type"] = "application/json"
	}
	if headerLookup(headers, "Accept") == "" {
		headers["Accept"] = "application/json"
	}
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
		appendFileAsset(assets, fileAssetDataSource(name, baseName(source), ""))
		diagnostics.Add(diag.Warning, "cURL data file", location, "data flag "+flag+" references local file "+source+"; file bytes are not embedded in the sample and must be supplied before replay")
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

func encodeCurlDataValue(raw string, diagnostics *diag.Collector, location string) string {
	if strings.Contains(raw, "@") && !strings.Contains(raw, "=") {
		diagnostics.Add(diag.Warning, "cURL data-urlencode", location, "--data-urlencode file-reference syntax was approximated as an encoded literal; review the generated query/body value")
	}
	if name, value, ok := strings.Cut(raw, "="); ok && name != "" {
		return url.QueryEscape(name) + "=" + url.QueryEscape(value)
	}
	if strings.HasPrefix(raw, "=") {
		return url.QueryEscape(strings.TrimPrefix(raw, "="))
	}
	return url.QueryEscape(raw)
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

func parseCurlSecondsMs(value string) int {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return int(seconds * 1000)
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
	if !ok || name == "" {
		diagnostics.Add(diag.Warning, "cURL multipart form", location, "form part "+raw+" did not use name=value syntax and was represented with a placeholder name")
		name = "field"
		value = raw
	}
	part := curlFormPart{Name: name, Value: value}
	segments := strings.Split(value, ";")
	payload := segments[0]
	for _, segment := range segments[1:] {
		key, val, ok := strings.Cut(segment, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "type":
			part.ContentType = strings.Trim(strings.TrimSpace(val), `"`)
		case "filename":
			part.Filename = strings.Trim(strings.TrimSpace(val), `"`)
		}
	}
	if !forceLiteral && (strings.HasPrefix(payload, "@") || strings.HasPrefix(payload, "<")) {
		source := strings.TrimLeft(payload, "@<")
		if part.Filename == "" {
			part.Filename = baseName(source)
		}
		if part.Filename == "" {
			part.Filename = "upload.bin"
		}
		part.Value = "{{file." + sanitizeIdentifier(name) + "}}"
		part.File = true
		appendFileAsset(assets, fileAssetDataSource(name, part.Filename, part.ContentType))
		diagnostics.Add(diag.Warning, "cURL multipart file", location, "form part "+name+" references local file "+source+"; file bytes are not embedded in the sample and must be supplied before replay")
		return part
	}
	part.Value = payload
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
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func isCurlFlag(token string) bool {
	return strings.HasPrefix(token, "-") && token != "-"
}

func curlFlagLikelyTakesValue(token string) bool {
	if strings.Contains(token, "=") {
		return false
	}
	switch token {
	case "--connect-timeout", "--max-time", "--retry", "--retry-delay", "--proxy", "--cacert", "--cert", "--key", "--resolve", "--interface", "--limit-rate", "--request-target", "--output", "-o", "--write-out", "-w":
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
