package http

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
	"golang.org/x/net/http/httpguts"
)

// AuthSupport describes whether the native HTTP runtime can execute an auth
// mode without source-specific compatibility logic.
type AuthSupport string

const (
	AuthExecutable  AuthSupport = "executable"
	AuthReviewOnly  AuthSupport = "review_only"
	AuthUnsupported AuthSupport = "unsupported"
)

const (
	AuthTypeNone      = "none"
	AuthTypeInherit   = "inherit"
	AuthTypeBasic     = "basic"
	AuthTypeBearer    = "bearer"
	AuthTypeDigest    = "digest"
	AuthTypeNTLM      = "ntlm"
	AuthTypeKerberos  = "kerberos"
	AuthTypeNegotiate = "negotiate"
	// AuthTypeNegotiateAny answers whichever challenge the server sends.
	AuthTypeNegotiateAny = "negotiate_any"
	AuthTypeAWS          = "aws"
	AuthTypeHawk         = "hawk"
)

var supportedMethods = [...]string{
	nethttp.MethodGet,
	nethttp.MethodPost,
	nethttp.MethodPut,
	nethttp.MethodPatch,
	nethttp.MethodDelete,
	nethttp.MethodHead,
	nethttp.MethodOptions,
}

const (
	VersionHTTP11 = "1.1"
	VersionHTTP2  = "2"
	// VersionHTTP3 requires final HTTP/3 over QUIC from the first request,
	// with no TCP fallback.
	VersionHTTP3 = "3"
	// VersionAutoH3 negotiates HTTP/2 or HTTP/1.1 over TCP and upgrades later
	// requests to a same-host HTTP/3 alternative the origin advertised.
	VersionAutoH3 = "auto+h3"
)

var supportedVersions = [...]string{
	VersionHTTP11,
	VersionHTTP2,
}

var (
	// ErrHTTP10Unsupported is returned before transport creation whenever a
	// caller explicitly requests HTTP/1.0. net/http ignores Request.Proto on
	// client requests and cannot provide exact HTTP/1.0 wire semantics.
	ErrHTTP10Unsupported = errors.New("http version 1.0 is not supported by the native runtime")
	// ErrUnsupportedVersion is the errors.Is target for unknown explicit
	// version strings.
	ErrUnsupportedVersion = errors.New("unsupported HTTP version")
	// ErrHTTP2Required prefixes transport failures when version 2 was selected.
	// The configured transport has no HTTP/1 fallback in this mode.
	ErrHTTP2Required = errors.New("http version 2 is required")
	// ErrHTTP3NotEnabled is returned before any traffic when a request
	// resolves to an HTTP/3 policy (3 or auto+h3) on a runtime that cannot
	// execute it. Scripts holding those policies still validate and save.
	ErrHTTP3NotEnabled = errors.New("HTTP/3 execution is not enabled on this installation")
)

// VersionUsesQUIC reports whether a normalized policy can send requests over
// QUIC: exact HTTP/3, or the auto+h3 upgrade.
func VersionUsesQUIC(version string) bool {
	return version == VersionHTTP3 || version == VersionAutoH3
}

// UnsupportedVersionError identifies an unknown explicit HTTP version without
// requiring callers to parse its Error text.
type UnsupportedVersionError struct {
	Version string
}

func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("unsupported HTTP version %q", e.Version)
}

func (e *UnsupportedVersionError) Unwrap() error {
	return ErrUnsupportedVersion
}

// HeaderListIssueCode is a stable, disclosure-safe reason that an authored
// header occurrence cannot retain its semantics through net/http.
type HeaderListIssueCode string

const (
	HeaderListIssueInvalidName               HeaderListIssueCode = "invalid_header_name"
	HeaderListIssueInvalidValue              HeaderListIssueCode = "invalid_header_value"
	HeaderListIssueDuplicateSingleton        HeaderListIssueCode = "duplicate_singleton_header"
	HeaderListIssueTransportOwned            HeaderListIssueCode = "transport_owned_header"
	HeaderListIssueHopByHopUnsupported       HeaderListIssueCode = "hop_by_hop_header"
	HeaderListIssueAmbiguousMap              HeaderListIssueCode = "ambiguous_header_map"
	HeaderListIssueExpectContinueUnsupported HeaderListIssueCode = "expect_continue_unsupported"
)

// HeaderListIssue points to the original occurrence. Detail intentionally
// contains no header value so it can be returned in import findings and logs.
type HeaderListIssue struct {
	Code   HeaderListIssueCode `json:"code"`
	Index  int                 `json:"index"`
	Name   string              `json:"name"`
	Detail string              `json:"detail"`
}

// HeaderListValidationError is returned by ValidateHeaderList* and exposes all
// deterministic issues through errors.As.
type HeaderListValidationError struct {
	Issues []HeaderListIssue
}

func (e *HeaderListValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "http header list has unsupported wire semantics"
	}
	first := e.Issues[0]
	if len(e.Issues) == 1 {
		return fmt.Sprintf("http header occurrence %d (%s): %s", first.Index, first.Name, first.Detail)
	}
	return fmt.Sprintf(
		"http header occurrence %d (%s): %s (and %d more issue(s))",
		first.Index,
		first.Name,
		first.Detail,
		len(e.Issues)-1,
	)
}

// AuthWireValueError identifies a typed-auth field that cannot be projected
// onto the HTTP wire safely. Error text deliberately excludes the field value.
type AuthWireValueError struct {
	Field string
}

func (e *AuthWireValueError) Error() string {
	field := strings.TrimSpace(e.Field)
	if field == "" {
		field = "value"
	}
	return "HTTP authentication " + field + " is not valid on the wire"
}

// AuthCapability is the importer-facing description of one canonical auth
// mode. Source aliases are normalized before lookup.
type AuthCapability struct {
	Type               string      `json:"type"`
	Support            AuthSupport `json:"support"`
	RequiresUsername   bool        `json:"requires_username,omitempty"`
	RequiresPassword   bool        `json:"requires_password,omitempty"`
	RequiresToken      bool        `json:"requires_token,omitempty"`
	ChallengeResponse  bool        `json:"challenge_response,omitempty"`
	ExplicitNoAuth     bool        `json:"explicit_no_auth,omitempty"`
	InheritsScriptAuth bool        `json:"inherits_script_auth,omitempty"`
}

// RuntimeCapabilities exposes HTTP limits and auth support to converters
// without duplicating private runtime switches.
type RuntimeCapabilities struct {
	MaxTimeoutMS   int              `json:"max_timeout_ms"`
	MaxRedirects   int              `json:"max_redirects"`
	MaxAssertions  int              `json:"max_assertions"`
	Methods        []string         `json:"methods"`
	Versions       []string         `json:"versions"`
	Authentication []AuthCapability `json:"authentication"`
}

// NormalizeVersion canonicalizes an authored HTTP version. An empty value
// means runtime negotiation (HTTP/1.1 with opportunistic HTTP/2). Final
// HTTP/3 spellings become 3; draft tokens such as h3-29 are left as they are,
// so they stay observations and never become a policy.
func NormalizeVersion(raw string) string {
	version := strings.TrimSpace(raw)
	switch strings.ToUpper(version) {
	case "HTTP/1.0":
		return "1.0"
	case "HTTP/1.1":
		return VersionHTTP11
	case "HTTP/2", "HTTP/2.0":
		return VersionHTTP2
	case "HTTP/3", "HTTP/3.0", "H3":
		return VersionHTTP3
	default:
		return version
	}
}

// SupportedVersions returns the explicit version modes this process can
// execute. Empty/omitted remains a supported automatic negotiation mode. The
// HTTP/3 policies appear only where HTTP/3 execution is enabled (APEXLOAD_HTTP3)
// and built: 3 with the exact transport, auto+h3 with the upgrade router.
func SupportedVersions() []string {
	versions := append([]string(nil), supportedVersions[:]...)
	if HTTP3ExecutionError() == nil {
		versions = append(versions, VersionHTTP3)
	}
	if HTTP3UpgradeExecutionError() == nil {
		versions = append(versions, VersionAutoH3)
	}
	return versions
}

// VersionExecutable reports whether raw is a policy the runtime can select
// exactly: the empty automatic mode, an exact TCP version or an HTTP/3 policy.
// HTTP/3 policies still need an installation with HTTP/3 execution enabled
// (APEXLOAD_HTTP3), which admission checks; importers may map captured HTTP/3
// to them because a draft that cannot run there is refused, not altered.
func VersionExecutable(raw string) bool {
	switch NormalizeVersion(raw) {
	case "", VersionHTTP11, VersionHTTP2, VersionHTTP3, VersionAutoH3:
		return true
	default:
		return false
	}
}

// ValidateVersion is the authoritative structural HTTP-version validation
// for a session (config) policy. It accepts the HTTP/3 policies whether or
// not this installation can execute them; a request that resolves to one is
// refused at dispatch with ErrHTTP3NotEnabled.
func ValidateVersion(raw string) error {
	version := NormalizeVersion(raw)
	switch version {
	case "", VersionHTTP11, VersionHTTP2, VersionHTTP3, VersionAutoH3:
		return nil
	case "1.0":
		return ErrHTTP10Unsupported
	default:
		return &UnsupportedVersionError{Version: version}
	}
}

// HeaderListIssues reports occurrences that net/http cannot preserve in the
// automatic protocol mode. Importers should prefer HeaderListIssuesForVersion
// when an exact source version is known.
func HeaderListIssues(headers protocol.HeaderList) []HeaderListIssue {
	return HeaderListIssuesForVersion(headers, "")
}

// HeaderListIssuesForVersion reports unsupported occurrences in source order.
// Ordinary repeated headers are supported. Host and User-Agent are singleton
// request fields in net/http; framing headers are transport-owned; and
// connection-specific headers are supported only when HTTP/1.1 is required.
func HeaderListIssuesForVersion(headers protocol.HeaderList, version string) []HeaderListIssue {
	return headerListIssuesForVersion(headers, version, false, false)
}

// HeaderListIssuesForVersionWithUnresolvedTemplates performs the same
// occurrence and protocol checks while allowing bounded {{...}} spans that
// have not yet been interpolated. Literal invalid bytes outside a template
// remain issues. Execute always uses the strict resolved-value validator.
func HeaderListIssuesForVersionWithUnresolvedTemplates(headers protocol.HeaderList, version string) []HeaderListIssue {
	return headerListIssuesForVersion(headers, version, true, true)
}

func headerListIssuesForVersion(headers protocol.HeaderList, version string, allowUnresolvedNames, allowUnresolvedValues bool) []HeaderListIssue {
	if len(headers) == 0 {
		return nil
	}

	normalizedVersion := NormalizeVersion(version)
	exactHTTP11 := normalizedVersion == VersionHTTP11
	// Exact HTTP/2 and exact HTTP/3 never fall back to HTTP/1.1, so its
	// Expect rule does not apply to them; auto+h3 may still use HTTP/1.1.
	noHTTP1 := normalizedVersion == VersionHTTP2 || normalizedVersion == VersionHTTP3
	singletonCounts := make(map[string]int, 2)
	expectCount := 0
	firstExpectHasContinue := false
	issues := make([]HeaderListIssue, 0)
	for index, header := range headers {
		if header.Enabled != nil && !*header.Enabled {
			continue
		}
		name := header.Name
		canonicalName := strings.ToLower(name)
		validName := httpguts.ValidHeaderFieldName(name)
		if !validName && allowUnresolvedNames {
			if replaced, ok := replaceUnresolvedTemplates(name, "X"); ok {
				validName = httpguts.ValidHeaderFieldName(replaced)
			}
		}
		if !validName {
			issues = append(issues, HeaderListIssue{
				Code:   HeaderListIssueInvalidName,
				Index:  index,
				Name:   "<invalid>",
				Detail: "header name contains characters HTTP does not allow",
			})
			continue
		}
		validValue := validResolvedHeaderValue(canonicalName, header.Value)
		if !validValue && allowUnresolvedValues {
			replacement := "template"
			if canonicalName == "cookie" {
				// A whole Cookie value still needs name=value shape while its
				// runtime template is unresolved. Strict execution validation
				// parses the interpolated value again before transport.
				replacement = "template=template"
			}
			if replaced, ok := replaceUnresolvedTemplates(header.Value, replacement); ok {
				validValue = validResolvedHeaderValue(canonicalName, replaced)
			}
		}
		if !validValue {
			issues = append(issues, HeaderListIssue{
				Code:   HeaderListIssueInvalidValue,
				Index:  index,
				Name:   name,
				Detail: InvalidHeaderValueDetail(name, header.Value),
			})
			continue
		}

		switch canonicalName {
		case "host", "user-agent":
			singletonCounts[canonicalName]++
			if singletonCounts[canonicalName] > 1 {
				issues = append(issues, HeaderListIssue{
					Code:   HeaderListIssueDuplicateSingleton,
					Index:  index,
					Name:   name,
					Detail: "multiple occurrences cannot be preserved by the native HTTP transport",
				})
			}
		case "content-length", "transfer-encoding", "trailer":
			issues = append(issues, HeaderListIssue{
				Code:   HeaderListIssueTransportOwned,
				Index:  index,
				Name:   name,
				Detail: "the native HTTP transport owns this framing header",
			})
		case "proxy-authorization":
			issues = append(issues, HeaderListIssue{
				Code:   HeaderListIssueTransportOwned,
				Index:  index,
				Name:   name,
				Detail: "proxy authentication is owned by the native HTTP transport",
			})
		case "expect":
			hasContinue := httpguts.HeaderValuesContainsToken([]string{header.Value}, "100-continue")
			if expectCount == 0 {
				firstExpectHasContinue = hasContinue
			} else if !noHTTP1 && hasContinue && !firstExpectHasContinue {
				issues = append(issues, HeaderListIssue{
					Code:   HeaderListIssueExpectContinueUnsupported,
					Index:  index,
					Name:   name,
					Detail: "HTTP/1.1 cannot honor 100-continue outside the first Expect occurrence",
				})
			}
			expectCount++
		case "connection", "keep-alive", "proxy-connection", "te", "upgrade":
			if !exactHTTP11 {
				issues = append(issues, HeaderListIssue{
					Code:   HeaderListIssueHopByHopUnsupported,
					Index:  index,
					Name:   name,
					Detail: "connection-specific headers require exact HTTP/1.1 mode",
				})
			}
		}
	}
	if len(issues) == 0 {
		return nil
	}
	return issues
}

// InvalidHeaderValueDetail says, without repeating the value, why HTTP cannot
// carry a header value that validResolvedHeaderValue rejects.
func InvalidHeaderValueDetail(name, value string) string {
	canonicalName := strings.ToLower(strings.TrimSpace(name))
	switch {
	case !httpguts.ValidHeaderFieldValue(value):
		return "header value contains characters HTTP does not allow"
	case (canonicalName == "host" || canonicalName == "user-agent") && strings.TrimSpace(value) == "":
		return "header value cannot be empty"
	case canonicalName == "host":
		return "header value is not a valid host"
	default:
		return "header value contains characters HTTP does not allow"
	}
}

func validResolvedHeaderValue(canonicalName, value string) bool {
	if !httpguts.ValidHeaderFieldValue(value) {
		return false
	}
	switch canonicalName {
	case "host":
		// An explicit empty Host does not mean an empty wire authority in
		// net/http: assigning Request.Host="" silently falls back to URL.Host.
		// Reject it so imported drafts cannot be classified exact while replaying
		// against a different virtual host.
		return strings.TrimSpace(value) != "" && httpguts.ValidHostHeader(value)
	case "user-agent":
		// net/http treats an explicitly empty User-Agent as a sentinel that
		// suppresses the field entirely. That is useful for an unauthored
		// request, but cannot preserve an authored empty header occurrence.
		return value != ""
	case "cookie":
		_, err := nethttp.ParseCookie(value)
		return err == nil
	default:
		return true
	}
}

// ValidateAuthWireValues validates the resolved typed-auth values that the
// native runtime will place on the wire. Basic credentials are base64 encoded;
// bearer tokens are inserted verbatim; digest usernames are emitted as a
// quoted header value while passwords are hashed. UTF-8 validation also keeps
// protected-session JSON from silently changing an imported credential.
func ValidateAuthWireValues(auth *protocol.AuthConfig) error {
	if auth == nil {
		return nil
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "username", value: auth.Username},
		{name: "password", value: auth.Password},
		{name: "token", value: auth.Token},
		{name: "domain", value: auth.Domain},
	} {
		if !utf8.ValidString(field.value) {
			return &AuthWireValueError{Field: field.name}
		}
	}
	switch NormalizeAuthType(auth.Type) {
	case AuthTypeBasic:
		// RFC 7617 uses the first colon as the user-id/password delimiter.
		// Encoding a typed username that itself contains ':' would therefore
		// replay a different username while appearing to have succeeded.
		if strings.Contains(auth.Username, ":") {
			return &AuthWireValueError{Field: "username"}
		}
	case AuthTypeBearer:
		if !httpguts.ValidHeaderFieldValue("Bearer " + auth.Token) {
			return &AuthWireValueError{Field: "token"}
		}
	case AuthTypeNTLM, AuthTypeNegotiate, AuthTypeNegotiateAny:
		// The credentials travel as Basic user:password into the handshake
		// (and as Basic itself for negotiate_any), so the Basic rules apply.
		if strings.TrimSpace(auth.Username) == "" || strings.Contains(auth.Username, ":") || !httpguts.ValidHeaderFieldValue(auth.Username) {
			return &AuthWireValueError{Field: "username"}
		}
	case AuthTypeDigest:
		// quoteDigestValue escapes quotes and backslashes, but control bytes
		// would remain invalid in the eventual Authorization header.
		if !httpguts.ValidHeaderFieldValue(auth.Username) {
			return &AuthWireValueError{Field: "username"}
		}
		if strings.TrimSpace(auth.Username) == "" {
			return &AuthWireValueError{Field: "username"}
		}
		if auth.Password == "" {
			return &AuthWireValueError{Field: "password"}
		}
	}
	return nil
}

// replaceUnresolvedTemplates replaces one or more bounded {{...}} spans while
// rejecting unmatched braces and control bytes inside a span. The replacement
// lets callers validate all literal bytes without guessing the future value.
func replaceUnresolvedTemplates(value, replacement string) (string, bool) {
	var out strings.Builder
	replaced := false
	for position := 0; position < len(value); {
		start := strings.Index(value[position:], "{{")
		if start < 0 {
			remainder := value[position:]
			if strings.Contains(remainder, "}}") {
				return "", false
			}
			out.WriteString(remainder)
			break
		}
		start += position
		literal := value[position:start]
		if strings.Contains(literal, "}}") {
			return "", false
		}
		out.WriteString(literal)
		end := strings.Index(value[start+2:], "}}")
		if end < 0 {
			return "", false
		}
		end += start + 2
		expression := value[start+2 : end]
		if strings.TrimSpace(expression) == "" ||
			strings.Contains(expression, "{{") ||
			strings.IndexFunc(expression, func(r rune) bool { return r < ' ' || r == 0x7f }) >= 0 {
			return "", false
		}
		out.WriteString(replacement)
		replaced = true
		position = end + 2
	}
	return out.String(), replaced
}

// ValidateHeaderList rejects headers that cannot retain their semantics in the
// automatic protocol mode.
func ValidateHeaderList(headers protocol.HeaderList) error {
	return ValidateHeaderListForVersion(headers, "")
}

// ValidateHeaderListForVersion rejects headers that cannot retain their
// semantics for version while retaining every issue for errors.As callers.
func ValidateHeaderListForVersion(headers protocol.HeaderList, version string) error {
	issues := HeaderListIssuesForVersion(headers, version)
	if len(issues) == 0 {
		return nil
	}
	return &HeaderListValidationError{Issues: issues}
}

// HeaderMapIssuesForVersion validates a compatibility header map in
// deterministic name order. Case-insensitive key collisions are rejected
// because map iteration plus Header.Set cannot define a stable winner.
func HeaderMapIssuesForVersion(headers map[string]string, version string) []HeaderListIssue {
	return headerMapIssuesForVersion(headers, version, false)
}

// HeaderMapIssuesForVersionWithUnresolvedTemplates is the pre-interpolation
// variant for DSL and importer validation. It permits templates in values, but
// not map keys because the engine does not interpolate compatibility-map keys.
func HeaderMapIssuesForVersionWithUnresolvedTemplates(headers map[string]string, version string) []HeaderListIssue {
	return headerMapIssuesForVersion(headers, version, true)
}

func headerMapIssuesForVersion(headers map[string]string, version string, allowUnresolvedTemplates bool) []HeaderListIssue {
	entries := sortedHeaderMapEntries(headers, nil)
	issues := headerMapCollisionIssues(entries, 0)
	if allowUnresolvedTemplates {
		// Header-map keys are not interpolated by the engine; values are.
		issues = append(issues, headerListIssuesForVersion(entries, version, false, true)...)
	} else {
		issues = append(issues, HeaderListIssuesForVersion(entries, version)...)
	}
	if len(issues) == 0 {
		return nil
	}
	sort.SliceStable(issues, func(left, right int) bool {
		return issues[left].Index < issues[right].Index
	})
	return issues
}

// effectiveAuthoredHeaderList projects the actual typed/default/action
// precedence into deterministic occurrences for runtime validation. Ordered
// lists shadow their compatibility maps; action headers shadow both default
// representations case-insensitively.
// EffectiveAuthoredHeaderList exposes the native default/action precedence for
// DSL and importer admission, without mutating either header representation.
func EffectiveAuthoredHeaderList(defaults map[string]string, defaultList protocol.HeaderList, actionHeaders map[string]string, actionList protocol.HeaderList) (protocol.HeaderList, []HeaderListIssue) {
	return effectiveAuthoredHeaderList(defaults, defaultList, actionHeaders, actionList)
}

func effectiveAuthoredHeaderList(
	defaults map[string]string,
	defaultHeaderList protocol.HeaderList,
	actionHeaders map[string]string,
	headerList protocol.HeaderList,
) (protocol.HeaderList, []HeaderListIssue) {
	defaultListNames := headerNames(defaultHeaderList)
	actionListNames := headerNames(headerList)
	actionNames := mapHeaderNames(actionHeaders)
	out := make(protocol.HeaderList, 0, len(defaults)+len(defaultHeaderList)+len(actionHeaders)+len(headerList))

	defaultShadows := make(map[string]struct{}, len(defaultListNames)+len(actionNames)+len(actionListNames))
	for name := range defaultListNames {
		defaultShadows[name] = struct{}{}
	}
	for name := range actionNames {
		defaultShadows[name] = struct{}{}
	}
	for name := range actionListNames {
		defaultShadows[name] = struct{}{}
	}
	defaultEntries := sortedHeaderMapEntries(defaults, defaultShadows)
	actionShadows := make(map[string]struct{}, len(actionNames)+len(actionListNames))
	for name := range actionNames {
		actionShadows[name] = struct{}{}
	}
	for name := range actionListNames {
		actionShadows[name] = struct{}{}
	}
	defaultOrderedEntries := filterHeaderList(defaultHeaderList, actionShadows)
	actionEntries := sortedHeaderMapEntries(actionHeaders, actionListNames)
	actionOrderedEntries := filterHeaderList(headerList, nil)
	mapIssues := headerMapCollisionIssues(defaultEntries, 0)
	mapIssues = append(mapIssues, headerMapCollisionIssues(actionEntries, len(defaultEntries)+len(defaultOrderedEntries))...)
	out = append(out, defaultEntries...)
	out = append(out, defaultOrderedEntries...)
	out = append(out, actionEntries...)
	out = append(out, actionOrderedEntries...)
	if len(out) == 0 {
		return nil, mapIssues
	}
	return out, mapIssues
}

func filterHeaderList(headers protocol.HeaderList, shadowed map[string]struct{}) protocol.HeaderList {
	if len(headers) == 0 {
		return nil
	}
	filtered := make(protocol.HeaderList, 0, len(headers))
	for _, header := range headers {
		if header.Enabled != nil && !*header.Enabled {
			continue
		}
		if _, exists := shadowed[strings.ToLower(strings.TrimSpace(header.Name))]; exists {
			continue
		}
		filtered = append(filtered, header)
	}
	return filtered
}

func sortedHeaderMapEntries(headers map[string]string, shadowed map[string]struct{}) protocol.HeaderList {
	keys := make([]string, 0, len(headers))
	for name := range headers {
		if _, exists := shadowed[strings.ToLower(name)]; !exists {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make(protocol.HeaderList, 0, len(keys))
	for _, name := range keys {
		entries = append(entries, protocol.HeaderEntry{Name: name, Value: headers[name]})
	}
	return entries
}

func headerMapCollisionIssues(entries protocol.HeaderList, offset int) []HeaderListIssue {
	firstByName := make(map[string]int, len(entries))
	var issues []HeaderListIssue
	for index, entry := range entries {
		name := strings.ToLower(entry.Name)
		if _, duplicate := firstByName[name]; duplicate {
			issues = append(issues, HeaderListIssue{
				Code:   HeaderListIssueAmbiguousMap,
				Index:  offset + index,
				Name:   entry.Name,
				Detail: "case-insensitive duplicate map keys have no stable winner",
			})
			continue
		}
		firstByName[name] = index
	}
	return issues
}

func headerNames(headers protocol.HeaderList) map[string]struct{} {
	names := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		name := strings.ToLower(strings.TrimSpace(header.Name))
		if name != "" {
			// An ordered entry remains authoritative for its name even when it
			// is disabled. This lets the ordered representation intentionally
			// suppress a stale compatibility-map value.
			names[name] = struct{}{}
		}
	}
	return names
}

func mapHeaderNames(headers map[string]string) map[string]struct{} {
	names := make(map[string]struct{}, len(headers))
	for name := range headers {
		names[strings.ToLower(name)] = struct{}{}
	}
	return names
}

// NormalizeMethod returns the canonical runtime spelling for a source HTTP
// method. It does not imply that the method is executable.
func NormalizeMethod(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

// SupportedMethods returns a detached list in the stable catalog order used
// by the runtime, DSL catalog, and import finalizer.
func SupportedMethods() []string {
	return append([]string(nil), supportedMethods[:]...)
}

// MethodExecutable reports whether the native HTTP transport implements raw.
func MethodExecutable(raw string) bool {
	method := NormalizeMethod(raw)
	for _, supported := range supportedMethods {
		if method == supported {
			return true
		}
	}
	return false
}

// ValidateMethod is the authoritative runtime method validation.
func ValidateMethod(raw string) error {
	method := NormalizeMethod(raw)
	if method == "" {
		return fmt.Errorf("HTTP method is required")
	}
	if !MethodExecutable(method) {
		return fmt.Errorf("unsupported HTTP method %q", method)
	}
	return nil
}

// ResolveMethod uses an explicit method or the case-insensitive action type.
// Unknown types are returned for admission and runtime validation to reject.
func ResolveMethod(explicitMethod, actionType string) string {
	if method := NormalizeMethod(explicitMethod); method != "" {
		return method
	}
	if strings.TrimSpace(actionType) == "" {
		return nethttp.MethodGet
	}
	return NormalizeMethod(actionType)
}

// NormalizeAuthType canonicalizes source aliases while preserving an empty
// value as "unspecified".
func NormalizeAuthType(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "noauth" {
		return AuthTypeNone
	}
	return mode
}

// LookupAuthCapability returns the runtime capability for raw. Unknown modes
// are represented explicitly as unsupported rather than folded into no-auth.
func LookupAuthCapability(raw string) AuthCapability {
	mode := NormalizeAuthType(raw)
	switch mode {
	case "":
		return AuthCapability{Type: "", Support: AuthExecutable, InheritsScriptAuth: true}
	case AuthTypeNone:
		return AuthCapability{Type: AuthTypeNone, Support: AuthExecutable, ExplicitNoAuth: true}
	case AuthTypeInherit:
		// The legacy runtime accepts this spelling as an explicit empty
		// per-action override. Only a nil/omitted action auth truly inherits the
		// script default.
		return AuthCapability{Type: AuthTypeInherit, Support: AuthExecutable, ExplicitNoAuth: true}
	case AuthTypeBasic:
		return AuthCapability{Type: AuthTypeBasic, Support: AuthExecutable, RequiresUsername: true, RequiresPassword: true}
	case AuthTypeBearer:
		return AuthCapability{Type: AuthTypeBearer, Support: AuthExecutable, RequiresToken: true}
	case AuthTypeDigest:
		return AuthCapability{Type: AuthTypeDigest, Support: AuthExecutable, RequiresUsername: true, RequiresPassword: true, ChallengeResponse: true}
	case AuthTypeNTLM, AuthTypeNegotiate, AuthTypeNegotiateAny:
		return AuthCapability{Type: mode, Support: AuthExecutable, RequiresUsername: true, RequiresPassword: true, ChallengeResponse: true}
	case AuthTypeKerberos, AuthTypeAWS, AuthTypeHawk:
		return AuthCapability{Type: mode, Support: AuthReviewOnly}
	default:
		// Importers must retain unknown schemes as review-only evidence instead
		// of treating them as no-auth. The runtime itself still rejects them.
		return AuthCapability{Type: mode, Support: AuthReviewOnly}
	}
}

// AuthTypeExecutable reports whether the native runtime accepts raw.
func AuthTypeExecutable(raw string) bool {
	return LookupAuthCapability(raw).Support == AuthExecutable
}

// ValidateAuthType exposes the runtime's authoritative auth validation and
// preserves its existing error messages.
func ValidateAuthType(raw string) error {
	return validateAuthType(raw)
}

// ValidateTimeoutMS exposes the same request timeout bound enforced by the
// transport. Zero means inherit the configured runtime timeout.
func ValidateTimeoutMS(value int) error {
	maximum := int(MaxConfiguredTimeout / time.Millisecond)
	if value < 0 || value > maximum {
		return fmt.Errorf("http timeout_ms must be between 0 and %d", maximum)
	}
	return nil
}

// ValidateMaxRedirects exposes the same redirect bound enforced by the
// transport. Nil means inherit; zero and -1 disable redirect following,
// matching the existing DSL/config contract.
func ValidateMaxRedirects(value *int) error {
	if value != nil && (*value < -1 || *value > MaxConfiguredRedirects) {
		return fmt.Errorf("http max_redirects must be between -1 and %d", MaxConfiguredRedirects)
	}
	return nil
}

// ValidateAssertions validates both the per-action count and every authored
// definition using the runtime evaluator's exact contract.
func ValidateAssertions(assertions []protocol.Assertion) error {
	if len(assertions) > MaxActionAssertions {
		return fmt.Errorf("http assertions must not exceed %d", MaxActionAssertions)
	}
	for index, assertion := range assertions {
		if err := ValidateAssertionDefinition(assertion); err != nil {
			return fmt.Errorf("http assertions[%d]: %w", index, err)
		}
	}
	return nil
}

// Capabilities returns a detached, deterministic HTTP capability snapshot.
func Capabilities() RuntimeCapabilities {
	auth := []AuthCapability{
		LookupAuthCapability(AuthTypeNone),
		LookupAuthCapability(AuthTypeInherit),
		LookupAuthCapability(AuthTypeBasic),
		LookupAuthCapability(AuthTypeBearer),
		LookupAuthCapability(AuthTypeDigest),
		LookupAuthCapability(AuthTypeNTLM),
		LookupAuthCapability(AuthTypeKerberos),
		LookupAuthCapability(AuthTypeNegotiate),
		LookupAuthCapability(AuthTypeAWS),
		LookupAuthCapability(AuthTypeHawk),
	}
	return RuntimeCapabilities{
		MaxTimeoutMS:   int(MaxConfiguredTimeout / time.Millisecond),
		MaxRedirects:   MaxConfiguredRedirects,
		MaxAssertions:  MaxActionAssertions,
		Methods:        SupportedMethods(),
		Versions:       SupportedVersions(),
		Authentication: auth,
	}
}
