// Package aurapayload performs bounded, exact Aura payload edits. It preserves
// unselected source spans and never includes source values in errors.
package aurapayload

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
)

const (
	MaxBodyBytes = 8 << 20
	// Edited base64 output is canonical. An unchanged wrapped representation
	// may retain up to the input ceiling; both remain bounded independently of
	// the 8MiB decoded wire ceiling.
	MaxEncodedOutputBytes  = ((MaxBodyBytes + 2) / 3) * 4
	MaxEncodedInputBytes   = MaxEncodedOutputBytes * 2
	MaxWorkBytes           = 32 << 20
	MaxTokens              = 131072
	MaxDepth               = 64
	MaxJSONStringDepth     = 3
	MaxLeaves              = 8192
	MaxRouteBytes          = 8192
	MaxRouteSegments       = 256
	MaxTemplateOccurrences = 4096
	MaxEdits               = 2048
	DirectJSON             = "direct_json"
	FormMessage            = "url_form_message"
	KeySegment             = "key"
	IndexSegment           = "index"
	JSONStringSegment      = "json_string"
)

type Code string

const (
	InvalidEncoding      Code = "invalid_encoding"
	UnsupportedEncoding  Code = "unsupported_encoding"
	InvalidForm          Code = "invalid_form"
	AmbiguousMessage     Code = "ambiguous_message"
	InvalidJSON          Code = "invalid_json"
	InvalidUTF8          Code = "invalid_utf8"
	InvalidSurrogate     Code = "invalid_surrogate"
	DuplicateKey         Code = "duplicate_key"
	InvalidEnvelope      Code = "invalid_envelope"
	ByteLimit            Code = "byte_limit"
	WorkLimit            Code = "work_limit"
	TokenLimit           Code = "token_limit"
	DepthLimit           Code = "depth_limit"
	JSONStringDepthLimit Code = "json_string_depth_limit"
	LeafLimit            Code = "leaf_limit"
	RouteLimit           Code = "route_limit"
	EditLimit            Code = "edit_limit"
	InvalidRoute         Code = "invalid_route"
	StaleShape           Code = "stale_shape"
	StaleValue           Code = "stale_value"
	OverlappingEdits     Code = "overlapping_edits"
)

const AmbiguousContext Code = "ambiguous_context"

type Error struct {
	Code      Code
	WorkBytes int
}

func (e *Error) Error() string { return "Aura payload: " + string(e.Code) }
func fail(code Code) error     { return &Error{Code: code} }

type Segment struct {
	Kind  string `json:"kind"`
	Key   string `json:"key,omitempty"`
	Index int    `json:"index,omitempty"`
}
type Leaf struct {
	Route           []Segment `json:"route"`
	Key             string    `json:"key"`
	Value           string    `json:"value"`
	ValueType       string    `json:"value_type"`
	DisplayPath     string    `json:"display_path"`
	EncodingLayers  []string  `json:"encoding_layers"`
	JSONStringDepth int       `json:"json_string_depth"`
}
type Inspection struct {
	WorkBytes int    `json:"work_bytes"`
	Codec     string `json:"codec"`
	Shape     string `json:"shape"`
	Leaves    []Leaf `json:"leaves"`
}
type Edit struct {
	Route       []Segment
	Shape       string
	Expected    string
	Replacement string
}

type budget struct{ work, tokens, limit int }

func (b *budget) addWork(n int) error {
	limit := b.limit
	if limit <= 0 {
		limit = MaxWorkBytes
	}
	if n < 0 || n > limit-b.work {
		return fail(WorkLimit)
	}
	b.work += n
	return nil
}
func (b *budget) token() error {
	b.tokens++
	if b.tokens > MaxTokens {
		return fail(TokenLimit)
	}
	return nil
}

type member struct {
	key   string
	value *node
}
type node struct {
	start, end int
	kind       string
	value      string
	members    []member
	elements   []*node
	inner      *document
}
type document struct {
	source string
	root   *node
}
type field struct {
	key, value                             string
	keyStart, keyEnd, valueStart, valueEnd int
	hasEqual                               bool
}
type payload struct {
	sourceBody   string
	wire         string
	encoded      bool
	codec        string
	fields       []field
	messageIndex int
	doc          *document
	inspection   Inspection
	nodes        map[string]*node
	budget       *budget
}

// Inspect returns no partial result on malformed, ambiguous or limited input.
// Nonstring leaves are counted explicitly by their ValueType, but cannot be
// replaced or interpolated as strings. Shape excludes literal leaf values.
func Inspect(body, bodyEncoding string) (Inspection, error) {
	return InspectWithWorkLimit(body, bodyEncoding, MaxWorkBytes)
}

// InspectWithWorkLimit shares a caller's remaining aggregate work budget.
// The per-payload ceiling still applies; zero remaining work fails explicitly.
func InspectWithWorkLimit(body, bodyEncoding string, maxWork int) (Inspection, error) {
	p, err := readPayloadLimit(body, bodyEncoding, maxWork)
	if err != nil {
		return Inspection{}, err
	}
	return p.inspection, nil
}

// Replace validates every route, shape and expected value before changing any
// bytes. Identical or overlapping targets are rejected atomically.
func Replace(body, bodyEncoding string, edits []Edit) (string, error) {
	return ReplaceWithWorkLimit(body, bodyEncoding, edits, MaxWorkBytes)
}

// ReplaceWithWorkLimit bounds inspection and all exact edits together.
func ReplaceWithWorkLimit(body, bodyEncoding string, edits []Edit, maxWork int) (string, error) {
	result, _, err := ReplaceWithWorkAccounting(body, bodyEncoding, edits, maxWork)
	return result, err
}

// ReplaceWithWorkAccounting reports safe work consumed by a successful or
// rejected exact edit, for bounded multi-action apply operations.
func ReplaceWithWorkAccounting(body, bodyEncoding string, edits []Edit, maxWork int) (result string, work int, err error) {
	if len(edits) > MaxEdits {
		return "", 0, fail(EditLimit)
	}
	p, err := readPayloadLimit(body, bodyEncoding, maxWork)
	defer func() {
		if p != nil {
			work = p.budget.work
		} else if typed, ok := err.(*Error); ok {
			work = typed.WorkBytes
		}
		if typed, ok := err.(*Error); ok {
			typed.WorkBytes = work
		}
	}()
	if err != nil {
		return "", 0, err
	}
	replacements := make(map[*node]string, len(edits))
	routes := make([][]Segment, 0, len(edits))
	for _, edit := range edits {
		if edit.Shape != p.inspection.Shape {
			return "", 0, fail(StaleShape)
		}
		key, err := routeKey(edit.Route)
		if err != nil {
			return "", 0, err
		}
		for _, previous := range routes {
			if prefixRoute(previous, edit.Route) || prefixRoute(edit.Route, previous) {
				return "", 0, fail(OverlappingEdits)
			}
		}
		n := p.nodes[key]
		if n == nil || n.kind != "string" || n.inner != nil {
			return "", 0, fail(InvalidRoute)
		}
		if n.value != edit.Expected {
			return "", 0, fail(StaleValue)
		}
		if !utf8.ValidString(edit.Replacement) {
			return "", 0, fail(InvalidUTF8)
		}
		if len(edit.Replacement) > MaxBodyBytes {
			return "", 0, fail(ByteLimit)
		}
		if _, err := quotedSize(edit.Replacement, MaxBodyBytes); err != nil {
			return "", 0, err
		}
		if err := p.budget.addWork(len(edit.Expected) + len(edit.Replacement)); err != nil {
			return "", 0, err
		}
		replacements[n] = edit.Replacement
		routes = append(routes, edit.Route)
	}
	wire, err := p.render(replacements, nil)
	if err != nil {
		return "", 0, err
	}
	result, err = p.encode(wire)
	return result, 0, err
}

// Resolve invokes the callback once per authored string leaf (and once per
// ordinary form value), then escapes the existing containing layers. It
// never reparses inserted values, including template-looking or JSON-looking
// values, and returns independently escaped executable and redacted bodies.
func Resolve(body, bodyEncoding string, resolve func(string) (string, string)) (string, string, error) {
	p, err := readPayload(body, bodyEncoding)
	if err != nil {
		return "", "", err
	}
	if resolve == nil {
		return "", "", fail(InvalidRoute)
	}
	actual, redacted := make(map[*node]string), make(map[*node]string)
	for _, leaf := range p.inspection.Leaves {
		if leaf.ValueType != "string" {
			continue
		}
		key, _ := routeKey(leaf.Route)
		value, safe := resolve(leaf.Value)
		if !utf8.ValidString(value) || !utf8.ValidString(safe) {
			return "", "", fail(InvalidUTF8)
		}
		if len(value) > MaxBodyBytes || len(safe) > MaxBodyBytes {
			return "", "", fail(ByteLimit)
		}
		if err := p.budget.addWork(len(value) + len(safe)); err != nil {
			return "", "", err
		}
		actual[p.nodes[key]], redacted[p.nodes[key]] = value, safe
	}
	type pair struct{ value, safe string }
	form := make(map[int][2]pair)
	contextCount := 0
	for _, f := range p.fields {
		if f.key == "aura.context" {
			contextCount++
		}
	}
	if p.codec == FormMessage {
		for i, f := range p.fields {
			if i == p.messageIndex {
				continue
			}
			key, safeKey := f.key, f.key
			var value, safeValue string
			if f.key == "aura.context" && looksJSONContainer(f.value) {
				value, safeValue, err = resolveJSON(f.value, p.budget, resolve)
				if err != nil {
					return "", "", err
				}
			} else {
				value, safeValue = resolve(f.value)
			}
			if f.key == "aura.context" && contextCount > 1 && (value != f.value || safeValue != f.value) {
				return "", "", fail(AmbiguousContext)
			}
			if !utf8.ValidString(key) || !utf8.ValidString(safeKey) || !utf8.ValidString(value) || !utf8.ValidString(safeValue) {
				return "", "", fail(InvalidUTF8)
			}
			if err := p.budget.addWork(len(key) + len(safeKey) + len(value) + len(safeValue)); err != nil {
				return "", "", err
			}
			form[i] = [2]pair{{key, safeKey}, {value, safeValue}}
		}
	}
	actualFields, safeFields := make(map[int][2]string), make(map[int][2]string)
	for i, pairs := range form {
		actualFields[i] = [2]string{pairs[0].value, pairs[1].value}
		safeFields[i] = [2]string{pairs[0].safe, pairs[1].safe}
	}
	wire, err := p.render(actual, actualFields)
	if err != nil {
		return "", "", err
	}
	safeWire, err := p.render(redacted, safeFields)
	if err != nil {
		return "", "", err
	}
	resolved, err := p.encode(wire)
	if err != nil {
		return "", "", err
	}
	safe, err := p.encode(safeWire)
	return resolved, safe, err
}

func readPayload(body, encoding string) (*payload, error) {
	return readPayloadLimit(body, encoding, MaxWorkBytes)
}

func readPayloadLimit(body, encoding string, maxWork int) (result *payload, err error) {
	if maxWork <= 0 {
		return nil, fail(WorkLimit)
	}
	b := &budget{limit: min(maxWork, MaxWorkBytes)}
	defer func() {
		if typed, ok := err.(*Error); ok {
			typed.WorkBytes = b.work
		}
	}()
	if err := b.addWork(len(body)); err != nil {
		return nil, err
	}
	p := &payload{sourceBody: body, wire: body, messageIndex: -1, budget: b, nodes: make(map[string]*node)}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "text", "utf8", "utf-8":
		if len(body) > MaxBodyBytes {
			return nil, fail(ByteLimit)
		}
	case "base64":
		if len(body) > MaxEncodedInputBytes {
			return nil, fail(ByteLimit)
		}
		decoded, _, err := dsl.DecodeBodyEncoding(body, encoding, MaxBodyBytes)
		if err != nil {
			if err == dsl.ErrBodyEncodingLimit {
				return nil, fail(ByteLimit)
			}
			return nil, fail(InvalidEncoding)
		}
		if err := b.addWork(len(decoded)); err != nil {
			return nil, err
		}
		p.wire, p.encoded = string(decoded), true
	default:
		// Windows-1252 requires a separate source/wire character policy. Opted-in
		// structured edits fail explicitly instead of guessing or falling back.
		return nil, fail(UnsupportedEncoding)
	}
	if !utf8.ValidString(p.wire) {
		return nil, fail(InvalidUTF8)
	}
	source := p.wire
	layers := []string{}
	if p.encoded {
		layers = append(layers, "base64")
	}
	if strings.HasPrefix(strings.TrimSpace(p.wire), "{") {
		p.codec = DirectJSON
	} else {
		p.codec = FormMessage
		if strings.Count(p.wire, "&")+1 > aura.MaxFormFields {
			return nil, fail(WorkLimit)
		}
		start := 0
		for _, raw := range strings.Split(p.wire, "&") {
			if strings.Contains(raw, ";") {
				return nil, fail(InvalidForm)
			}
			keyRaw, valueRaw, hasEqual := strings.Cut(raw, "=")
			key, err := url.QueryUnescape(keyRaw)
			if err != nil {
				return nil, fail(InvalidForm)
			}
			// Structured Aura substitution keeps field names fixed. Name
			// templates could rename a sibling into message or aura.context.
			if strings.Contains(key, "{{") {
				return nil, fail(InvalidForm)
			}
			value, err := url.QueryUnescape(valueRaw)
			if err != nil {
				return nil, fail(InvalidForm)
			}
			if !utf8.ValidString(key) || !utf8.ValidString(value) {
				return nil, fail(InvalidUTF8)
			}
			if err := b.addWork(len(key) + len(value)); err != nil {
				return nil, err
			}
			f := field{key: key, value: value, keyStart: start, keyEnd: start + len(keyRaw), valueStart: start + len(keyRaw), valueEnd: start + len(raw), hasEqual: hasEqual}
			if hasEqual {
				f.valueStart++
			}
			if key == "message" {
				if p.messageIndex >= 0 {
					return nil, fail(AmbiguousMessage)
				}
				p.messageIndex = len(p.fields)
				source = value
			}
			p.fields = append(p.fields, f)
			start += len(raw) + 1
		}
		if p.messageIndex < 0 {
			return nil, fail(AmbiguousMessage)
		}
		layers = append(layers, "url_form")
	}
	layers = append(layers, "json")
	doc, err := parseDocument(source, b)
	if err != nil {
		return nil, err
	}
	p.doc = doc
	if err := b.addWork(len(p.wire)); err != nil {
		return nil, err
	}
	if !aura.ValidateRequest([]byte(p.wire)).Success {
		return nil, fail(InvalidEnvelope)
	}
	p.inspection.Codec = p.codec
	if err := p.collect(doc.root, nil, "", "$", layers, 0, 0); err != nil {
		return nil, err
	}
	h := sha256.New()
	_, _ = h.Write([]byte("aurapayload-v1|" + encodingName(p.encoded) + "|" + p.codec + "|"))
	for _, f := range p.fields {
		_, _ = h.Write([]byte(strconv.Itoa(len(f.key)) + ":" + f.key + "=" + strconv.FormatBool(f.hasEqual) + ";"))
	}
	write := func(data []byte) (int, error) {
		if err := b.addWork(len(data)); err != nil {
			return 0, err
		}
		return h.Write(data)
	}
	if err := shapeNode(write, p.doc.root); err != nil {
		return nil, err
	}
	p.inspection.Shape = hex.EncodeToString(h.Sum(nil))
	p.inspection.WorkBytes = b.work
	return p, nil
}
func encodingName(encoded bool) string {
	if encoded {
		return "base64"
	}
	return "text"
}

func (p *payload) collect(n *node, route []Segment, key, path string, layers []string, stringDepth, depth int) error {
	if depth > MaxDepth {
		return fail(DepthLimit)
	}
	if len(route) > MaxRouteSegments || len(path) > MaxRouteBytes {
		return fail(RouteLimit)
	}
	// Include route/path construction work, not just decoded source bytes.
	if err := p.budget.addWork(len(path) + len(route)*32); err != nil {
		return err
	}
	switch n.kind {
	case "object":
		members := append([]member(nil), n.members...)
		sort.Slice(members, func(i, j int) bool { return members[i].key < members[j].key })
		for _, m := range members {
			if _, err := quotedSize(m.key, MaxRouteBytes); err != nil {
				return fail(RouteLimit)
			}
			next := appendRoute(route, Segment{Kind: KeySegment, Key: m.key})
			if err := p.collect(m.value, next, m.key, path+displayKey(m.key), layers, stringDepth, depth+1); err != nil {
				return err
			}
		}
	case "array":
		for i, child := range n.elements {
			next := appendRoute(route, Segment{Kind: IndexSegment, Index: i})
			if err := p.collect(child, next, key, path+"["+strconv.Itoa(i)+"]", layers, stringDepth, depth+1); err != nil {
				return err
			}
		}
	case "string":
		if looksJSONContainer(n.value) {
			if stringDepth >= MaxJSONStringDepth {
				return fail(JSONStringDepthLimit)
			}
			inner, err := parseDocument(n.value, p.budget)
			if err != nil {
				return err
			}
			n.inner = inner
			next := appendRoute(route, Segment{Kind: JSONStringSegment})
			nextLayers := append(append([]string(nil), layers...), "json_string")
			return p.collect(inner.root, next, key, path+"::json", nextLayers, stringDepth+1, depth+1)
		}
		fallthrough
	default:
		if len(p.inspection.Leaves) >= MaxLeaves {
			return fail(LeafLimit)
		}
		routeID, err := routeKey(route)
		if err != nil {
			return err
		}
		if len(path) > MaxRouteBytes {
			return fail(RouteLimit)
		}
		if err := p.budget.addWork(len(routeID)); err != nil {
			return err
		}
		p.nodes[routeID] = n
		p.inspection.Leaves = append(p.inspection.Leaves, Leaf{Route: append([]Segment(nil), route...), Key: key, Value: n.value, ValueType: n.kind, DisplayPath: path, EncodingLayers: append([]string(nil), layers...), JSONStringDepth: stringDepth})
	}
	return nil
}
func appendRoute(route []Segment, next Segment) []Segment {
	result := make([]Segment, len(route)+1)
	copy(result, route)
	result[len(route)] = next
	return result
}
func displayKey(key string) string {
	plain := key != ""
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c >= '0' && c <= '9' && i > 0) {
			plain = false
			break
		}
	}
	if plain {
		return "." + key
	}
	data, _ := json.Marshal(key)
	return "[" + string(data) + "]"
}
func routeKey(route []Segment) (string, error) {
	if len(route) == 0 || len(route) > MaxRouteSegments {
		return "", fail(InvalidRoute)
	}
	for _, s := range route {
		switch s.Kind {
		case KeySegment:
			if s.Index != 0 {
				return "", fail(InvalidRoute)
			}
		case IndexSegment:
			if s.Key != "" || s.Index < 0 {
				return "", fail(InvalidRoute)
			}
		case JSONStringSegment:
			if s.Key != "" || s.Index != 0 {
				return "", fail(InvalidRoute)
			}
		default:
			return "", fail(InvalidRoute)
		}
	}
	bound := 2
	for _, s := range route {
		size, err := quotedSize(s.Key, MaxRouteBytes)
		if err != nil {
			return "", fail(RouteLimit)
		}
		bound += size + len(s.Kind) + 50
		if bound > MaxRouteBytes {
			return "", fail(RouteLimit)
		}
	}
	data, _ := json.Marshal(route)
	if len(data) > MaxRouteBytes {
		return "", fail(RouteLimit)
	}
	return string(data), nil
}
func prefixRoute(a, b []Segment) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func shapeNode(write func([]byte) (int, error), n *node) error {
	if _, err := write([]byte(n.kind + "(")); err != nil {
		return err
	}
	members := append([]member(nil), n.members...)
	sort.Slice(members, func(i, j int) bool { return members[i].key < members[j].key })
	for _, m := range members {
		if _, err := write([]byte(strconv.Itoa(len(m.key)) + ":" + m.key)); err != nil {
			return err
		}
		if err := shapeNode(write, m.value); err != nil {
			return err
		}
	}
	for _, child := range n.elements {
		if err := shapeNode(write, child); err != nil {
			return err
		}
	}
	if n.inner != nil {
		if _, err := write([]byte("json_string:")); err != nil {
			return err
		}
		if err := shapeNode(write, n.inner.root); err != nil {
			return err
		}
	}
	_, err := write([]byte(");"))
	return err
}

func (p *payload) encode(wire string) (string, error) {
	if wire == p.wire {
		return p.sourceBody, nil
	}
	if len(wire) > MaxBodyBytes {
		return "", fail(ByteLimit)
	}
	if p.encoded {
		if err := p.budget.addWork(base64.StdEncoding.EncodedLen(len(wire))); err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString([]byte(wire)), nil
	}
	return wire, nil
}

type patch struct {
	start, end int
	value      string
}

func (p *payload) render(replacements map[*node]string, fields map[int][2]string) (string, error) {
	message, err := renderDocument(p.doc, replacements, p.budget)
	if err != nil {
		return "", err
	}
	if p.codec == DirectJSON {
		return message, nil
	}
	patches := make([]patch, 0, len(fields)*2+1)
	for i, f := range p.fields {
		if i == p.messageIndex {
			if message != f.value {
				encoded, err := escapeForm(message, p.budget)
				if err != nil {
					return "", err
				}
				patches = append(patches, patch{f.valueStart, f.valueEnd, encoded})
			}
		} else if values, ok := fields[i]; ok {
			if values[0] != f.key {
				encoded, err := escapeForm(values[0], p.budget)
				if err != nil {
					return "", err
				}
				patches = append(patches, patch{f.keyStart, f.keyEnd, encoded})
			}
			if values[1] != f.value {
				encoded, err := escapeForm(values[1], p.budget)
				if err != nil {
					return "", err
				}
				if !f.hasEqual {
					encoded = "=" + encoded
				}
				patches = append(patches, patch{f.valueStart, f.valueEnd, encoded})
			}
		}
	}
	return applyPatches(p.wire, patches, p.budget)
}
func escapeForm(value string, b *budget) (string, error) {
	size := 0
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' || c == ' ' {
			size++
		} else {
			size += 3
		}
		if size > MaxBodyBytes {
			return "", fail(ByteLimit)
		}
	}
	if err := b.addWork(size); err != nil {
		return "", err
	}
	return url.QueryEscape(value), nil
}
func renderDocument(doc *document, replacements map[*node]string, b *budget) (string, error) {
	patches := make([]patch, 0)
	var walk func(*node) error
	walk = func(n *node) error {
		if n.inner != nil {
			value, err := renderDocument(n.inner, replacements, b)
			if err != nil {
				return err
			}
			if value != n.value {
				quoted, err := quoteBounded(value, b)
				if err != nil {
					return err
				}
				patches = append(patches, patch{n.start, n.end, quoted})
			}
			return nil
		}
		if value, ok := replacements[n]; ok && value != n.value {
			quoted, err := quoteBounded(value, b)
			if err != nil {
				return err
			}
			patches = append(patches, patch{n.start, n.end, quoted})
			return nil
		}
		for _, m := range n.members {
			if err := walk(m.value); err != nil {
				return err
			}
		}
		for _, child := range n.elements {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(doc.root); err != nil {
		return "", err
	}
	return applyPatches(doc.source, patches, b)
}
func applyPatches(source string, patches []patch, b *budget) (string, error) {
	if len(patches) == 0 {
		return source, nil
	}
	sort.Slice(patches, func(i, j int) bool { return patches[i].start < patches[j].start })
	size, last := len(source), 0
	for _, p := range patches {
		if p.start < last || p.end < p.start || p.end > len(source) {
			return "", fail(OverlappingEdits)
		}
		size += len(p.value) - (p.end - p.start)
		if size > MaxBodyBytes {
			return "", fail(ByteLimit)
		}
		last = p.end
	}
	if err := b.addWork(size); err != nil {
		return "", err
	}
	var out strings.Builder
	out.Grow(size)
	last = 0
	for _, p := range patches {
		out.WriteString(source[last:p.start])
		out.WriteString(p.value)
		last = p.end
	}
	out.WriteString(source[last:])
	return out.String(), nil
}

func looksJSONContainer(value string) bool {
	trimmed := strings.TrimSpace(value)
	// Disclosure replaces credential leaves with this fixed literal. It is
	// authored data, not an encoded JSON array, and must keep sibling routes
	// inspectable for the guided credential workflow.
	if trimmed == credential.Redacted {
		return false
	}
	return (strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "{{")) || strings.HasPrefix(trimmed, "[")
}

func resolveJSON(source string, b *budget, resolve func(string) (string, string)) (string, string, error) {
	doc, err := parseDocument(source, b)
	if err != nil {
		return "", "", err
	}
	q := &payload{doc: doc, budget: b, nodes: make(map[string]*node)}
	if err := q.collect(doc.root, nil, "", "$", []string{"json"}, 0, 0); err != nil {
		return "", "", err
	}
	actual, safe := make(map[*node]string), make(map[*node]string)
	for _, leaf := range q.inspection.Leaves {
		if leaf.ValueType != "string" {
			continue
		}
		key, _ := routeKey(leaf.Route)
		value, redacted := resolve(leaf.Value)
		if !utf8.ValidString(value) || !utf8.ValidString(redacted) {
			return "", "", fail(InvalidUTF8)
		}
		if err := b.addWork(len(value) + len(redacted)); err != nil {
			return "", "", err
		}
		actual[q.nodes[key]], safe[q.nodes[key]] = value, redacted
	}
	value, err := renderDocument(doc, actual, b)
	if err != nil {
		return "", "", err
	}
	redacted, err := renderDocument(doc, safe, b)
	return value, redacted, err
}

// quotedSize mirrors encoding/json string escaping without allocating the
// escaped value first. The bound applies to the eventual quoted JSON bytes.
func quotedSize(value string, limit int) (int, error) {
	if !utf8.ValidString(value) {
		return 0, fail(InvalidUTF8)
	}
	size := 2
	for _, r := range value {
		switch {
		case r == '"' || r == '\\' || r == '\b' || r == '\f' || r == '\n' || r == '\r' || r == '\t':
			size += 2
		case r < 0x20 || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029':
			size += 6
		default:
			size += utf8.RuneLen(r)
		}
		if size > limit {
			return 0, fail(ByteLimit)
		}
	}
	if size > limit {
		return 0, fail(ByteLimit)
	}
	return size, nil
}
func quoteBounded(value string, b *budget) (string, error) {
	size, err := quotedSize(value, MaxBodyBytes)
	if err != nil {
		return "", err
	}
	if err := b.addWork(size); err != nil {
		return "", err
	}
	data, _ := json.Marshal(value)
	return string(data), nil
}
