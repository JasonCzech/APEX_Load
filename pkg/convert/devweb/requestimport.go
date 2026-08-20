package devweb

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	filepath "path"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// finalizeDevWebHTTPRequest is the source-adapter boundary: DevWeb's static
// JavaScript recognition has already produced an effective request, and only
// that request enters the shared request/security/finalization pipeline.
//
// The shared projector reviews authored credentials consistently while
// preserving their exact executable values. Source-review evidence is scrubbed
// separately; importing a script must not turn an already-available credential
// into a binding the operator has to reenter.
func finalizeDevWebHTTPRequest(
	action dsl.Action,
	index int,
	sourceFile string,
	registry *credentialpolicy.NameRegistry,
	provenVars map[string]struct{},
	trustedValues map[string]struct{},
) (dsl.Action, requestimport.Report, error) {
	source := credentialpolicy.SourceRef{
		Format:  "devweb",
		Path:    sourceFile,
		Pointer: fmt.Sprintf("actions/%d", index),
	}
	location := source.Path + "#" + source.Pointer

	var findings []importreview.Finding
	headers, headerDiagnostic := requestimport.HeaderListFromMapWithDiagnostic(action.Headers, location)
	if headerDiagnostic.Detail != "" {
		findings = append(findings, importreview.Finding{
			Code:       "source_order_unavailable",
			Severity:   headerDiagnostic.Severity,
			Element:    headerDiagnostic.Element,
			Detail:     headerDiagnostic.Detail,
			Location:   headerDiagnostic.Location,
			Source:     "devweb",
			ReasonCode: "source_order_unavailable",
			SourceRef:  location,
		})
	}
	for headerIndex := range headers {
		headers[headerIndex].Source = credentialpolicy.SourceRef{
			Format:  source.Format,
			Path:    source.Path,
			Pointer: fmt.Sprintf("%s/headers/%d", source.Pointer, headerIndex),
		}
	}

	effective := requestimport.EffectiveRequest{
		Key:     action.Name,
		Name:    action.Name,
		Method:  action.Method,
		Target:  action.Target,
		Headers: headers,
		Body: requestimport.Body{
			Kind:     devWebRequestBodyKind(action.Body, action.Headers),
			Payload:  []byte(action.Body),
			Encoding: requestimport.BodyEncodingText,
			Source: credentialpolicy.SourceRef{
				Format:  source.Format,
				Path:    source.Path,
				Pointer: source.Pointer + "/body",
			},
		},
		Metadata: action.Metadata,
		Source:   source,
	}

	finalized, report, err := requestimport.FinalizeAction(effective, requestimport.FinalizeOptions{
		Credentials: &requestimport.CredentialProjectionOptions{
			Mode:       credentialpolicy.Preserve,
			Registry:   registry,
			NamePrefix: "DEVWEB",
			// Variables a DevWeb extractor produces are response-derived, not
			// authored credentials. Without this a correlated "Basic {{var.token}}"
			// header would be governed into a secret binding the operator cannot
			// satisfy, because the credential never existed as a literal.
			ProvenVars: provenVars,
			// {{data.*}} / {{param.*}} references the converter emitted from DevWeb
			// parameter mappings are runtime data bindings, not authored secrets.
			TrustedWholeValues: trustedValues,
		},
		Overlay:  requestimport.ActionOverlay{Type: "request"},
		Findings: findings,
	})
	if err != nil {
		return dsl.Action{}, report, fmt.Errorf("finalize devweb request at %s: %w", source.Pointer, err)
	}
	// The shared finalizer owns the request surface (target, headers, auth,
	// body) and nothing else. Everything the DevWeb adapter derived from the
	// source script — extractors, textCheck assertions, think time — is
	// converter state and is carried over unchanged.
	finalized.Extractors = action.Extractors
	finalized.Assertions = action.Assertions
	finalized.ThinkTime = action.ThinkTime
	finalized.ThinkTimeAfter = action.ThinkTimeAfter
	finalized.Value = action.Value
	return finalized, report, nil
}

func devWebRequestBodyKind(body string, headers map[string]string) requestimport.BodyKind {
	if strings.TrimSpace(body) == "" {
		return requestimport.BodyNone
	}
	contentType := ""
	for name, value := range headers {
		if strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
			contentType = value
		}
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch strings.ToLower(mediaType) {
	case "application/json", "application/problem+json":
		return requestimport.BodyJSON
	case "application/xml", "text/xml", "application/soap+xml":
		return requestimport.BodyXML
	case "application/x-www-form-urlencoded":
		return requestimport.BodyFormURLEncoded
	case "application/graphql":
		return requestimport.BodyGraphQL
	}
	trimmed := strings.TrimSpace(body)
	if json.Valid([]byte(trimmed)) && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) {
		return requestimport.BodyJSON
	}
	if strings.HasPrefix(trimmed, "<") {
		return requestimport.BodyXML
	}
	return requestimport.BodyRaw
}

// publicDevWebCredentialRequirements strips source values from the requirements
// that ride in the public conversion payload.
func publicDevWebCredentialRequirements(requirements []credentialpolicy.Requirement) []credentialpolicy.Requirement {
	if len(requirements) == 0 {
		return nil
	}
	public := credentialpolicy.CloneRequirements(requirements)
	for index := range public {
		public[index].Value = ""
		public[index].DeferredSourceExpression = ""
	}
	return public
}

// devWebExtractorVariables returns every variable name a DevWeb extractor
// produces anywhere in the script. The converter only emits {{var.X}} where it
// recognized a "<request>.extractors.X" reference, so a reference and its
// producing extractor always exist together — including on the same request,
// which is how DevWeb scripts carry a token across iterations.
func devWebExtractorVariables(actions []dsl.Action) map[string]struct{} {
	proven := make(map[string]struct{})
	for _, action := range actions {
		for _, extractor := range action.Extractors {
			if name := strings.TrimSpace(extractor.Name); name != "" {
				proven[name] = struct{}{}
			}
		}
	}
	if len(proven) == 0 {
		return nil
	}
	return proven
}

// devWebHTTPRequestAction reports whether an action is an HTTP request the
// shared pipeline can finalize. Review-only markers and non-HTTP protocol steps
// are excluded: they carry no request surface to project.
func devWebHTTPRequestAction(action dsl.Action) bool {
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "request", "http_request":
		return true
	}
	return false
}

// devWebRuntimeDataReferences collects the {{data.*}} and {{param.*}} whole
// references the converter emitted while mapping DevWeb parameters. They are
// trusted by construction: the converter only produces them from a recognized
// load.params/parameter-file mapping, never from authored literal text.
func devWebRuntimeDataReferences(actions []dsl.Action) map[string]struct{} {
	trusted := make(map[string]struct{})
	collect := func(value string) {
		for _, match := range devWebRuntimeReferencePattern.FindAllString(value, -1) {
			trusted[match] = struct{}{}
		}
	}
	for _, action := range actions {
		collect(action.Target)
		collect(action.Body)
		for _, value := range action.Headers {
			collect(value)
		}
		for _, header := range action.HeaderList {
			collect(header.Value)
		}
	}
	if len(trusted) == 0 {
		return nil
	}
	return trusted
}

var devWebRuntimeReferencePattern = regexp.MustCompile(`\{\{\s*(?:data|param)\.[A-Za-z0-9_.-]+\s*\}\}`)

var devWebDataReferencePattern = regexp.MustCompile(
	`\{\{\s*data\.([A-Za-z0-9_-]+)\.([A-Za-z0-9_.-]+)\s*\}\}`,
)

// ProjectRequestBody parses a complete request body. Keep the aggregate work
// bounded when a generated or adversarial body repeats many data references;
// above this budget the review masks every referenced data column rather than
// probing each occurrence independently.
const maxDevWebCredentialBodyProjectionWork = 64 << 20

// devWebCredentialDataBindings identifies runtime CSV columns consumed by a
// credential-bearing request surface. The executable {{data.*}} references and
// inline rows remain untouched; the result is used only to mask SourceReview.
func devWebCredentialDataBindings(actions []dsl.Action) map[string]map[string]struct{} {
	bindings := make(map[string]map[string]struct{})
	record := func(value string) {
		for _, match := range devWebDataReferencePattern.FindAllStringSubmatch(value, -1) {
			if len(match) != 3 {
				continue
			}
			source := strings.ToLower(strings.TrimSpace(match[1]))
			column := strings.ToLower(strings.TrimSpace(match[2]))
			if source == "" || column == "" {
				continue
			}
			if bindings[source] == nil {
				bindings[source] = make(map[string]struct{})
			}
			bindings[source][column] = struct{}{}
		}
	}
	for _, action := range actions {
		collectDevWebCredentialURLDataReferences(action.Target, record)
		for name, value := range action.Headers {
			if credential.SensitiveHeader(name) || credential.SensitiveName(name) {
				record(value)
			}
		}
		for _, header := range action.HeaderList {
			if credential.SensitiveHeader(header.Name) || credential.SensitiveName(header.Name) {
				record(header.Value)
			}
		}
		if action.Auth != nil {
			record(action.Auth.Password)
			record(action.Auth.Token)
		}
		collectDevWebCredentialBodyDataReferences(
			action.Body,
			devWebActionContentType(action),
			record,
		)
	}
	if len(bindings) == 0 {
		return nil
	}
	return bindings
}

func markDevWebCredentialPreservingImport(script *dsl.Script) {
	if script == nil {
		return
	}
	preserved := ensureDevWebImportPreserved(script)
	preserved[dsl.ImportPreservedCredentialPolicyKey] =
		dsl.ImportPreservedCredentialPolicyPreserve
}

func markDevWebCredentialDataBindings(
	script *dsl.Script,
	bindings map[string]map[string]struct{},
) {
	if script == nil || len(bindings) == 0 {
		return
	}
	names := make(map[string]struct{})
	for _, parameter := range script.Parameters {
		source := strings.ToLower(strings.TrimSpace(parameter.DataSource))
		column := strings.ToLower(strings.TrimSpace(parameter.Column))
		if _, sensitive := bindings[source][column]; sensitive {
			if name := strings.TrimSpace(parameter.Name); name != "" {
				names[name] = struct{}{}
			}
		}
	}
	if len(names) == 0 {
		return
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		return strings.ToLower(ordered[left]) < strings.ToLower(ordered[right])
	})
	preserved := ensureDevWebImportPreserved(script)
	preserved[dsl.ImportPreservedCredentialParameterNamesKey] = ordered
}

func collectDevWebCredentialURLDataReferences(raw string, record func(string)) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	authorityStart := 0
	if scheme := strings.Index(raw, "://"); scheme >= 0 {
		authorityStart = scheme + 3
	} else if strings.HasPrefix(raw, "//") {
		authorityStart = 2
	}
	authorityEnd := len(raw)
	if end := strings.IndexAny(raw[authorityStart:], "/?#"); end >= 0 {
		authorityEnd = authorityStart + end
	}
	if at := strings.LastIndex(raw[authorityStart:authorityEnd], "@"); at >= 0 {
		record(raw[authorityStart : authorityStart+at])
	}
	if queryStart := strings.IndexByte(raw, '?'); queryStart >= 0 {
		queryEnd := len(raw)
		if fragment := strings.IndexByte(raw[queryStart+1:], '#'); fragment >= 0 {
			queryEnd = queryStart + 1 + fragment
		}
		for _, pair := range strings.Split(raw[queryStart+1:queryEnd], "&") {
			name, value, _ := strings.Cut(pair, "=")
			decodedName, err := url.QueryUnescape(name)
			if err == nil && credential.SensitiveName(decodedName) {
				record(value)
			}
		}
	}
}

func collectDevWebCredentialBodyDataReferences(
	body string,
	contentType string,
	record func(string),
) {
	collectDevWebCredentialBodyDataReferencesWithBudget(
		body,
		contentType,
		record,
		maxDevWebCredentialBodyProjectionWork,
	)
}

func collectDevWebCredentialBodyDataReferencesWithBudget(
	body string,
	contentType string,
	record func(string),
	maxProjectionWork int,
) {
	if strings.TrimSpace(body) == "" || record == nil {
		return
	}
	maxProbes := 0
	if maxProjectionWork > 0 {
		maxProbes = maxProjectionWork / max(1, len(body))
	}
	if maxProbes < 1 {
		// Fail closed: all references in a body that cannot be classified within
		// the work budget are treated as credential-bound for source review.
		record(body)
		return
	}
	occurrences := devWebDataReferencePattern.FindAllStringIndex(body, maxProbes+1)
	if len(occurrences) == 0 {
		return
	}
	if len(occurrences) > maxProbes {
		record(body)
		return
	}
	for index, occurrence := range occurrences {
		reference := body[occurrence[0]:occurrence[1]]
		probe := fmt.Sprintf("APEX_DEVWEB_CREDENTIAL_PROBE_%d_7f2c", index+1)
		// Classify one occurrence at a time. Replacing every equal reference at
		// once is unsafe when a data binding is used both in a password field and
		// in an ordinary field: the surviving ordinary probe would make the
		// credential occurrence appear nonsensitive.
		candidate := body[:occurrence[0]] + probe + body[occurrence[1]:]
		projected, result, err := credentialpolicy.ProjectRequestBody(
			[]byte(candidate),
			contentType,
			credentialpolicy.Options{
				Mode:        credentialpolicy.Redact,
				Source:      credentialpolicy.SourceRef{Format: "devweb_source_review"},
				RequestKey:  "devweb_source_review",
				TargetField: "body",
			},
		)
		if err != nil || result.Disposition == credentialpolicy.Rejected ||
			projected == nil || !bytes.Contains(projected, []byte(probe)) {
			record(reference)
		}
	}
}

func devWebActionContentType(action dsl.Action) string {
	for _, header := range action.HeaderList {
		if strings.EqualFold(strings.TrimSpace(header.Name), "Content-Type") {
			return header.Value
		}
	}
	for name, value := range action.Headers {
		if strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
			return value
		}
	}
	return ""
}

// redactDevWebCredentialDataFile masks only credential-bound CSV columns in a
// detached source-review copy. If the file cannot be parsed deterministically,
// the complete review file is replaced rather than risk disclosing a row.
func redactDevWebCredentialDataFile(
	name string,
	content string,
	bindings map[string]map[string]struct{},
) (string, bool) {
	if len(bindings) == 0 {
		return content, false
	}
	sourceName := strings.ToLower(sanitizeName(
		strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)),
	))
	columns := bindings[sourceName]
	if len(columns) == 0 {
		return content, false
	}
	reader := csv.NewReader(strings.NewReader(content))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil || len(records) == 0 {
		return credential.Redacted, content != credential.Redacted
	}
	var indexes []int
	for index, column := range records[0] {
		if _, sensitive := columns[strings.ToLower(strings.TrimSpace(column))]; sensitive {
			indexes = append(indexes, index)
		}
	}
	if len(indexes) == 0 {
		// A declared data binding without the expected header is ambiguous.
		return credential.Redacted, content != credential.Redacted
	}
	changed := false
	for row := 1; row < len(records); row++ {
		for _, column := range indexes {
			if column >= len(records[row]) {
				return credential.Redacted, content != credential.Redacted
			}
			if records[row][column] != credential.Redacted {
				records[row][column] = credential.Redacted
				changed = true
			}
		}
	}
	if !changed {
		return content, false
	}
	var output strings.Builder
	writer := csv.NewWriter(&output)
	writer.WriteAll(records)
	if err := writer.Error(); err != nil {
		return credential.Redacted, content != credential.Redacted
	}
	return output.String(), true
}

// redactDevWebCredentialValues removes discovered credential values from the
// source text that rides in the public source review.
//
// Preserve mode intentionally emits no binding requirements, so values are
// also collected from the finalized executable actions. Longest-first keeps a
// value containing another from leaving a suffix behind.
func redactDevWebCredentialValues(
	content string,
	requirements []credentialpolicy.Requirement,
	actions []dsl.Action,
) (string, bool) {
	return redactDevWebCredentialValuesWithAdditional(
		content,
		requirements,
		actions,
		nil,
	)
}

func redactDevWebCredentialValuesWithAdditional(
	content string,
	requirements []credentialpolicy.Requirement,
	actions []dsl.Action,
	additionalValues []string,
) (string, bool) {
	return redactDevWebCredentialValuesWithBudgetAndAdditional(
		content,
		requirements,
		actions,
		additionalValues,
		maxDevWebCredentialSourceRedactionWork,
	)
}

const maxDevWebCredentialSourceRedactionWork = 256 << 20

func redactDevWebCredentialValuesWithBudget(
	content string,
	requirements []credentialpolicy.Requirement,
	actions []dsl.Action,
	maxWork int64,
) (string, bool) {
	return redactDevWebCredentialValuesWithBudgetAndAdditional(
		content,
		requirements,
		actions,
		nil,
		maxWork,
	)
}

func redactDevWebCredentialValuesWithBudgetAndAdditional(
	content string,
	requirements []credentialpolicy.Requirement,
	actions []dsl.Action,
	additionalValues []string,
	maxWork int64,
) (string, bool) {
	values := make([]string, 0, len(requirements)+len(actions)*2+len(additionalValues))
	values = append(values, additionalValues...)
	for _, requirement := range requirements {
		if value := strings.TrimSpace(requirement.Value); value != "" {
			values = append(values, value)
		}
	}
	for _, action := range actions {
		if action.Target != "" && scrub.ValueURL(action.Target, scrub.Policy{}) != action.Target {
			// Preserve-mode projection deliberately keeps the executable URL
			// byte-exact. Source review is a different disclosure boundary:
			// remove the complete authored URL scalar when it contains userinfo
			// or a credential-shaped query value. RedactQuotedValues below also
			// finds the corresponding escaped JavaScript string literal.
			values = append(values, action.Target)
		}
		for name, value := range action.Headers {
			if credential.SensitiveHeader(name) && value != "" {
				values = append(values, value)
			}
		}
		if action.Body != "" && scrub.Value(action.Body, scrub.Policy{}) != action.Body {
			values = append(values, action.Body)
		}
	}
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	values = unique
	sort.SliceStable(values, func(i, j int) bool {
		if len(values[i]) == len(values[j]) {
			return values[i] < values[j]
		}
		return len(values[i]) > len(values[j])
	})
	if !devWebCredentialRedactionWithinBudget(len(content), len(values), maxWork) {
		return "// [DEVWEB SOURCE OMITTED: CREDENTIAL REDACTION WORK LIMIT EXCEEDED]" +
			strings.Repeat("\n", strings.Count(content, "\n")), true
	}
	changed := false
	for _, value := range values {
		next := strings.ReplaceAll(content, value, credential.Redacted)
		changed = changed || next != content
		content = next
	}
	content, quotedChanged := scrub.RedactQuotedValues(content, values)
	return content, changed || quotedChanged
}

func devWebCredentialRedactionWithinBudget(
	sourceBytes int,
	valueCount int,
	maxWork int64,
) bool {
	if valueCount <= 0 {
		return true
	}
	if maxWork < 0 {
		return false
	}
	return int64(sourceBytes) <= maxWork/int64(valueCount)
}
