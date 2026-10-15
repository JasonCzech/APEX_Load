package bruno

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"gopkg.in/yaml.v3"
	"html"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const nativeXMLRequirementKeyPrefix = "native-xml:"
const nativeXMLReviewMarker = "<?apex-import-review bruno-xml-unsupported-markup?>"

type nativeXMLCredentialSpan struct {
	start int
	end   int
	kind  string
	name  string
}

func sanitizeNativeXMLBodies(
	files []sourceFile,
) ([]sourceFile, []SecretRequirement, int, int, error) {
	out := make([]sourceFile, 0, len(files))
	requirements := make([]SecretRequirement, 0)
	reviewRedactions := 0
	for _, file := range files {
		ext := strings.ToLower(path.Ext(file.Path))
		if (ext != ".yml" && ext != ".yaml") || classifyFileRole(file.Path, file.Data) != "request" {
			out = append(out, file)
			continue
		}
		var document map[string]any
		if err := yaml.Unmarshal(file.Data, &document); err != nil {
			return nil, nil, 0, 0, fmt.Errorf("parse native XML request %q: %w", file.Path, err)
		}
		httpBlock := nativeMapValue(document, "http")
		bodyValue, bodyPointer, isXML := nativeXMLBodyValue(httpBlock)
		if !isXML || strings.TrimSpace(bodyValue) == "" {
			out = append(out, file)
			continue
		}
		rewritten, current, reviewRequired, err := rewriteNativeXMLCredentials(file.Path, bodyPointer, bodyValue)
		if err != nil {
			return nil, nil, 0, 0, fmt.Errorf("sanitize native XML body %q: %w", file.Path, err)
		}
		if reviewRequired {
			reviewRedactions++
		}
		if len(current) == 0 && rewritten == bodyValue {
			out = append(out, file)
			continue
		}
		if _, err := setNativeJSONPointerWithAliasPeers(
			document,
			file.nativeAliasGroups,
			bodyPointer,
			rewritten,
		); err != nil {
			return nil, nil, 0, 0, fmt.Errorf("write sanitized native XML body %q: %w", file.Path, err)
		}
		content, err := yaml.Marshal(document)
		if err != nil {
			return nil, nil, 0, 0, fmt.Errorf("serialize sanitized native XML body %q: %w", file.Path, err)
		}
		out = append(out, sourceFile{
			Path:              file.Path,
			Data:              content,
			Mode:              file.Mode,
			nativeAliasGroups: file.nativeAliasGroups,
		})
		requirements = append(requirements, current...)
	}
	sort.Slice(requirements, func(i, j int) bool {
		if requirements[i].Path == requirements[j].Path {
			return requirements[i].Key < requirements[j].Key
		}
		return requirements[i].Path < requirements[j].Path
	})
	return out, requirements, len(requirements), reviewRedactions, nil
}

func nativeXMLBodyValue(httpBlock map[string]any) (string, string, bool) {
	body, present := nativeLookupPresent(httpBlock, "body")
	if !present {
		return "", "", false
	}
	if bodyMap, ok := body.(map[string]any); ok {
		value := anyString(nativeLookup(bodyMap, "data"))
		bodyType := strings.ToLower(strings.TrimSpace(anyString(nativeLookup(bodyMap, "type"))))
		return value, "/http/body/data", strings.Contains(bodyType, "xml") || strings.HasPrefix(strings.TrimSpace(value), "<")
	}
	value := anyString(body)
	return value, "/http/body", strings.HasPrefix(strings.TrimSpace(value), "<")
}

func rewriteNativeXMLCredentials(
	sourcePath, bodyPointer, body string,
) (string, []SecretRequirement, bool, error) {
	decoder := xml.NewDecoder(strings.NewReader(body))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, false, fmt.Errorf("XML could not be safely parsed: %w", err)
		}
	}

	spans, err := scanNativeXMLCredentialSpans(body)
	if err != nil {
		return "", nil, false, err
	}

	trustedTemplates := map[string]struct{}{credential.Redacted: {}}
	var requirements []SecretRequirement
	var rewritten strings.Builder
	rewritten.Grow(len(body))
	cursor := 0
	occurrences := make(map[string]int)
	for _, span := range spans {
		if span.start < cursor || span.end < span.start || span.end > len(body) {
			return "", nil, false, errorsNativeXMLOverlappingSpans()
		}
		rewritten.WriteString(body[cursor:span.start])
		cursor = span.end
		identity := span.kind + "\x00" + strings.ToLower(span.name)
		occurrences[identity]++
		value := body[span.start:span.end]
		if nativeXMLCredentialValueContainsSentinel(value) {
			requirement := nativeXMLSecretRequirement(
				sourcePath,
				bodyPointer,
				span.kind,
				span.name,
				occurrences[identity],
				value,
			)
			rewritten.WriteString(nativeCredentialProjectionReplacement(requirement))
			requirements = append(requirements, requirement)
			continue
		}
		rewritten.WriteString(value)
		// Every credential-bearing scalar in an authored XML body is retained
		// exactly as written.
		//
		// Literals used to be rewritten to {{secret.NAME}} on import. Nothing an
		// imported script already contains is a project secret — the operator has
		// the source — and that rewrite made the stored bundle and the exported
		// script unrunnable until they bound a value they never chose to
		// externalize. Externalization stays available as an explicit operator
		// action; it is no longer the import default.
		//
		// Recording each scalar as a trusted whole value also keeps the shared
		// fail-closed pass below from swapping the surrounding body for a
		// placeholder: that fallback exists for credential material with no safe
		// scalar offset, not for values this scanner already located.
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			trustedTemplates[trimmed] = struct{}{}
		}
	}
	rewritten.WriteString(body[cursor:])
	projectedBody := rewritten.String()

	// The Bruno-specific scanner owns stable requirement identities for ordinary
	// XML scalars. Run the shared projector as a second, fail-closed pass for
	// credential-bearing CDATA, comments, processing instructions, and
	// directives that do not have a safe Bruno binding representation.
	projected, result, err := credentialpolicy.ProjectXML([]byte(projectedBody), credentialpolicy.Options{
		Mode:               credentialpolicy.Preserve,
		Source:             credentialpolicy.SourceRef{Format: "bruno", Path: sourcePath, Pointer: bodyPointer},
		RequestKey:         sourcePath,
		NamePrefix:         "BRUNO",
		TrustedWholeValues: trustedTemplates,
	})
	if err != nil {
		return "", nil, false, err
	}
	if result.Disposition == credentialpolicy.Rejected {
		return "", nil, false, fmt.Errorf("shared XML credential policy rejected Bruno source")
	}
	for _, requirement := range result.Requirements {
		if !requirement.ExistingReference {
			return "", nil, false, fmt.Errorf(
				"shared XML credential policy found a scalar not covered by Bruno identity projection",
			)
		}
	}
	for _, finding := range result.Findings {
		if finding.Code != "import_credential_preserved" {
			continue
		}
		// Credential-looking content inside CDATA/comments/directives has no
		// safe scalar offset. Govern the complete authored XML body as one
		// reversible occurrence: the sanitized bundle and inspection draft keep
		// only the placeholder, while the protected requirement restores the
		// exact original body during the default commit. Explicit
		// externalization may bind the same whole body without changing bytes.
		requirement := nativeXMLSecretRequirement(
			sourcePath,
			bodyPointer,
			"body",
			"payload",
			1,
			body,
		)
		return nativeCredentialProjectionReplacement(requirement), []SecretRequirement{requirement}, false, nil
	}
	reviewRequired := result.Disposition == credentialpolicy.BlockedDraft
	projectedBody = string(projected)
	if reviewRequired && !strings.Contains(projectedBody, nativeXMLReviewMarker) {
		projectedBody += nativeXMLReviewMarker
	}
	return projectedBody, requirements, reviewRequired, nil
}

// rewriteNativeXMLCredentialTemplates applies credential context to templates
// that occupy sensitive XML element or attribute values. The source sanitizer
// deliberately leaves exact templates intact so variable resolution can retain
// their real source value for governed migration; this projection converts the
// resolved value to a {{secret.*}} requirement before the general body template
// pass can turn it into an ordinary parameter.
func rewriteNativeXMLCredentialTemplates(
	body string,
	state *nativeTemplateState,
	bodyPointer string,
	sourceRequirements map[string][]SecretRequirement,
) (string, error) {
	spans, err := scanNativeXMLCredentialSpans(body)
	if err != nil {
		return "", err
	}
	if len(spans) == 0 {
		return body, nil
	}

	var rewritten strings.Builder
	rewritten.Grow(len(body))
	cursor := 0
	occurrences := make(map[string]int)
	for _, span := range spans {
		if span.start < cursor || span.end < span.start || span.end > len(body) {
			return "", errorsNativeXMLOverlappingSpans()
		}
		identity := span.kind + "\x00" + strings.ToLower(span.name)
		occurrences[identity]++
		rewritten.WriteString(body[cursor:span.start])
		rewritten.WriteString(rewriteNativeXMLCredentialValue(
			body[span.start:span.end],
			bodyPointer,
			span.kind,
			span.name,
			occurrences[identity],
			state,
			sourceRequirements,
		))
		cursor = span.end
	}
	rewritten.WriteString(body[cursor:])
	return rewritten.String(), nil
}

func rewriteNativeXMLCredentialValue(
	value, bodyPointer, kind, name string,
	occurrence int,
	state *nativeTemplateState,
	sourceRequirements map[string][]SecretRequirement,
) string {
	if secretReference, ok := exactNativeSecretReference(value); ok {
		return secretReference
	}
	if !brunoTemplatePattern.MatchString(value) {
		// A literal credential the operator authored into the XML body is not a
		// project secret. Rewriting it to {{secret.NAME}} on import produced a
		// stored bundle and an exported script that could not replay until the
		// operator bound a value they never asked to externalize. Preserve the
		// exact scalar; externalizing it stays an explicit operator action.
		return value
	}
	sourceVariable, hasExactSourceVariable := exactNativeCredentialSourceReference(value)
	beforeMissing := make(map[string]struct{}, len(state.missing))
	for missing := range state.missing {
		beforeMissing[missing] = struct{}{}
	}
	dependencies := make([]nativeVariable, 0)
	complete := true
	projectedWholeCredential := false
	matchIndexes := brunoTemplatePattern.FindAllStringIndex(value, -1)
	composedCredential := len(matchIndexes) != 1 ||
		matchIndexes[0][0] != 0 ||
		matchIndexes[0][1] != len(value)
	usageRequirement := nativeXMLSecretRequirement(
		state.sourcePath,
		bodyPointer,
		kind,
		name,
		occurrence,
		"",
	)
	_, usageRequirementAvailable := nativeSourceSecretValue(
		usageRequirement.Replacement,
		sourceRequirements,
	)
	resolved := brunoTemplatePattern.ReplaceAllStringFunc(value, func(template string) string {
		match := brunoTemplatePattern.FindStringSubmatch(template)
		if len(match) < 2 {
			complete = false
			return template
		}
		expression := strings.TrimSpace(match[1])
		lower := strings.ToLower(expression)
		for _, prefix := range []string{"env.", "data.", "var.", "param.", "secret.", "fn.", "fake.", "file."} {
			if strings.HasPrefix(lower, prefix) {
				complete = false
				return template
			}
		}
		if strings.Contains(expression, "(") || !nativeTemplateNamePattern.MatchString(expression) {
			complete = false
			return template
		}
		if _, exists := state.defined[expression]; !exists {
			complete = false
			return template
		}
		resolution := resolveNativeVariable(expression, state)
		dependencies = appendNativeVariableDependencies(dependencies, resolution.Dependencies...)
		if secretReference, ok := exactNativeSecretReference(resolution.Value); ok {
			if composedCredential && usageRequirementAvailable {
				projectedWholeCredential = true
				return resolution.Value
			}
			if !composedCredential {
				context := nativeTemplateContextLabel(nativeTemplateXMLCredential, name)
				projected := nativeVariableSecretRequirement(
					state.defined[expression],
					state.sourcePath,
					context,
				)
				if secretReference == projected.Replacement {
					projectedWholeCredential = true
					return resolution.Value
				}
			}
			if sourceValue, available := nativeSourceSecretValue(secretReference, sourceRequirements); available {
				return sourceValue
			}
		}
		if !resolution.Complete {
			complete = false
		}
		return resolution.Value
	})
	if brunoTemplatePattern.MatchString(resolved) {
		complete = false
	}
	if projectedWholeCredential && len(dependencies) > 0 {
		complete = true
	}

	// The entire sensitive XML value is replaced by one governed secret, so
	// unresolved variables inside it no longer participate in execution.
	for missing := range state.missing {
		if _, existed := beforeMissing[missing]; !existed {
			delete(state.missing, missing)
		}
	}

	context := nativeXMLCredentialContext(kind, name)
	var requirement SecretRequirement
	if composedCredential || len(dependencies) > 1 {
		requirement = usageRequirement
		requirement.Context = context
		requirement.scrubTargets = nativeVariableScrubTargets(dependencies)
		requirement.scrubTargets = append(
			requirement.scrubTargets,
			nativeSecretScrubTarget{
				Path:     state.sourcePath,
				Pointer:  bodyPointer,
				Kind:     "xml_credential_value",
				Selector: kind + "\x00" + strings.ToLower(name),
				Index:    occurrence,
			},
		)
	} else if len(dependencies) > 0 {
		requirement = nativeVariableSecretRequirement(dependencies[0], state.sourcePath, context)
		requirement.scrubTargets = nativeVariableScrubTargets(dependencies)
		requirement.scrubTargets = append(
			requirement.scrubTargets,
			nativeSecretScrubTarget{
				Path:     state.sourcePath,
				Pointer:  bodyPointer,
				Kind:     "xml_credential_value",
				Selector: kind + "\x00" + strings.ToLower(name),
				Index:    occurrence,
			},
		)
	} else {
		if hasExactSourceVariable {
			requirement = nativeUnresolvedSourceVariableSecretRequirement(
				sourceVariable,
				state.sourcePath,
				context,
			)
		} else {
			requirement = nativeSecretRequirement(
				name+"_"+strconv.Itoa(occurrence),
				state.sourcePath,
				context,
			)
		}
	}
	if hasExactSourceVariable {
		requirement.DisplayLabel = nativeSourceVariableDisplayLabel(sourceVariable)
		requirement.SourceVariable = sourceVariable
		requirement.AffectedPaths = nativeSecretRequirementPaths(
			append(requirement.AffectedPaths, state.sourcePath)...,
		)
	}
	if complete && !brunoTemplatePattern.MatchString(resolved) {
		requirement.Value = recoverableNativeXMLCredentialValue(resolved)
	}
	if hasExactSourceVariable {
		requirement.UnresolvedSourceVariable = requirement.Value == ""
	}
	registerNativeGeneratedRequirement(state, requirement)
	return nativeCredentialProjectionReplacement(requirement)
}

func nativeXMLCredentialContext(kind, name string) string {
	role := "credential"
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(nativeXMLLocalName(name)))
	switch {
	case strings.Contains(normalized, "username"):
		role = "username"
	case strings.Contains(normalized, "password") || strings.Contains(normalized, "passwd"):
		role = "password"
	case strings.Contains(normalized, "token"):
		role = "token"
	case strings.Contains(normalized, "apikey"):
		role = "API key"
	}
	if kind == "element" {
		return "XML " + role + " element <" + name + ">"
	}
	return "XML " + role + " " + kind + " " + name
}

func setNativeXMLCredentialValue(
	document any,
	bodyPointer, selector string,
	occurrence int,
	replacement string,
) (bool, error) {
	bodyValue, err := nativeJSONPointerValue(document, bodyPointer)
	if err != nil {
		return false, err
	}
	body, ok := bodyValue.(string)
	if !ok {
		return false, fmt.Errorf("XML body scrub target %q is not a string", bodyPointer)
	}
	spans, err := scanNativeXMLCredentialSpans(body)
	if err != nil {
		return false, err
	}
	currentOccurrence := 0
	for _, span := range spans {
		identity := span.kind + "\x00" + strings.ToLower(span.name)
		if identity != selector {
			continue
		}
		currentOccurrence++
		if currentOccurrence != occurrence {
			continue
		}
		if body[span.start:span.end] == replacement {
			return false, nil
		}
		rewritten := body[:span.start] + replacement + body[span.end:]
		return setNativeJSONPointer(document, bodyPointer, rewritten)
	}
	return false, fmt.Errorf(
		"XML credential scrub target %q occurrence %d was not found",
		selector,
		occurrence,
	)
}

func errorsNativeXMLOverlappingSpans() error {
	return fmt.Errorf("XML credential spans overlap")
}

func scanNativeXMLCredentialSpans(body string) ([]nativeXMLCredentialSpan, error) {
	type elementFrame struct {
		name              string
		sensitive         bool
		credentialContext bool
		contentStart      int
		hasChild          bool
		hasMarkup         bool
	}
	var stack []elementFrame
	var spans []nativeXMLCredentialSpan

	for offset := 0; offset < len(body); {
		start := strings.IndexByte(body[offset:], '<')
		if start < 0 {
			break
		}
		start += offset
		switch {
		case strings.HasPrefix(body[start:], "<!--"):
			end := strings.Index(body[start+4:], "-->")
			if end < 0 {
				return nil, fmt.Errorf("unterminated XML comment")
			}
			if len(stack) > 0 {
				stack[len(stack)-1].hasMarkup = true
			}
			offset = start + 4 + end + 3
			continue
		case strings.HasPrefix(body[start:], "<![CDATA["):
			end := strings.Index(body[start+9:], "]]>")
			if end < 0 {
				return nil, fmt.Errorf("unterminated XML CDATA")
			}
			if len(stack) > 0 {
				stack[len(stack)-1].hasMarkup = true
			}
			offset = start + 9 + end + 3
			continue
		case strings.HasPrefix(body[start:], "<?"):
			end := strings.Index(body[start+2:], "?>")
			if end < 0 {
				return nil, fmt.Errorf("unterminated XML processing instruction")
			}
			if len(stack) > 0 {
				stack[len(stack)-1].hasMarkup = true
			}
			offset = start + 2 + end + 2
			continue
		case strings.HasPrefix(body[start:], "<!"):
			end, err := nativeXMLTagEnd(body, start)
			if err != nil {
				return nil, err
			}
			if len(stack) > 0 {
				stack[len(stack)-1].hasMarkup = true
			}
			offset = end
			continue
		case strings.HasPrefix(body[start:], "</"):
			end, err := nativeXMLTagEnd(body, start)
			if err != nil {
				return nil, err
			}
			if len(stack) == 0 {
				return nil, fmt.Errorf("unexpected XML closing element")
			}
			frame := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if frame.sensitive && frame.hasChild && !nativeXMLCredentialContainerName(frame.name) {
				return nil, fmt.Errorf("sensitive XML element %q contains unsupported child markup", frame.name)
			}
			if frame.sensitive && !frame.hasChild {
				if frame.hasMarkup {
					return nil, fmt.Errorf("sensitive XML element %q contains unsupported markup", frame.name)
				}
				valueStart, valueEnd := trimNativeXMLWhitespace(body, frame.contentStart, start)
				if valueStart < valueEnd {
					spans = append(spans, nativeXMLCredentialSpan{
						start: valueStart,
						end:   valueEnd,
						kind:  "element",
						name:  frame.name,
					})
				}
			}
			offset = end
			continue
		default:
			end, err := nativeXMLTagEnd(body, start)
			if err != nil {
				return nil, err
			}
			parentCredentialContext := len(stack) > 0 &&
				stack[len(stack)-1].credentialContext
			name, selfClosing, attributes, err := nativeXMLStartTag(
				body,
				start,
				end,
				parentCredentialContext,
			)
			if err != nil {
				return nil, err
			}
			if len(stack) > 0 {
				stack[len(stack)-1].hasChild = true
			}
			spans = append(spans, attributes...)
			if !selfClosing {
				containerContext := parentCredentialContext ||
					nativeXMLCredentialContainerName(name)
				stack = append(stack, elementFrame{
					name:              name,
					sensitive:         parentCredentialContext || IsNativeXMLCredentialName(nativeXMLLocalName(name)),
					credentialContext: containerContext,
					contentStart:      end,
				})
			}
			offset = end
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start == spans[j].start {
			return spans[i].end < spans[j].end
		}
		return spans[i].start < spans[j].start
	})
	return spans, nil
}

func nativeXMLTagEnd(body string, start int) (int, error) {
	var quote byte
	bracketDepth := 0
	for index := start + 1; index < len(body); index++ {
		switch body[index] {
		case '\'', '"':
			if quote == 0 {
				quote = body[index]
			} else if quote == body[index] {
				quote = 0
			}
		case '[':
			if quote == 0 {
				bracketDepth++
			}
		case ']':
			if quote == 0 && bracketDepth > 0 {
				bracketDepth--
			}
		case '>':
			if quote == 0 && bracketDepth == 0 {
				return index + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("unterminated XML tag")
}

func nativeXMLStartTag(
	body string,
	start, end int,
	credentialContext bool,
) (string, bool, []nativeXMLCredentialSpan, error) {
	position := start + 1
	for position < end && isNativeXMLSpace(body[position]) {
		position++
	}
	nameStart := position
	for position < end && !isNativeXMLNameDelimiter(body[position]) {
		position++
	}
	if nameStart == position {
		return "", false, nil, fmt.Errorf("XML start element has no name")
	}
	elementName := body[nameStart:position]
	var spans []nativeXMLCredentialSpan
	for position < end {
		for position < end && isNativeXMLSpace(body[position]) {
			position++
		}
		if position >= end || body[position] == '>' {
			break
		}
		if body[position] == '/' {
			return elementName, true, spans, nil
		}
		nameStart = position
		for position < end && !isNativeXMLNameDelimiter(body[position]) {
			position++
		}
		if nameStart == position {
			return "", false, nil, fmt.Errorf("XML attribute has no name")
		}
		attributeName := body[nameStart:position]
		for position < end && isNativeXMLSpace(body[position]) {
			position++
		}
		if position >= end || body[position] != '=' {
			return "", false, nil, fmt.Errorf("XML attribute %q has no value", attributeName)
		}
		position++
		for position < end && isNativeXMLSpace(body[position]) {
			position++
		}
		if position >= end || (body[position] != '\'' && body[position] != '"') {
			return "", false, nil, fmt.Errorf("XML attribute %q is not quoted", attributeName)
		}
		quote := body[position]
		position++
		valueStart := position
		for position < end && body[position] != quote {
			position++
		}
		if position >= end {
			return "", false, nil, fmt.Errorf("XML attribute %q is unterminated", attributeName)
		}
		isNamespaceDeclaration := strings.EqualFold(attributeName, "xmlns") ||
			strings.HasPrefix(strings.ToLower(attributeName), "xmlns:")
		if !isNamespaceDeclaration &&
			(credentialContext ||
				nativeXMLCredentialContainerName(elementName) ||
				IsNativeXMLCredentialName(nativeXMLLocalName(attributeName))) &&
			valueStart < position {
			spans = append(spans, nativeXMLCredentialSpan{
				start: valueStart,
				end:   position,
				kind:  "attribute",
				name:  attributeName,
			})
		}
		position++
	}
	return elementName, false, spans, nil
}

// IsNativeXMLCredentialName reports whether an XML element or attribute name
// represents credential material in Bruno's native SOAP/HTTP projection. XML
// APIs commonly qualify the ordinary secret vocabulary (for example
// userPassword or partnerAuthToken), so this policy deliberately recognizes
// credential suffixes while leaving generic business names untouched.
func IsNativeXMLCredentialName(name string) bool {
	local := nativeXMLLocalName(name)
	if scrub.SensitiveName(local) {
		return true
	}
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(local))
	for _, suffix := range []string{
		"password", "passwd", "passphrase",
		"authtoken", "accesstoken", "refreshtoken", "sessiontoken",
		"apikey", "clientsecret", "privatekey", "accesskey",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func nativeXMLCredentialContainerName(name string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(nativeXMLLocalName(name)))
	switch normalized {
	case "auth", "authentication", "credentials", "credential", "security", "securityheader", "securitymessageheader":
		return true
	default:
		return false
	}
}

func nativeXMLSecretRequirement(
	sourcePath, bodyPointer, kind, name string,
	occurrence int,
	value string,
) SecretRequirement {
	localName := nativeXMLLocalName(name)
	identity := bodyPointer + "#" + kind + ":" + strings.ToLower(localName) + ":" + strconv.Itoa(occurrence)
	hashIdentity := bodyPointer + "\x00" + kind + "\x00" + strings.ToLower(localName) + "\x00" + strconv.Itoa(occurrence)
	nameHash := sha256.Sum256([]byte("bruno-native-xml-name\x00" + sourcePath + "\x00" + hashIdentity))
	suffix := "_" + strings.ToUpper(hex.EncodeToString(nameHash[:6]))
	suggested := secretName(sourcePath, "body_xml_"+localName)
	if len(suggested)+len(suffix) > credential.MaxLogicalNameBytes {
		suggested = strings.TrimRight(suggested[:credential.MaxLogicalNameBytes-len(suffix)], "_")
	}
	suggested += suffix
	idHash := sha256.Sum256([]byte("bruno-native-xml\x00" + sourcePath + "\x00" + hashIdentity))
	return SecretRequirement{
		ID:            hex.EncodeToString(idHash[:12]),
		Path:          sourcePath,
		Key:           nativeXMLRequirementKeyPrefix + identity,
		SuggestedName: suggested,
		Replacement:   "{{secret." + suggested + "}}",
		Context:       "Bruno XML body " + kind + " " + name,
		Value:         recoverableNativeXMLCredentialValue(value),
		AffectedPaths: nativeSecretRequirementPaths(sourcePath),
		readableName:  readableNativeSecretBase(localName),
	}
}

func recoverableNativeXMLCredentialValue(value string) string {
	if nativeXMLCredentialValueContainsSentinel(value) {
		return ""
	}
	return value
}

func nativeXMLCredentialValueContainsSentinel(value string) bool {
	decoded, stable := decodeBrunoCredentialValueToStable(value, func(current string) (string, error) {
		return html.UnescapeString(current), nil
	})
	return !stable || containsBrunoCredentialRedactionSentinel(decoded)
}

func nativeXMLLocalName(name string) string {
	if separator := strings.LastIndexByte(name, ':'); separator >= 0 {
		return name[separator+1:]
	}
	return name
}

func trimNativeXMLWhitespace(body string, start, end int) (int, int) {
	for start < end && isNativeXMLSpace(body[start]) {
		start++
	}
	for end > start && isNativeXMLSpace(body[end-1]) {
		end--
	}
	return start, end
}

func isNativeXMLSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func isNativeXMLNameDelimiter(value byte) bool {
	return isNativeXMLSpace(value) || value == '=' || value == '/' || value == '>'
}
