// Package requestimport defines the effective HTTP request IR and DSL projection
// shared by request-oriented importers. Source parsers remain responsible for
// hierarchy, inheritance, and source-template semantics; this package owns the
// common request shape and final runtime-facing decisions.
package requestimport

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// SourceRef is the shared source-provenance shape used by credential bindings.
type SourceRef = credentialpolicy.SourceRef

// Header is one ordered HTTP header occurrence. A disabled occurrence still
// shadows inherited/default map values while contributing no wire bytes.
type Header struct {
	Name    string    `json:"name"`
	Value   string    `json:"value"`
	Enabled *bool     `json:"enabled,omitempty"`
	Source  SourceRef `json:"source,omitempty"`
}

// HeaderList is the authoritative logical occurrence representation for
// imported HTTP headers. It preserves authored occurrence order, duplicates,
// and spelling for review and runtime projection. The Go HTTP transport may
// canonicalize field names and does not promise cross-name wire ordering.
type HeaderList []Header

// Protocol identifies the runtime family that will execute an effective
// request. Empty retains the compatibility behavior of the owning script.
type Protocol string

const (
	ProtocolHTTP     Protocol = "http"
	ProtocolSOAP     Protocol = "soap"
	ProtocolJavaHTTP Protocol = "java_http"
)

// URLEncodingPolicy records how the source adapter produced Target. Target is
// always the already-effective wire value; the finalizer never re-encodes it.
type URLEncodingPolicy string

const (
	URLPolicyUnspecified URLEncodingPolicy = ""
	URLPolicyPreserveRaw URLEncodingPolicy = "preserve_raw"
	URLPolicyEncoded     URLEncodingPolicy = "encoded_components"
)

// Summary returns a detached compatibility map. Header names are compared
// case-insensitively and the last occurrence wins, including its authored
// spelling.
func (headers HeaderList) Summary() map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	spelling := make(map[string]string, len(headers))
	for _, header := range headers {
		if header.Enabled != nil && !*header.Enabled {
			continue
		}
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		identity := strings.ToLower(name)
		if previous, exists := spelling[identity]; exists && previous != name {
			delete(out, previous)
		}
		spelling[identity] = name
		out[name] = header.Value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Has reports whether an active header exists using case-insensitive matching.
func (headers HeaderList) Has(name string) bool {
	name = strings.TrimSpace(name)
	for _, header := range headers {
		if header.Enabled != nil && !*header.Enabled {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(header.Name), name) {
			return true
		}
	}
	return false
}

// HeaderListFromMap provides a deterministic compatibility bridge for sources
// that truly expose only a map. It cannot reconstruct lost duplicate order.
func HeaderListFromMap(headers map[string]string) HeaderList {
	if len(headers) == 0 {
		return nil
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(names[i]))
		right := strings.ToLower(strings.TrimSpace(names[j]))
		if left == right {
			return names[i] < names[j]
		}
		return left < right
	})
	out := make(HeaderList, 0, len(names))
	for _, name := range names {
		out = append(out, Header{Name: name, Value: headers[name]})
	}
	return out
}

// HeaderListFromMapWithDiagnostic is the import-facing bridge for sources that
// expose only a map. The diagnostic makes the unavoidable loss of authored
// order explicit without making the deterministic result non-executable.
func HeaderListFromMapWithDiagnostic(headers map[string]string, location string) (HeaderList, diag.Diagnostic) {
	list := HeaderListFromMap(headers)
	if len(list) == 0 {
		return nil, diag.Diagnostic{}
	}
	return list, diag.Diagnostic{
		Element:  "HTTP headers",
		Detail:   "source_order_unavailable: the source exposed headers as a map; deterministic name order was used",
		Severity: diag.Info,
		Location: strings.TrimSpace(location),
	}
}

// BodyKind identifies the source-neutral body serialization.
type BodyKind string

const (
	BodyNone           BodyKind = ""
	BodyRaw            BodyKind = "raw"
	BodyJSON           BodyKind = "json"
	BodyXML            BodyKind = "xml"
	BodyFormURLEncoded BodyKind = "form_urlencoded"
	BodyMultipart      BodyKind = "multipart"
	BodyGraphQL        BodyKind = "graphql"
	BodyFile           BodyKind = "file"
)

// BodyEncoding controls how Payload is represented in the JSON DSL.
type BodyEncoding string

const (
	BodyEncodingAuto   BodyEncoding = ""
	BodyEncodingText   BodyEncoding = "text"
	BodyEncodingBase64 BodyEncoding = "base64"
)

// Body carries authoritative wire bytes or one structured representation.
// Payload is copied unchanged when present; otherwise the finalizer
// deterministically serializes the selected form, multipart, GraphQL, or file
// representation. It never coerces typed JSON values into strings.
type Body struct {
	Kind         BodyKind       `json:"kind,omitempty"`
	MediaType    string         `json:"media_type,omitempty"`
	Payload      []byte         `json:"payload,omitempty"`
	Encoding     BodyEncoding   `json:"encoding,omitempty"`
	FormFields   []FormField    `json:"form_fields,omitempty"`
	Multipart    *MultipartBody `json:"multipart,omitempty"`
	GraphQL      *GraphQLBody   `json:"graphql,omitempty"`
	FileAssetRef string         `json:"file_asset_ref,omitempty"`
	Assets       []Asset        `json:"assets,omitempty"`
	Source       SourceRef      `json:"source,omitempty"`
}

// FormField is one ordered application/x-www-form-urlencoded occurrence.
// RawName/RawValue, when set, are already percent-encoded wire components and
// are copied verbatim. Otherwise Name/Value are encoded with HTML form rules.
type FormField struct {
	Name      string    `json:"name"`
	Value     string    `json:"value"`
	RawName   string    `json:"raw_name,omitempty"`
	RawValue  string    `json:"raw_value,omitempty"`
	HadEquals *bool     `json:"had_equals,omitempty"`
	Source    SourceRef `json:"source,omitempty"`
}

// MultipartPart is one ordered multipart occurrence. Headers is authoritative
// for the part's logical header occurrences; generated
// Content-Disposition/Content-Type headers are used only when the corresponding
// authored header is absent. MIME serialization may canonicalize names and
// does not promise cross-name wire ordering.
type MultipartPart struct {
	Name         string     `json:"name,omitempty"`
	FileName     string     `json:"file_name,omitempty"`
	ContentType  string     `json:"content_type,omitempty"`
	Headers      HeaderList `json:"headers,omitempty"`
	Data         []byte     `json:"data,omitempty"`
	FileAssetRef string     `json:"file_asset_ref,omitempty"`
	Source       SourceRef  `json:"source,omitempty"`
}

// MultipartBody carries ordered parts and an optional source boundary. A
// missing boundary is synthesized deterministically from candidate identity.
type MultipartBody struct {
	Boundary string          `json:"boundary,omitempty"`
	Parts    []MultipartPart `json:"parts,omitempty"`
}

// GraphQLBody is the structured GraphQL HTTP JSON envelope. Variables and
// Extensions remain raw JSON so scalar types and source key order are not
// coerced by an interface{} round-trip.
type GraphQLBody struct {
	Query         string          `json:"query"`
	OperationName string          `json:"operation_name,omitempty"`
	Variables     json.RawMessage `json:"variables,omitempty"`
	Extensions    json.RawMessage `json:"extensions,omitempty"`
}

// Asset is a governed file referenced by the request body.
type Asset struct {
	Name        string    `json:"name"`
	FileName    string    `json:"file_name,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	Data        []byte    `json:"data,omitempty"`
	StorageRef  string    `json:"storage_ref,omitempty"`
	Source      SourceRef `json:"source,omitempty"`
}

// AuthMode distinguishes absence/inheritance from explicit no-auth.
type AuthMode string

const (
	AuthUnspecified AuthMode = ""
	AuthInherit     AuthMode = AuthMode(httpprotocol.AuthTypeInherit)
	AuthNone        AuthMode = AuthMode(httpprotocol.AuthTypeNone)
	AuthBasic       AuthMode = AuthMode(httpprotocol.AuthTypeBasic)
	AuthBearer      AuthMode = AuthMode(httpprotocol.AuthTypeBearer)
	AuthDigest      AuthMode = AuthMode(httpprotocol.AuthTypeDigest)
	AuthUnsupported AuthMode = "unsupported"
)

// Auth is the effective source auth behavior. SourceMode retains an unsupported
// source name without making it look executable.
type Auth struct {
	Mode       AuthMode  `json:"mode,omitempty"`
	SourceMode string    `json:"source_mode,omitempty"`
	Username   string    `json:"username,omitempty"`
	Password   string    `json:"password,omitempty"`
	Token      string    `json:"token,omitempty"`
	Domain     string    `json:"domain,omitempty"`
	Source     SourceRef `json:"source,omitempty"`
}

// TimeoutMode distinguishes inherited, bounded, and source-unbounded timeout
// semantics.
type TimeoutMode string

const (
	TimeoutInherit   TimeoutMode = ""
	TimeoutBounded   TimeoutMode = "bounded"
	TimeoutUnbounded TimeoutMode = "unbounded"
)

type Timeout struct {
	Mode         TimeoutMode `json:"mode,omitempty"`
	Milliseconds int         `json:"milliseconds,omitempty"`
}

// RedirectMode distinguishes inherited, disabled, limited, and source-unlimited
// redirect behavior.
type RedirectMode string

const (
	RedirectInherit   RedirectMode = ""
	RedirectDisabled  RedirectMode = "disabled"
	RedirectLimited   RedirectMode = "limited"
	RedirectUnlimited RedirectMode = "unlimited"
)

type Redirects struct {
	Mode RedirectMode `json:"mode,omitempty"`
	Max  int          `json:"max,omitempty"`
}

// MissingReference records a source template that could not be bound to an
// executable APEX namespace.
type MissingReference struct {
	Namespace string    `json:"namespace,omitempty"`
	Name      string    `json:"name"`
	Source    SourceRef `json:"source,omitempty"`
}

// Request is one fully resolved effective request.
type Request struct {
	Key               string                `json:"key"`
	CandidateID       string                `json:"candidate_id,omitempty"`
	SourceProfile     string                `json:"source_profile,omitempty"`
	Protocol          Protocol              `json:"protocol,omitempty"`
	Name              string                `json:"name"`
	Method            string                `json:"method"`
	Target            string                `json:"target"`
	BaseURL           string                `json:"base_url,omitempty"`
	BaseURLSource     SourceRef             `json:"base_url_source,omitempty"`
	URLPolicy         URLEncodingPolicy     `json:"url_encoding_policy,omitempty"`
	HTTPVersion       string                `json:"http_version,omitempty"`
	Headers           HeaderList            `json:"headers,omitempty"`
	Body              Body                  `json:"body,omitempty"`
	Auth              Auth                  `json:"auth,omitempty"`
	AuthConfig        *dsl.AuthConfig       `json:"auth_config,omitempty"`
	AuthSource        SourceRef             `json:"auth_source,omitempty"`
	Timeout           Timeout               `json:"timeout,omitempty"`
	Redirects         Redirects             `json:"redirects,omitempty"`
	ThinkTime         dsl.ThinkTimeConfig   `json:"think_time,omitempty"`
	ThinkTimeAfter    bool                  `json:"think_time_after,omitempty"`
	Assertions        []dsl.Assertion       `json:"assertions,omitempty"`
	Extractors        []dsl.Extractor       `json:"extractors,omitempty"`
	SOAP              *dsl.SOAPActionConfig `json:"soap,omitempty"`
	MissingReferences []MissingReference    `json:"missing_references,omitempty"`
	Bindings          []SourceBinding       `json:"bindings,omitempty"`
	Diagnostics       diag.Diagnostics      `json:"diagnostics,omitempty"`
	SourceMappings    []SourceMapping       `json:"source_mappings,omitempty"`
	Metadata          map[string]any        `json:"metadata,omitempty"`
	Source            SourceRef             `json:"source,omitempty"`
	SourceRefs        []SourceRef           `json:"source_refs,omitempty"`
}

// SourceBinding records a source template's runtime binding without retaining
// the source value. Resolved false participates in missing-variable readiness.
type SourceBinding struct {
	Namespace string    `json:"namespace,omitempty"`
	Name      string    `json:"name"`
	Reference string    `json:"reference,omitempty"`
	Resolved  bool      `json:"resolved,omitempty"`
	Source    SourceRef `json:"source,omitempty"`
}

// SourceMapping relates a source construct to its projected DSL surface.
type SourceMapping struct {
	Source SourceRef `json:"source"`
	Target string    `json:"target"`
}

// Response is an optional observed response used by sample/correlation flows.
type Response struct {
	Status  int        `json:"status,omitempty"`
	Headers HeaderList `json:"headers,omitempty"`
	Body    Body       `json:"body,omitempty"`
}

// Exchange combines an effective request with optional observation timing and
// response evidence.
type Exchange struct {
	Request      Request        `json:"request"`
	Response     *Response      `json:"response,omitempty"`
	StartedAt    time.Time      `json:"started_at,omitempty"`
	HasStartedAt bool           `json:"has_started_at,omitempty"`
	Duration     time.Duration  `json:"duration,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}
