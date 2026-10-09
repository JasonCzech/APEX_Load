package requestimport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"golang.org/x/net/http/httpguts"
)

// ImportHeaderFidelityMetadataKey is the durable record of source header
// occurrences that could not be represented by the native HTTP transport.
// Version 1 of this envelope has:
//
//   - version: 1
//   - reason_codes: sorted []string
//   - source_refs: sorted []string
//   - omitted_occurrences: ordered []map with source_index, name, reason_code,
//     optional source_ref, and (for credential-preserving imports) base64
//     fields containing the exact source name/value bytes
//
// The exact bytes are inert: active HeaderList/Headers surfaces no longer
// contain the occurrence, the marker requires a durable execution gate, and
// API/read/review projections mask both byte fields.
const ImportHeaderFidelityMetadataKey = dsl.ImportHeaderFidelityMetadataKey

// ImportMultipartFidelityMetadataKey is the inert, execution-gated record of
// exact structured multipart fields that cannot be serialized onto the native
// HTTP wire. The active multipart body contains only valid replacement fields;
// exact source bytes exist solely in this metadata envelope.
const ImportMultipartFidelityMetadataKey = dsl.ImportMultipartFidelityMetadataKey

// ImportHTTPVersionFidelityMetadataKey marks a source HTTP version that cannot
// be selected by the native transport. The exact source spelling remains in
// ordinary source provenance; this envelope contains only the stable reason and
// source references and is protected by the same durable execution gate.
const ImportHTTPVersionFidelityMetadataKey = "import_http_version_fidelity"

const (
	importRuntimeFidelityVersion      = 1
	reasonUnsupportedHTTPVersion      = "unsupported_http_version"
	reasonHTTPVersionNotPreserved     = "http_version_not_preserved"
	importReviewRequiredBlocker       = "import_review_required"
	headerFidelityOccurrenceFieldName = "omitted_occurrences"
	multipartFidelityFieldName        = "omitted_fields"
)

// RuntimeFidelityOptions supplies provenance when an already-projected script
// is reviewed. EffectiveRequest projection uses its per-request/per-header
// sources directly.
type RuntimeFidelityOptions struct {
	SourceProfile string
	Source        SourceRef
	// PreserveOmittedHeaderBytes retains exact source name/value bytes in the
	// inert action marker. It is enabled by the shared finalizer only for an
	// import whose durable value policy is preserve.
	PreserveOmittedHeaderBytes bool
	// DeclaredHTTPVersion is a source-wide exact version declaration supplied by
	// adapters whose legacy projected actions do not carry per-request version
	// metadata. It is used only when a request surface has no declaration of its
	// own. Unlike per-request declarations, a source-wide declaration never
	// promotes an empty caller-selected runtime mode; a mismatch is review-gated.
	DeclaredHTTPVersion       string
	DeclaredHTTPVersionSource SourceRef
}

type runtimeHeaderOmission struct {
	SourceIndex int
	Name        string
	ReasonCode  string
	Detail      string
	Source      SourceRef
	// identity is retained only in memory so callers can reason about header
	// precedence without copying an arbitrary authored name into diagnostics or
	// durable metadata.
	identity string
	// sourceName/sourceValue remain private until they are encoded into an
	// execution-gated inert marker. Keeping Go strings here preserves their
	// exact bytes, including invalid UTF-8, until base64 encoding.
	sourceName      string
	sourceValue     string
	hasSourceHeader bool
}

type runtimeMultipartOmission struct {
	PartIndex      int
	HeaderIndex    int
	Field          string
	ReasonCode     string
	Source         SourceRef
	sourceName     string
	sourceValue    string
	hasSourceName  bool
	hasSourceValue bool
}

type runtimeFidelityProjection struct {
	HeaderOmissions            []runtimeHeaderOmission
	PreserveOmittedHeaderBytes bool
	MultipartOmissions         []runtimeMultipartOmission
	PreserveMultipartBytes     bool
	VersionBlocked             bool
	VersionReason              string
	VersionDetail              string
	VersionSource              SourceRef
}

// projectRuntimeFidelity converts runtime-incompatible header occurrences into
// a valid inert representation and returns the durable findings/evidence that
// keep the draft non-executable. Ordinary duplicate headers are untouched.
func projectRuntimeFidelity(
	request Request,
	headers HeaderList,
) (HeaderList, runtimeFidelityProjection, []importreview.Finding) {
	sanitized, projection, findings := projectHeaderRuntimeFidelity(request, headers)

	if strings.TrimSpace(request.HTTPVersion) != "" &&
		!sourceHTTPVersionExecutable(request.HTTPVersion) {
		projection.VersionBlocked = true
		projection.VersionReason = reasonUnsupportedHTTPVersion
		projection.VersionDetail = "the source HTTP version cannot be selected exactly by the native HTTP runtime"
		projection.VersionSource = request.Source
		findings = append(findings, requestReviewFinding(
			request,
			reasonUnsupportedHTTPVersion,
			"HTTP version",
			"the source HTTP version cannot be selected exactly by the native HTTP runtime",
		))
	}
	return sanitized, projection, findings
}

func projectHeaderRuntimeFidelity(
	request Request,
	headers HeaderList,
) (HeaderList, runtimeFidelityProjection, []importreview.Finding) {
	version := httpprotocol.NormalizeVersion(request.HTTPVersion)
	if version == "auto" {
		version = ""
	}
	sanitized, omissions := sanitizeHeaderOccurrences(headers, version)
	projection := runtimeFidelityProjection{HeaderOmissions: omissions}
	findings := make([]importreview.Finding, 0, len(omissions))

	for _, omission := range omissions {
		source := omission.Source
		if sourceLocation(source) == "" {
			source = request.Source
		}
		finding := requestReviewFinding(
			request,
			omission.ReasonCode,
			omission.Name,
			omission.Detail,
		)
		finding.Location = sourceLocation(source)
		finding.SourceRef = sourceLocation(source)
		findings = append(findings, finding)
	}
	return sanitized, projection, findings
}

func sanitizeHeaderOccurrences(headers HeaderList, version string) (HeaderList, []runtimeHeaderOmission) {
	if len(headers) == 0 {
		return nil, nil
	}
	type occurrence struct {
		header      Header
		sourceIndex int
	}
	active := make([]occurrence, len(headers))
	for index, header := range headers {
		active[index] = occurrence{header: header, sourceIndex: index}
	}

	omissions := make([]runtimeHeaderOmission, 0)
	seenOmission := make(map[string]struct{})
	for pass := 0; pass <= len(headers) && len(active) > 0; pass++ {
		wire := make(protocol.HeaderList, len(active))
		for index := range active {
			wire[index] = protocol.HeaderEntry{
				Name:    active[index].header.Name,
				Value:   active[index].header.Value,
				Enabled: active[index].header.Enabled,
			}
		}
		issues := structuralHeaderIssues(httpprotocol.HeaderListIssuesForVersionWithUnresolvedTemplates(wire, version), version)
		if len(issues) == 0 {
			break
		}

		remove := make(map[int]struct{}, len(issues))
		for _, issue := range issues {
			activeIndex := issue.Index
			if activeIndex < 0 || activeIndex >= len(active) {
				// The runtime contract reports source occurrence indices. Keep a
				// defensive name fallback so a future issue implementation still
				// produces a safe draft instead of retaining a rejected header.
				for index := range active {
					if strings.EqualFold(active[index].header.Name, issue.Name) {
						activeIndex = index
						break
					}
				}
			}
			if activeIndex < 0 || activeIndex >= len(active) {
				continue
			}
			remove[activeIndex] = struct{}{}
			item := active[activeIndex]
			reason := headerIssueReasonCode(issue.Code)
			identity := strconv.Itoa(item.sourceIndex) + "\x00" + reason
			if _, exists := seenOmission[identity]; exists {
				continue
			}
			seenOmission[identity] = struct{}{}
			omissions = append(omissions, runtimeHeaderOmission{
				SourceIndex:     item.sourceIndex,
				Name:            safeHeaderEvidenceName(item.header.Name),
				ReasonCode:      reason,
				Detail:          issue.Detail,
				Source:          item.header.Source,
				identity:        headerIdentity(item.header.Name),
				sourceName:      item.header.Name,
				sourceValue:     item.header.Value,
				hasSourceHeader: true,
			})
		}
		if len(remove) == 0 {
			// Fail closed if an issue cannot be associated with an occurrence.
			// Removing every remaining occurrence is disclosure-safe and keeps
			// the draft structurally valid.
			for index := range active {
				remove[index] = struct{}{}
				item := active[index]
				reason := "http_header_unmapped_runtime_issue"
				identity := strconv.Itoa(item.sourceIndex) + "\x00" + reason
				if _, exists := seenOmission[identity]; exists {
					continue
				}
				seenOmission[identity] = struct{}{}
				omissions = append(omissions, runtimeHeaderOmission{
					SourceIndex:     item.sourceIndex,
					Name:            safeHeaderEvidenceName(item.header.Name),
					ReasonCode:      reason,
					Detail:          "the source header could not be represented by the native HTTP runtime",
					Source:          item.header.Source,
					identity:        headerIdentity(item.header.Name),
					sourceName:      item.header.Name,
					sourceValue:     item.header.Value,
					hasSourceHeader: true,
				})
			}
		}
		next := make([]occurrence, 0, len(active)-len(remove))
		for index, item := range active {
			if _, omitted := remove[index]; !omitted {
				next = append(next, item)
			}
		}
		active = next
	}

	sanitized := make(HeaderList, len(active))
	for index := range active {
		sanitized[index] = active[index].header
	}
	sort.SliceStable(omissions, func(i, j int) bool {
		if omissions[i].SourceIndex != omissions[j].SourceIndex {
			return omissions[i].SourceIndex < omissions[j].SourceIndex
		}
		if omissions[i].ReasonCode != omissions[j].ReasonCode {
			return omissions[i].ReasonCode < omissions[j].ReasonCode
		}
		return omissions[i].Name < omissions[j].Name
	})
	return sanitized, omissions
}

func headerIssueReasonCode(code httpprotocol.HeaderListIssueCode) string {
	return "http_header_" + stableCode(string(code))
}

func safeHeaderEvidenceName(name string) string {
	if !httpguts.ValidHeaderFieldName(name) {
		return "<invalid-header>"
	}
	// Only runtime-defined special names are known to be non-sensitive. An
	// otherwise valid authored name can itself carry a canary/credential, so do
	// not copy arbitrary names into findings or durable evidence.
	switch strings.ToLower(name) {
	case "host":
		return "Host"
	case "user-agent":
		return "User-Agent"
	case "content-length":
		return "Content-Length"
	case "transfer-encoding":
		return "Transfer-Encoding"
	case "trailer":
		return "Trailer"
	case "connection":
		return "Connection"
	case "keep-alive":
		return "Keep-Alive"
	case "proxy-connection":
		return "Proxy-Connection"
	case "te":
		return "TE"
	case "upgrade":
		return "Upgrade"
	default:
		return "<header>"
	}
}

func applyRuntimeFidelityMetadata(action *dsl.Action, projection runtimeFidelityProjection) {
	if action == nil || (len(projection.HeaderOmissions) == 0 &&
		len(projection.MultipartOmissions) == 0 &&
		!projection.VersionBlocked) {
		return
	}
	if action.Metadata == nil {
		action.Metadata = make(map[string]any)
	}
	if len(projection.HeaderOmissions) > 0 {
		reasons := make([]string, 0, len(projection.HeaderOmissions))
		refs := make([]string, 0, len(projection.HeaderOmissions))
		occurrences := make([]map[string]any, 0, len(projection.HeaderOmissions))
		if existing, ok := action.Metadata[ImportHeaderFidelityMetadataKey].(map[string]any); ok {
			reasons = append(reasons, metadataStringValues(existing["reason_codes"])...)
			refs = append(refs, metadataStringValues(existing["source_refs"])...)
			occurrences = append(occurrences, metadataObjectValues(existing[headerFidelityOccurrenceFieldName])...)
		}
		seenOccurrences := make(map[string]bool, len(occurrences))
		for _, entry := range occurrences {
			if encoded, err := json.Marshal(entry); err == nil {
				seenOccurrences[string(encoded)] = true
			}
		}
		for _, omission := range projection.HeaderOmissions {
			ref := sourceLocation(omission.Source)
			entry := map[string]any{
				"source_index": omission.SourceIndex,
				"name":         omission.Name,
				"reason_code":  omission.ReasonCode,
			}
			if projection.PreserveOmittedHeaderBytes && omission.hasSourceHeader {
				entry[dsl.ImportHeaderFidelityPreservedNameBase64Key] =
					base64.StdEncoding.EncodeToString([]byte(omission.sourceName))
				entry[dsl.ImportHeaderFidelityPreservedValueBase64Key] =
					base64.StdEncoding.EncodeToString([]byte(omission.sourceValue))
			}
			if ref != "" {
				entry["source_ref"] = ref
				refs = append(refs, ref)
			}
			reasons = append(reasons, omission.ReasonCode)
			if encoded, err := json.Marshal(entry); err == nil && !seenOccurrences[string(encoded)] {
				seenOccurrences[string(encoded)] = true
				occurrences = append(occurrences, entry)
			}
		}
		action.Metadata[ImportHeaderFidelityMetadataKey] = map[string]any{
			"version":             importRuntimeFidelityVersion,
			"reason_codes":        uniqueSorted(reasons),
			"source_refs":         uniqueSorted(refs),
			"omitted_occurrences": occurrences,
		}
	}
	if len(projection.MultipartOmissions) > 0 {
		reasons := make([]string, 0, len(projection.MultipartOmissions))
		refs := make([]string, 0, len(projection.MultipartOmissions))
		fields := make([]map[string]any, 0, len(projection.MultipartOmissions))
		if existing, ok := action.Metadata[ImportMultipartFidelityMetadataKey].(map[string]any); ok {
			reasons = append(reasons, metadataStringValues(existing["reason_codes"])...)
			refs = append(refs, metadataStringValues(existing["source_refs"])...)
			fields = append(fields, metadataObjectValues(existing[multipartFidelityFieldName])...)
		}
		for _, omission := range projection.MultipartOmissions {
			ref := sourceLocation(omission.Source)
			entry := map[string]any{
				"part_index":  omission.PartIndex,
				"field":       omission.Field,
				"reason_code": omission.ReasonCode,
			}
			if omission.HeaderIndex >= 0 {
				entry["header_index"] = omission.HeaderIndex
			}
			if projection.PreserveMultipartBytes {
				if omission.hasSourceName {
					entry[dsl.ImportHeaderFidelityPreservedNameBase64Key] =
						base64.StdEncoding.EncodeToString([]byte(omission.sourceName))
				}
				if omission.hasSourceValue {
					entry[dsl.ImportHeaderFidelityPreservedValueBase64Key] =
						base64.StdEncoding.EncodeToString([]byte(omission.sourceValue))
				}
			}
			if ref != "" {
				entry["source_ref"] = ref
				refs = append(refs, ref)
			}
			reasons = append(reasons, omission.ReasonCode)
			fields = append(fields, entry)
		}
		action.Metadata[ImportMultipartFidelityMetadataKey] = map[string]any{
			"version":        importRuntimeFidelityVersion,
			"reason_codes":   uniqueSorted(reasons),
			"source_refs":    uniqueSorted(refs),
			"omitted_fields": fields,
		}
	}
	if projection.VersionBlocked {
		reason := firstNonEmpty(projection.VersionReason, reasonUnsupportedHTTPVersion)
		refs := uniqueSorted([]string{sourceLocation(projection.VersionSource)})
		action.Metadata[ImportHTTPVersionFidelityMetadataKey] = map[string]any{
			"version":     importRuntimeFidelityVersion,
			"reason_code": reason,
			"source_refs": refs,
		}
	}
}

func metadataStringValues(raw any) []string {
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func metadataObjectValues(raw any) []map[string]any {
	switch values := raw.(type) {
	case []map[string]any:
		out := make([]map[string]any, 0, len(values))
		for _, value := range values {
			out = append(out, cloneMetadataMap(value))
		}
		return out
	case []any:
		out := make([]map[string]any, 0, len(values))
		for _, rawValue := range values {
			if value, ok := rawValue.(map[string]any); ok {
				out = append(out, cloneMetadataMap(value))
			}
		}
		return out
	default:
		return nil
	}
}

func existingHeaderFidelityOccurrenceCount(metadata map[string]any) int {
	if metadata == nil {
		return 0
	}
	marker, ok := metadata[ImportHeaderFidelityMetadataKey].(map[string]any)
	if !ok {
		return 0
	}
	return len(metadataObjectValues(marker[headerFidelityOccurrenceFieldName]))
}

func cloneMetadataMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func runtimeFidelitySourceMappings(
	projection runtimeFidelityProjection,
	actionPath string,
	headerOccurrenceBase int,
) []SourceMapping {
	base := strings.TrimSpace(actionPath)
	if base == "" {
		base = "request"
	}
	mappings := make([]SourceMapping, 0, len(projection.HeaderOmissions)+len(projection.MultipartOmissions)+1)
	for index, omission := range projection.HeaderOmissions {
		mappings = append(mappings, SourceMapping{
			Source: omission.Source,
			Target: fmt.Sprintf(
				"%s.metadata.%s.%s[%d]",
				base,
				ImportHeaderFidelityMetadataKey,
				headerFidelityOccurrenceFieldName,
				headerOccurrenceBase+index,
			),
		})
	}
	for index, omission := range projection.MultipartOmissions {
		mappings = append(mappings, SourceMapping{
			Source: omission.Source,
			Target: fmt.Sprintf(
				"%s.metadata.%s.%s[%d]",
				base,
				ImportMultipartFidelityMetadataKey,
				multipartFidelityFieldName,
				index,
			),
		})
	}
	if projection.VersionBlocked {
		mappings = append(mappings, SourceMapping{
			Source: projection.VersionSource,
			Target: base + ".metadata." + ImportHTTPVersionFidelityMetadataKey,
		})
	}
	return mappings
}

// ApplyRuntimeFidelityReview sanitizes and review-gates an already-projected
// HTTP-family script. It is used by importers such as enterprise archives whose
// source adapter predates EffectiveRequest, and by FinalizeScript as a final
// defense after adapter overlays and script-wide HTTP settings are known.
func ApplyRuntimeFidelityReview(
	script *dsl.Script,
	options RuntimeFidelityOptions,
) ([]importreview.Finding, []SourceMapping, error) {
	if script == nil {
		return nil, nil, nil
	}
	if !isHTTPFamilyProtocol(script.Protocol) {
		return nil, nil, nil
	}

	sourceProfile := firstNonEmpty(
		options.SourceProfile,
		options.Source.Format,
		script.GeneratedFrom,
		"import",
	)
	configuredVersionPresent := script.Config.HTTP.Version != ""
	configuredVersion := httpprotocol.NormalizeVersion(script.Config.HTTP.Version)
	configVersionBlocked := strings.TrimSpace(script.Config.HTTP.Version) != "" &&
		!httpprotocol.VersionExecutable(configuredVersion)
	if configVersionBlocked {
		// Empty means runtime negotiation. It is a valid inert projection of an
		// unsupported source mode and, unlike "1.1", does not falsely claim
		// exact HTTP/1.1 semantics.
		script.Config.HTTP.Version = ""
		configuredVersion = ""
	} else {
		// Keep the runtime-facing spelling canonical even when a source used
		// HTTP/1.1 or HTTP/2.0 aliases.
		// A nonempty whitespace-only legacy config selects Auto before an
		// exact protocol alias. Keep that presence across storage/reopen.
		if configuredVersion != "" || !configuredVersionPresent {
			script.Config.HTTP.Version = configuredVersion
		}
	}

	type surfaceVersionDeclaration struct {
		raw        string
		normalized string
		source     SourceRef
		fromOption bool
	}
	declarations := make(map[string]surfaceVersionDeclaration)
	surfaceCount := 0
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if !runtimeSurfaceIsHTTPRequest(ref) {
			return nil
		}
		surfaceCount++
		raw := runtimeSurfaceDeclaredHTTPVersion(ref)
		declarationSource := runtimeSurfaceSource(ref, options.Source, sourceProfile)
		fromOption := false
		if strings.TrimSpace(raw) == "" && strings.TrimSpace(options.DeclaredHTTPVersion) != "" {
			raw = options.DeclaredHTTPVersion
			fromOption = true
			declarationSource = options.DeclaredHTTPVersionSource
			if sourceLocation(declarationSource) == "" {
				declarationSource = options.Source
			}
		}
		declarations[ref.Path] = surfaceVersionDeclaration{
			raw:        raw,
			normalized: sourceHTTPVersionNormalized(raw),
			source:     declarationSource,
			fromOption: fromOption,
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// An exact source version can be promoted to the script-wide runtime mode
	// only when every request-shaped surface declares the same supported
	// explicit version. Otherwise promotion could silently change an
	// unspecified sibling request.
	if configuredVersion == "" && !configuredVersionPresent && dsl.InheritedHTTPVersion(script) == "" && !configVersionBlocked && surfaceCount > 0 {
		var common string
		canPromote := true
		for _, declaration := range declarations {
			if declaration.fromOption ||
				strings.TrimSpace(declaration.raw) == "" ||
				declaration.normalized == "" ||
				!sourceHTTPVersionExecutable(declaration.normalized) || declaration.normalized == "auto" {
				canPromote = false
				break
			}
			if common == "" {
				common = declaration.normalized
				continue
			}
			if declaration.normalized != common {
				canPromote = false
				break
			}
		}
		if canPromote && common != "" {
			configuredVersion = common
			script.Config.HTTP.Version = common
		}
	}

	defaultSource := options.Source
	if defaultSource.Format == "" {
		defaultSource.Format = sourceProfile
	}
	if defaultSource.Pointer == "" {
		defaultSource.Pointer = "config.http.default_headers"
	}
	defaultHeaderCount, defaultOmissions := sanitizeScriptDefaultHeaders(
		&script.Config.HTTP,
		"structural",
		defaultSource,
	)

	var findings []importreview.Finding
	var mappings []SourceMapping
	visitedRequests := 0
	err = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if !runtimeSurfaceIsHTTPRequest(ref) {
			return nil
		}
		visitedRequests++
		source := runtimeSurfaceSource(ref, options.Source, sourceProfile)
		if rawCapture, present := ref.Metadata()["response_content_capture"]; present {
			captureAction := ref.Action
			if captureAction == nil {
				captureAction = &dsl.Action{Metadata: ref.Metadata()}
			}
			ApplyResponseContentCapture(captureAction, ReviewResponseContentMetadata(rawCapture), source)
			if ref.Action == nil {
				ref.SetMetadata(captureAction.Metadata)
			}
		}
		declaration := declarations[ref.Path]
		// Legacy per-request source provenance is promoted to a leaf only when
		// the adapter did not already choose one. Source-wide LR declarations
		// remain inherited and never override a caller-selected script mode.
		if !declaration.fromOption && declaration.raw != "" {
			if policy, supported := sourceHTTPVersionPolicy(declaration.raw); supported {
				if ref.Action != nil && !ref.Action.HTTPVersion.IsPresent() {
					ref.Action.HTTPVersion, _ = dsl.NewHTTPVersionOverride(policy)
				} else if ref.Request != nil {
					if _, present := ref.Request["http_version"]; !present {
						ref.Request["http_version"] = policy
					}
				}
			}
		}
		effectiveVersion, versionErr := runtimeSurfaceEffectiveHTTPVersion(ref, dsl.InheritedHTTPVersion(script))
		projection := runtimeFidelityProjection{
			HeaderOmissions:            headerOmissionsWithFallbackSource(defaultOmissions, source),
			PreserveOmittedHeaderBytes: options.PreserveOmittedHeaderBytes,
		}
		projection.HeaderOmissions = append(projection.HeaderOmissions, inheritedDefaultHeaderIssues(script.Config.HTTP, ref, effectiveVersion, defaultSource)...)

		if ref.Action != nil {
			omissions := sanitizeDSLActionHeaders(
				ref.Action,
				effectiveVersion,
				source,
				defaultHeaderCount,
			)
			projection.HeaderOmissions = append(projection.HeaderOmissions, omissions...)
		} else if ref.Request != nil {
			omissions := sanitizeMapRequestHeaders(
				ref.Request,
				effectiveVersion,
				source,
				defaultHeaderCount,
			)
			projection.HeaderOmissions = append(projection.HeaderOmissions, omissions...)
		}

		switch {
		case configVersionBlocked:
			projection.VersionBlocked = true
			projection.VersionReason = reasonUnsupportedHTTPVersion
			projection.VersionDetail = "the source script HTTP version cannot be selected exactly by the native HTTP runtime"
			projection.VersionSource = defaultSource
		case strings.TrimSpace(declaration.raw) != "" &&
			!sourceHTTPVersionExecutable(declaration.normalized):
			projection.VersionBlocked = true
			projection.VersionReason = reasonUnsupportedHTTPVersion
			projection.VersionDetail = "the source HTTP version cannot be selected exactly by the native HTTP runtime"
			projection.VersionSource = declaration.source
		case versionErr != nil:
			projection.VersionBlocked = true
			projection.VersionReason = "malformed_http_version"
			projection.VersionSource = source
			projection.VersionDetail = "the imported leaf HTTP version has an invalid scalar shape or policy"
			if ref.Request != nil {
				delete(ref.Request, "http_version")
			} else if ref.Action != nil {
				ref.Action.HTTPVersion = dsl.OptionalHTTPVersion{}
			}
		case declaration.normalized != "" &&
			declaration.normalized != effectiveVersion:
			projection.VersionBlocked = true
			projection.VersionReason = reasonHTTPVersionNotPreserved
			projection.VersionDetail = "the source request's exact HTTP version is not guaranteed by its effective runtime policy"
			projection.VersionSource = declaration.source
		}
		if len(projection.HeaderOmissions) == 0 && !projection.VersionBlocked {
			return nil
		}

		action := ref.Action
		if action == nil {
			action = &dsl.Action{Name: ref.Name(), Metadata: ref.Metadata()}
		}
		applyRuntimeFidelityMetadata(action, projection)
		for _, omission := range projection.HeaderOmissions {
			finding := importreview.Finding{
				Code:       importReviewRequiredBlocker,
				Severity:   diag.Blocker,
				Element:    omission.Name,
				Detail:     omission.Detail,
				Location:   sourceLocation(omission.Source),
				ActionPath: ref.Path,
				Source:     sourceProfile,
				ReasonCode: omission.ReasonCode,
				SourceRef:  sourceLocation(omission.Source),
				Resolution: "replace_behavior_and_remove_gate",
				Gate:       importreview.GateExecution,
			}
			findings = append(findings, finding)
		}
		if projection.VersionBlocked {
			reason := firstNonEmpty(projection.VersionReason, reasonUnsupportedHTTPVersion)
			detail := firstNonEmpty(
				projection.VersionDetail,
				"the source HTTP version cannot be selected exactly by the native HTTP runtime",
			)
			findings = append(findings, importreview.Finding{
				Code:       importReviewRequiredBlocker,
				Severity:   diag.Blocker,
				Element:    "HTTP version",
				Detail:     detail,
				Location:   sourceLocation(projection.VersionSource),
				ActionPath: ref.Path,
				Source:     sourceProfile,
				ReasonCode: reason,
				SourceRef:  sourceLocation(projection.VersionSource),
				Resolution: "replace_behavior_and_remove_gate",
				Gate:       importreview.GateExecution,
			})
		}
		importreview.ApplyToAction(action, findingsForActionPath(findings, ref.Path))
		if ref.Action == nil {
			ref.SetMetadata(action.Metadata)
		}
		leafMappings := runtimeFidelitySourceMappings(projection, ref.Path, 0)
		// Repeated review can retain the same inherited omission. Map that
		// evidence to its existing coordinate rather than beyond the array.
		marker, _ := action.Metadata[ImportHeaderFidelityMetadataKey].(map[string]any)
		occurrences := metadataObjectValues(marker[headerFidelityOccurrenceFieldName])
		for index, omission := range projection.HeaderOmissions {
			for retainedIndex, retained := range occurrences {
				retainedRef, _ := retained["source_ref"].(string)
				if retained["name"] == omission.Name && retained["reason_code"] == omission.ReasonCode && retainedRef == sourceLocation(omission.Source) && fmt.Sprint(retained["source_index"]) == fmt.Sprint(omission.SourceIndex) {
					leafMappings[index].Target = fmt.Sprintf("%s.metadata.%s.%s[%d]", ref.Path, ImportHeaderFidelityMetadataKey, headerFidelityOccurrenceFieldName, retainedIndex)
					break
				}
			}
		}
		mappings = append(mappings, leafMappings...)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if visitedRequests == 0 && (len(defaultOmissions) > 0 || configVersionBlocked) {
		path := fmt.Sprintf("actions[%d]", len(script.Actions))
		projection := runtimeFidelityProjection{
			HeaderOmissions:            headerOmissionsWithFallbackSource(defaultOmissions, defaultSource),
			PreserveOmittedHeaderBytes: options.PreserveOmittedHeaderBytes,
		}
		if configVersionBlocked {
			projection.VersionBlocked = true
			projection.VersionReason = reasonUnsupportedHTTPVersion
			projection.VersionDetail = "the source script HTTP version cannot be selected exactly by the native HTTP runtime"
			projection.VersionSource = defaultSource
		}
		placeholder := dsl.Action{
			Name: "Review imported HTTP runtime settings",
			Type: "review_required",
			Params: map[string]any{
				"reason": "Imported HTTP runtime settings require review before execution.",
			},
		}
		applyRuntimeFidelityMetadata(&placeholder, projection)
		for _, omission := range projection.HeaderOmissions {
			findings = append(findings, importreview.Finding{
				Code:       importReviewRequiredBlocker,
				Severity:   diag.Blocker,
				Element:    omission.Name,
				Detail:     omission.Detail,
				Location:   sourceLocation(omission.Source),
				ActionPath: path,
				Source:     sourceProfile,
				ReasonCode: omission.ReasonCode,
				SourceRef:  sourceLocation(omission.Source),
				Resolution: "replace_behavior_and_remove_gate",
				Gate:       importreview.GateExecution,
			})
		}
		if projection.VersionBlocked {
			findings = append(findings, importreview.Finding{
				Code:       importReviewRequiredBlocker,
				Severity:   diag.Blocker,
				Element:    "HTTP version",
				Detail:     projection.VersionDetail,
				Location:   sourceLocation(projection.VersionSource),
				ActionPath: path,
				Source:     sourceProfile,
				ReasonCode: projection.VersionReason,
				SourceRef:  sourceLocation(projection.VersionSource),
				Resolution: "replace_behavior_and_remove_gate",
				Gate:       importreview.GateExecution,
			})
		}
		importreview.ApplyToAction(&placeholder, findingsForActionPath(findings, path))
		script.Actions = append(script.Actions, placeholder)
		mappings = append(mappings, runtimeFidelitySourceMappings(projection, path, 0)...)
	}
	return importreview.NormalizeFindings(findings), dedupeSourceMappings(mappings), nil
}

func sanitizeScriptDefaultHeaders(
	config *dsl.HTTPConfig,
	version string,
	source SourceRef,
) (int, []runtimeHeaderOmission) {
	if config == nil {
		return 0, nil
	}
	userAgentShadowed := hasHeaderName(config.DefaultHeaders, "User-Agent")
	acceptEncodingShadowed := hasHeaderName(config.DefaultHeaders, "Accept-Encoding")
	acceptLanguageShadowed := hasHeaderName(config.DefaultHeaders, "Accept-Language")
	effective := make(map[string]string, len(config.DefaultHeaders)+3)
	if config.UserAgent != "" && !userAgentShadowed {
		effective["User-Agent"] = config.UserAgent
	}
	if config.AcceptEncoding != "" && !acceptEncodingShadowed {
		effective["Accept-Encoding"] = config.AcceptEncoding
	}
	if config.AcceptLanguage != "" && !acceptLanguageShadowed {
		effective["Accept-Language"] = config.AcceptLanguage
	}
	// Match protocol/http.buildDefaultHeaders: an authored case-insensitive
	// name overrides its scalar convenience field. Collisions within the
	// authored map still have no deterministic winner.
	for name, value := range config.DefaultHeaders {
		effective[name] = value
	}
	count := len(effective) + len(config.DefaultHeaderList)
	list, sanitized, omissions := sanitizeHeaderSurfaces(config.DefaultHeaderList, effective, version, source, 0)
	config.DefaultHeaderList = list

	for name := range config.DefaultHeaders {
		if _, retained := sanitized[name]; !retained {
			delete(config.DefaultHeaders, name)
		}
	}
	if len(config.DefaultHeaders) == 0 {
		config.DefaultHeaders = nil
	}
	if userAgentShadowed {
		// An authored DefaultHeaders entry is the runtime winner. Drop
		// the semantically dead convenience scalar even when the winning map
		// value is valid, so stale invalid or sensitive source bytes cannot
		// survive in the stored draft behind that override.
		config.UserAgent = ""
	} else if _, retained := sanitized["User-Agent"]; !retained {
		config.UserAgent = ""
	}
	if acceptEncodingShadowed {
		config.AcceptEncoding = ""
	} else if _, retained := sanitized["Accept-Encoding"]; !retained {
		config.AcceptEncoding = ""
	}
	if acceptLanguageShadowed {
		config.AcceptLanguage = ""
	} else if _, retained := sanitized["Accept-Language"]; !retained {
		config.AcceptLanguage = ""
	}
	return count, omissions
}

func hasHeaderName(headers map[string]string, name string) bool {
	for existing := range headers {
		if strings.EqualFold(strings.TrimSpace(existing), name) {
			return true
		}
	}
	return false
}

func sanitizeDSLActionHeaders(
	action *dsl.Action,
	version string,
	source SourceRef,
	sourceIndexOffset int,
) []runtimeHeaderOmission {
	if action == nil {
		return nil
	}
	headerList, headerMap, omissions := sanitizeHeaderSurfaces(
		action.HeaderList,
		action.Headers,
		version,
		source,
		sourceIndexOffset,
	)
	action.HeaderList = headerList
	action.Headers = headerMap
	return omissions
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func sanitizeMapRequestHeaders(
	request map[string]any,
	version string,
	source SourceRef,
	sourceIndexOffset int,
) []runtimeHeaderOmission {
	if request == nil {
		return nil
	}
	rawList, hadHeaderList := request["header_list"]
	parsedList := parseMapRequestHeaderList(rawList, hadHeaderList, source, sourceIndexOffset)
	entries := parsedList.entries
	rawHeaders, hadHeaders := request["headers"]
	parsedMap := parseMapRequestHeaderMap(
		rawHeaders,
		hadHeaders,
		source,
		sourceIndexOffset+parsedList.sourceCount,
	)
	headerMap := parsedMap.headers
	legacyHeadersList := false
	parseOmissions := append(
		append([]runtimeHeaderOmission(nil), parsedList.omissions...),
		parsedMap.omissions...,
	)
	if !hadHeaderList && !parsedMap.recognized && hadHeaders {
		// Older http_sequence builders stored ordered entries directly in the
		// headers field. Preserve and normalize that accepted shape instead of
		// silently deleting it while migrating to header_list + summary.
		legacy := parseMapRequestHeaderList(
			rawHeaders,
			true,
			source,
			sourceIndexOffset,
		)
		if legacy.recognized {
			entries = legacy.entries
			legacyHeadersList = true
			parseOmissions = append(
				append([]runtimeHeaderOmission(nil), parsedList.omissions...),
				legacy.omissions...,
			)
		}
	}
	sanitizedList, sanitizedMap, omissions := sanitizeHeaderSurfaces(
		entries,
		headerMap,
		version,
		source,
		sourceIndexOffset,
	)
	omissions = append(omissions, parseOmissions...)
	sortRuntimeHeaderOmissions(omissions)
	if hadHeaderList || legacyHeadersList || len(sanitizedList) > 0 {
		out := make([]map[string]any, 0, len(sanitizedList))
		for _, header := range sanitizedList {
			entry := map[string]any{"name": header.Name, "value": header.Value}
			if header.Enabled != nil {
				entry["enabled"] = *header.Enabled
			}
			out = append(out, entry)
		}
		request["header_list"] = out
	}
	if len(sanitizedMap) == 0 {
		delete(request, "headers")
	} else {
		request["headers"] = sanitizedMap
	}
	return omissions
}

func sanitizeHeaderSurfaces(
	list dsl.HeaderList,
	headerMap map[string]string,
	version string,
	source SourceRef,
	sourceIndexOffset int,
) (dsl.HeaderList, map[string]string, []runtimeHeaderOmission) {
	importHeaders := make(HeaderList, 0, len(list))
	originalListNames := make(map[string]struct{}, len(list))
	for _, header := range list {
		importHeaders = append(importHeaders, Header{Name: header.Name, Value: header.Value, Enabled: header.Enabled, Source: source})
		originalListNames[headerIdentity(header.Name)] = struct{}{}
	}
	sanitizedList, listOmissions := sanitizeHeaderOccurrences(importHeaders, version)
	for index := range listOmissions {
		listOmissions[index].SourceIndex += sourceIndexOffset
	}

	outList := make(dsl.HeaderList, len(sanitizedList))
	for index := range sanitizedList {
		outList[index] = dsl.HeaderEntry{Name: sanitizedList[index].Name, Value: sanitizedList[index].Value, Enabled: sanitizedList[index].Enabled}
	}

	outMap := cloneStringMap(headerMap)
	// HeaderList is authoritative for every identity it originally carried.
	// Remove all matching compatibility entries before reviewing map-only
	// fields, then derive a fresh summary from the surviving list. This avoids
	// both resurrecting an omitted occurrence and retaining a stale last-value
	// summary after one of several list occurrences is removed.
	for identity := range originalListNames {
		for name := range outMap {
			if headerIdentity(name) == identity {
				delete(outMap, name)
			}
		}
	}

	unshadowed := make(map[string]string)
	for name, value := range outMap {
		unshadowed[name] = value
	}
	sanitizedMap, mapOmissions := sanitizeHeaderMapOccurrences(
		unshadowed,
		version,
		source,
		sourceIndexOffset+len(list),
	)
	for name := range unshadowed {
		if _, retained := sanitizedMap[name]; !retained {
			delete(outMap, name)
		}
	}
	summary := outList.ToMap()
	if len(summary) > 0 && outMap == nil {
		outMap = make(map[string]string, len(summary))
	}
	for name, value := range summary {
		outMap[name] = value
	}
	if len(outMap) == 0 {
		outMap = nil
	}
	omissions := append(listOmissions, mapOmissions...)
	sortRuntimeHeaderOmissions(omissions)
	return outList, outMap, omissions
}

func sanitizeHeaderMapOccurrences(
	headers map[string]string,
	version string,
	source SourceRef,
	sourceIndexOffset int,
) (map[string]string, []runtimeHeaderOmission) {
	if len(headers) == 0 {
		return nil, nil
	}
	active := cloneStringMap(headers)
	originalKeys := sortedStringKeys(headers)
	originalIndex := make(map[string]int, len(originalKeys))
	for index, key := range originalKeys {
		originalIndex[key] = sourceIndexOffset + index
	}

	var omissions []runtimeHeaderOmission
	seen := make(map[string]struct{})
	for pass := 0; pass <= len(headers) && len(active) > 0; pass++ {
		keys := sortedStringKeys(active)
		issues := structuralHeaderIssues(httpprotocol.HeaderMapIssuesForVersionWithUnresolvedTemplates(active, version), version)
		if len(issues) == 0 {
			break
		}
		remove := make(map[string]struct{}, len(issues))
		for _, issue := range issues {
			if issue.Index < 0 || issue.Index >= len(keys) {
				continue
			}
			key := keys[issue.Index]
			remove[key] = struct{}{}
			reason := headerIssueReasonCode(issue.Code)
			identity := key + "\x00" + reason
			if _, exists := seen[identity]; exists {
				continue
			}
			seen[identity] = struct{}{}
			omissions = append(omissions, runtimeHeaderOmission{
				SourceIndex:     originalIndex[key],
				Name:            safeHeaderEvidenceName(key),
				ReasonCode:      reason,
				Detail:          issue.Detail,
				Source:          source,
				identity:        headerIdentity(key),
				sourceName:      key,
				sourceValue:     active[key],
				hasSourceHeader: true,
			})
		}
		if len(remove) == 0 {
			for _, key := range keys {
				remove[key] = struct{}{}
				reason := "http_header_unmapped_runtime_issue"
				identity := key + "\x00" + reason
				if _, exists := seen[identity]; exists {
					continue
				}
				seen[identity] = struct{}{}
				omissions = append(omissions, runtimeHeaderOmission{
					SourceIndex:     originalIndex[key],
					Name:            safeHeaderEvidenceName(key),
					ReasonCode:      reason,
					Detail:          "the source header could not be represented by the native HTTP runtime",
					Source:          source,
					identity:        headerIdentity(key),
					sourceName:      key,
					sourceValue:     active[key],
					hasSourceHeader: true,
				})
			}
		}
		for key := range remove {
			delete(active, key)
		}
	}
	if len(active) == 0 {
		active = nil
	}
	sortRuntimeHeaderOmissions(omissions)
	return active, omissions
}

func headerOmissionsWithFallbackSource(input []runtimeHeaderOmission, fallback SourceRef) []runtimeHeaderOmission {
	if len(input) == 0 {
		return nil
	}
	out := append([]runtimeHeaderOmission(nil), input...)
	for index := range out {
		if sourceLocation(out[index].Source) == "" {
			out[index].Source = fallback
		}
	}
	return out
}

func sortRuntimeHeaderOmissions(omissions []runtimeHeaderOmission) {
	sort.SliceStable(omissions, func(i, j int) bool {
		if omissions[i].SourceIndex != omissions[j].SourceIndex {
			return omissions[i].SourceIndex < omissions[j].SourceIndex
		}
		if omissions[i].ReasonCode != omissions[j].ReasonCode {
			return omissions[i].ReasonCode < omissions[j].ReasonCode
		}
		return omissions[i].Name < omissions[j].Name
	})
}

func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func headerIdentity(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

type parsedMapRequestHeaderList struct {
	entries     dsl.HeaderList
	omissions   []runtimeHeaderOmission
	sourceCount int
	recognized  bool
}

func parseMapRequestHeaderList(
	raw any,
	present bool,
	source SourceRef,
	sourceIndexOffset int,
) parsedMapRequestHeaderList {
	switch values := raw.(type) {
	case dsl.HeaderList:
		return parsedMapRequestHeaderList{
			entries:     append(dsl.HeaderList(nil), values...),
			sourceCount: len(values),
			recognized:  true,
		}
	case []dsl.HeaderEntry:
		return parsedMapRequestHeaderList{
			entries:     append(dsl.HeaderList(nil), values...),
			sourceCount: len(values),
			recognized:  true,
		}
	case []map[string]any:
		out := make(dsl.HeaderList, 0, len(values))
		var omissions []runtimeHeaderOmission
		for index, value := range values {
			name, nameOK := value["name"].(string)
			headerValue, valueOK := value["value"].(string)
			if !nameOK || !valueOK {
				omissions = append(omissions, malformedHeaderShapeOmission(
					sourceIndexOffset+index,
					name,
					source,
				))
				continue
			}
			header := dsl.HeaderEntry{Name: name, Value: headerValue}
			if rawEnabled, present := value["enabled"]; present {
				enabled, ok := rawEnabled.(bool)
				if !ok {
					omissions = append(omissions, malformedHeaderShapeOmission(sourceIndexOffset+index, name, source))
					continue
				}
				header.Enabled = &enabled
			}
			out = append(out, header)
		}
		return parsedMapRequestHeaderList{
			entries:     out,
			omissions:   omissions,
			sourceCount: len(values),
			recognized:  true,
		}
	case []any:
		out := make(dsl.HeaderList, 0, len(values))
		var omissions []runtimeHeaderOmission
		for index, rawValue := range values {
			value, ok := rawValue.(map[string]any)
			if !ok {
				omissions = append(omissions, malformedHeaderShapeOmission(
					sourceIndexOffset+index,
					"",
					source,
				))
				continue
			}
			name, nameOK := value["name"].(string)
			headerValue, valueOK := value["value"].(string)
			if !nameOK || !valueOK {
				omissions = append(omissions, malformedHeaderShapeOmission(
					sourceIndexOffset+index,
					name,
					source,
				))
				continue
			}
			header := dsl.HeaderEntry{Name: name, Value: headerValue}
			if rawEnabled, present := value["enabled"]; present {
				enabled, ok := rawEnabled.(bool)
				if !ok {
					omissions = append(omissions, malformedHeaderShapeOmission(sourceIndexOffset+index, name, source))
					continue
				}
				header.Enabled = &enabled
			}
			out = append(out, header)
		}
		return parsedMapRequestHeaderList{
			entries:     out,
			omissions:   omissions,
			sourceCount: len(values),
			recognized:  true,
		}
	case map[string]string:
		return parsedMapRequestHeaderList{
			entries:     dsl.HeaderListFromMap(values),
			sourceCount: len(values),
			recognized:  true,
		}
	case map[string]any:
		keys := make([]string, 0, len(values))
		for name := range values {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		headers := make(map[string]string, len(values))
		var omissions []runtimeHeaderOmission
		for index, name := range keys {
			value, ok := values[name].(string)
			if !ok {
				omissions = append(omissions, malformedHeaderShapeOmission(
					sourceIndexOffset+index,
					name,
					source,
				))
				continue
			}
			headers[name] = value
		}
		return parsedMapRequestHeaderList{
			entries:     dsl.HeaderListFromMap(headers),
			omissions:   omissions,
			sourceCount: len(values),
			recognized:  true,
		}
	case nil:
		return parsedMapRequestHeaderList{recognized: present}
	default:
		if !present {
			return parsedMapRequestHeaderList{}
		}
		return parsedMapRequestHeaderList{
			omissions: []runtimeHeaderOmission{
				malformedHeaderShapeOmission(sourceIndexOffset, "", source),
			},
			sourceCount: 1,
		}
	}
}

type parsedMapRequestHeaderMap struct {
	headers     map[string]string
	omissions   []runtimeHeaderOmission
	sourceCount int
	recognized  bool
}

func parseMapRequestHeaderMap(
	raw any,
	present bool,
	source SourceRef,
	sourceIndexOffset int,
) parsedMapRequestHeaderMap {
	switch values := raw.(type) {
	case map[string]string:
		return parsedMapRequestHeaderMap{
			headers:     cloneStringMap(values),
			sourceCount: len(values),
			recognized:  true,
		}
	case map[string]any:
		out := make(map[string]string, len(values))
		keys := make([]string, 0, len(values))
		for name := range values {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		var omissions []runtimeHeaderOmission
		for index, name := range keys {
			rawValue := values[name]
			if value, ok := rawValue.(string); ok {
				out[name] = value
				continue
			}
			omissions = append(omissions, malformedHeaderShapeOmission(
				sourceIndexOffset+index,
				name,
				source,
			))
		}
		if len(out) == 0 {
			out = nil
		}
		return parsedMapRequestHeaderMap{
			headers:     out,
			omissions:   omissions,
			sourceCount: len(values),
			recognized:  true,
		}
	case nil:
		return parsedMapRequestHeaderMap{recognized: present}
	default:
		if !present {
			return parsedMapRequestHeaderMap{}
		}
		return parsedMapRequestHeaderMap{
			omissions: []runtimeHeaderOmission{
				malformedHeaderShapeOmission(sourceIndexOffset, "", source),
			},
			sourceCount: 1,
		}
	}
}

func malformedHeaderShapeOmission(index int, name string, source SourceRef) runtimeHeaderOmission {
	evidenceName := "<header>"
	if strings.TrimSpace(name) != "" {
		evidenceName = safeHeaderEvidenceName(name)
	}
	return runtimeHeaderOmission{
		SourceIndex: index,
		Name:        evidenceName,
		ReasonCode:  "http_header_malformed_shape",
		Detail:      "the imported header occurrence has an unsupported structured shape",
		Source:      source,
		identity:    headerIdentity(name),
	}
}

func runtimeSurfaceDeclaredHTTPVersion(ref dslwalk.SurfaceRef) string {
	if metadata := ref.Metadata(); metadata != nil {
		if value, ok := metadata["http_version"].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sourceHTTPVersionPolicy(raw string) (string, bool) {
	version := sourceHTTPVersionNormalized(raw)
	switch version {
	case "auto", "1.1", "2":
		return version, true
	default:
		return "", false
	}
}

func sourceHTTPVersionNormalized(raw string) string {
	return httpprotocol.NormalizeVersion(raw)
}

func sourceHTTPVersionExecutable(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	_, ok := sourceHTTPVersionPolicy(raw)
	return ok
}

func runtimeSurfaceEffectiveHTTPVersion(ref dslwalk.SurfaceRef, inherited string) (string, error) {
	if ref.Action != nil {
		return dsl.EffectiveHTTPVersion(*ref.Action, inherited)
	}
	action := dsl.Action{}
	if raw, present := ref.Request["http_version"]; present {
		value, ok := raw.(string)
		if !ok {
			return "auto", fmt.Errorf("invalid HTTP version scalar")
		}
		var err error
		action.HTTPVersion, err = dsl.NewHTTPVersionOverride(value)
		if err != nil {
			return "auto", err
		}
	}
	return dsl.EffectiveHTTPVersion(action, inherited)
}

func structuralHeaderIssues(issues []httpprotocol.HeaderListIssue, version string) []httpprotocol.HeaderListIssue {
	if version != "structural" {
		return issues
	}
	retained := issues[:0]
	for _, issue := range issues {
		if issue.Code != httpprotocol.HeaderListIssueHopByHopUnsupported && issue.Code != httpprotocol.HeaderListIssueExpectContinueUnsupported {
			retained = append(retained, issue)
		}
	}
	return retained
}

// Incompatible inherited defaults are gated on the affected leaf, rather than
// removed globally and silently lost from valid sibling/resource requests.
func inheritedDefaultHeaderIssues(config dsl.HTTPConfig, ref dslwalk.SurfaceRef, version string, source SourceRef) []runtimeHeaderOmission {
	headers := cloneStringMap(config.DefaultHeaders)
	if headers == nil {
		headers = make(map[string]string)
	}
	for _, header := range []struct{ name, value string }{{"User-Agent", config.UserAgent}, {"Accept-Encoding", config.AcceptEncoding}, {"Accept-Language", config.AcceptLanguage}} {
		if header.value != "" {
			if !hasHeaderName(headers, header.name) {
				headers[header.name] = header.value
			}
		}
	}
	var localHeaders map[string]string
	var localList dsl.HeaderList
	if ref.Action != nil {
		localHeaders, localList = ref.Action.Headers, ref.Action.HeaderList
	} else {
		if raw, err := json.Marshal(ref.Request); err == nil {
			var action dsl.Action
			if json.Unmarshal(raw, &action) == nil {
				localHeaders, localList = action.Headers, action.HeaderList
			}
		}
	}
	shadowed := make(map[string]bool)
	for name := range localHeaders {
		shadowed[strings.ToLower(name)] = true
		for inherited := range headers {
			if strings.EqualFold(name, inherited) {
				delete(headers, inherited)
			}
		}
	}
	for _, header := range localList {
		shadowed[strings.ToLower(header.Name)] = true
	}
	var defaults dsl.HeaderList
	for _, header := range config.DefaultHeaderList {
		if !shadowed[strings.ToLower(header.Name)] {
			defaults = append(defaults, header)
		}
	}
	for _, header := range append(append(dsl.HeaderList(nil), config.DefaultHeaderList...), localList...) {
		for inherited := range headers {
			if strings.EqualFold(header.Name, inherited) {
				delete(headers, inherited)
			}
		}
	}
	_, _, omissions := sanitizeHeaderSurfaces(defaults, headers, version, source, 0)
	return omissions
}

func runtimeSurfaceIsHTTPRequest(ref dslwalk.SurfaceRef) bool {
	if ref.IsNestedRequest() {
		return true
	}
	if ref.Action == nil {
		return false
	}
	actionType := strings.ToLower(strings.TrimSpace(ref.Action.Type))
	if actionType == "http_sequence" || strings.HasPrefix(actionType, "control_") {
		return false
	}
	if strings.TrimSpace(ref.Action.Target) == "" {
		return false
	}
	if strings.TrimSpace(ref.Action.Method) != "" {
		return true
	}
	switch actionType {
	case "request", "http_request", "soap_request", "java_http_request":
		return true
	default:
		return httpprotocol.MethodExecutable(actionType)
	}
}

func runtimeSurfaceSource(ref dslwalk.SurfaceRef, fallback SourceRef, sourceProfile string) SourceRef {
	source := fallback
	if source.Format == "" {
		source.Format = sourceProfile
	}
	metadata := ref.Metadata()
	if metadata != nil {
		if value, ok := metadata["source_file"].(string); ok && strings.TrimSpace(value) != "" {
			source.Path = strings.TrimSpace(value)
		}
		if value, found := metadata["source_line"]; found {
			switch line := value.(type) {
			case int:
				if line > 0 {
					source.Pointer = "line:" + strconv.Itoa(line)
				}
			case float64:
				if line > 0 && line == float64(int(line)) {
					source.Pointer = "line:" + strconv.Itoa(int(line))
				}
			}
		}
	}
	if source.Pointer == "" {
		source.Pointer = ref.Path
	}
	if source.ItemID == "" {
		source.ItemID = ref.Name()
	}
	return source
}

func isHTTPFamilyProtocol(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(ProtocolHTTP), "http/1.1", "http/2", string(ProtocolSOAP), string(ProtocolJavaHTTP):
		return true
	default:
		return false
	}
}

func findingsForActionPath(findings []importreview.Finding, path string) []importreview.Finding {
	var out []importreview.Finding
	for _, finding := range findings {
		if finding.ActionPath == path {
			out = append(out, finding)
		}
	}
	return out
}

func dedupeSourceMappings(input []SourceMapping) []SourceMapping {
	if len(input) == 0 {
		return nil
	}
	out := make([]SourceMapping, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, mapping := range input {
		key := strings.Join([]string{
			mapping.Source.Format,
			mapping.Source.Path,
			mapping.Source.Pointer,
			mapping.Source.ItemID,
			mapping.Target,
		}, "\x00")
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, mapping)
	}
	return out
}
