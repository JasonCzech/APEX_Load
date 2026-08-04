package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jmespath/go-jmespath"
	xhtml "golang.org/x/net/html"
)

const (
	maxCompiledExtractorCacheEntries  = 1024
	maxCompiledExtractorCacheKeyBytes = 4 << 20
	// MaxExtractorExpressionBytes bounds both parser/compiler work and the size
	// of keys admitted to the shared compiled-expression caches. It is exported
	// so DSL validation can reject an oversized HTTP extractor before execution;
	// the runtime check remains authoritative for direct protocol callers.
	MaxExtractorExpressionBytes = 64 << 10
)

type compiledExtractorCache[T any] struct {
	mu          sync.Mutex
	items       map[string]T
	order       []string
	keyBytes    int
	maxKeyBytes int
}

func (c *compiledExtractorCache[T]) get(key string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.items[key]
	return value, ok
}

// Load preserves the small sync.Map-style test seam used by cache behavior
// tests while the production cache remains bounded.
func (c *compiledExtractorCache[T]) Load(key string) (T, bool) {
	return c.get(key)
}

func (c *compiledExtractorCache[T]) put(key string, value T) T {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.items[key]; ok {
		return existing
	}
	if c.items == nil {
		c.items = make(map[string]T)
	}
	keyLimit := c.maxKeyBytes
	if keyLimit <= 0 {
		keyLimit = maxCompiledExtractorCacheKeyBytes
	}
	// A single key larger than the whole cache budget is safe to compile for the
	// current request but must never become process-retained state.
	if len(key) > keyLimit {
		return value
	}
	for len(c.order) > 0 && (len(c.items) >= maxCompiledExtractorCacheEntries || c.keyBytes > keyLimit-len(key)) {
		evicted := c.order[0]
		delete(c.items, evicted)
		c.order = c.order[1:]
		c.keyBytes -= len(evicted)
	}
	c.items[key] = value
	c.order = append(c.order, key)
	c.keyBytes += len(key)
	return value
}

var compiledRegexCache compiledExtractorCache[*regexp.Regexp]
var compiledXPathCache compiledExtractorCache[*xpath.Expr]
var compiledJMESPathCache compiledExtractorCache[*jmespath.JMESPath]

// compileRegexCached returns a shared compiled regexp for expr, compiling it on
// the first request and reusing the result thereafter. Compile errors are not
// cached: an invalid pattern surfaces the same error on every call, exactly as
// regexp.Compile did before.
func compileRegexCached(expr string) (*regexp.Regexp, error) {
	if err := validateExtractorExpressionSize(expr); err != nil {
		return nil, err
	}
	if cached, ok := compiledRegexCache.get(expr); ok {
		return cached, nil
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	return compiledRegexCache.put(expr, re), nil
}

func compileJMESPathCached(expr string) (*jmespath.JMESPath, error) {
	if err := validateExtractorExpressionSize(expr); err != nil {
		return nil, err
	}
	if cached, ok := compiledJMESPathCache.get(expr); ok {
		return cached, nil
	}
	compiled, err := jmespath.Compile(expr)
	if err != nil {
		return nil, err
	}
	return compiledJMESPathCache.put(expr, compiled), nil
}

const xpathFragmentExpressionPrefix = "__apexload_xpath_fragment__:"

const xpathExpressionConfigPrefix = "__apexload_xpath_config__:"

const regexOrdinalExpressionConfigPrefix = "__apexload_regex_ordinal__:"

const boundaryExpressionConfigPrefix = "__apexload_boundary_config__:"

const (
	// One result also needs room for the synthetic name_count entry. The
	// protocol-level StoreExtraction check remains authoritative when several
	// extractors share the same result budget.
	maxAllExtractorMatches = protocol.MaxExtractionEntries - 1
	maxAllExtractorBytes   = protocol.MaxExtractionBytes
	// Tree-building extractors must never turn a response with many tiny
	// structural tokens into an unbounded object graph. These limits are checked
	// in a streaming preflight before JSON/XML/HTML tree construction.
	maxExtractorStructuredNodes      = 100_000
	maxExtractorStructuredDepth      = 256
	maxExtractorAttributesPerElement = 4_096
	// Conversion happens before protocol.StoreExtraction enforces the durable
	// 64 KiB result ceiling. Bound the pre-conversion value too so a response can
	// never make url/html decoding or QueryEscape allocate from a multi-megabyte
	// capture that cannot be retained anyway. The larger input allowance still
	// permits encoded entities to contract below the result ceiling.
	maxExtractorTransformInputBytes = 4 << 20
	// Bound aggregate logical input work per action. Every body extractor and
	// every scanning header extractor independently walks or parses its input,
	// so each pass is charged even though the response bytes are already
	// buffered. The limit permits two passes over a maximum-sized body (or a
	// body plus an equally large flattened-header surface) while preventing a
	// large extractor list from multiplying one response into gigabytes of CPU
	// and parser-allocation work.
	maxExtractorInputWorkBytes int64 = 2 * hardHTTPMaxBodyBytes
)

var errExtractorResourceLimit = errors.New("extractor resource limit exceeded")

type extractorInputWorkBudget struct {
	used              int64
	limit             int64
	bodyBytes         int64
	headerBytes       int64
	directHeaderBytes map[string]int64
}

func newExtractorInputWorkBudget(headers http.Header, body []byte) *extractorInputWorkBudget {
	directHeaderBytes := make(map[string]int64, len(headers))
	for key := range headers {
		var total int64
		for _, value := range headers.Values(key) {
			total += int64(len(value))
		}
		directHeaderBytes[strings.ToLower(key)] = total
	}
	return &extractorInputWorkBudget{
		limit:             maxExtractorInputWorkBytes,
		bodyBytes:         int64(len(body)),
		headerBytes:       flattenedHeadersByteSize(headers),
		directHeaderBytes: directHeaderBytes,
	}
}

func (budget *extractorInputWorkBudget) charge(ext protocol.Extractor) error {
	if budget == nil {
		return fmt.Errorf("%w: extractor input budget is unavailable", errExtractorResourceLimit)
	}
	if err := validateExtractorExpressionSize(ext.Expression); err != nil {
		return err
	}
	var cost int64
	switch ext.Source {
	case "", "body":
		// Every supported body extractor performs its own scan or parse.
		cost = budget.bodyBytes
		switch ext.Type {
		case "jsonpath", "json_path", "jmespath", "css":
			// Structured extraction performs one streaming shape preflight and
			// one tree-building/query pass. Charge both full scans.
			if cost > budget.limit/2 {
				return fmt.Errorf("%w: aggregate extractor input exceeds %d bytes", errExtractorResourceLimit, budget.limit)
			}
			cost *= 2
		case "xpath":
			// XPath's format-safe path may make four passes: a lexical XML
			// attribute check, strict XML preflight, HTML fallback preflight,
			// and the final XML/HTML tree construction. Charge the worst case
			// up front so fallback cannot multiply parser work beyond budget.
			if cost > budget.limit/4 {
				return fmt.Errorf("%w: aggregate extractor input exceeds %d bytes", errExtractorResourceLimit, budget.limit)
			}
			cost *= 4
		}
	case "header":
		// Direct MIME-header lookup is bounded by the map implementation. Regex,
		// and boundary paths flatten and scan the whole header surface
		// independently. Direct save-all only walks values for its named header.
		if ext.Mode == "all" && (ext.Type == "" || ext.Type == "header") {
			if budget.directHeaderBytes == nil {
				// Preserve conservative behavior for manually constructed test
				// budgets and direct internal callers without the header index.
				cost = budget.headerBytes
			} else {
				cost = budget.directHeaderBytes[strings.ToLower(ext.Expression)]
			}
		} else if ext.Type != "" && ext.Type != "header" {
			cost = budget.headerBytes
		}
	default:
		// Unsupported sources fail without inspecting either response surface.
		return nil
	}
	if cost < 0 || cost > budget.limit-budget.used {
		return fmt.Errorf("%w: aggregate extractor input exceeds %d bytes", errExtractorResourceLimit, budget.limit)
	}
	budget.used += cost
	return nil
}

func validateExtractorExpressionSize(expression string) error {
	if len(expression) > MaxExtractorExpressionBytes {
		return fmt.Errorf("%w: extractor expression exceeds %d bytes", errExtractorResourceLimit, MaxExtractorExpressionBytes)
	}
	return nil
}

func flattenedHeadersByteSize(headers http.Header) int64 {
	var total int64
	for key := range headers {
		for _, value := range headers.Values(key) {
			// flattenHeaders emits "<key>: <value>\n" for every occurrence.
			total += int64(len(key) + len(value) + 3)
		}
	}
	return total
}

type xpathExpressionConfig struct {
	Expression string            `json:"expression"`
	Fragment   bool              `json:"fragment,omitempty"`
	Namespaces map[string]string `json:"namespaces,omitempty"`
}

type regexOrdinalExpressionConfig struct {
	Expression string `json:"expression"`
	Ordinal    int    `json:"ordinal"`
}

type boundaryExpressionConfig struct {
	Left            string `json:"left"`
	Right           string `json:"right"`
	Ordinal         int    `json:"ordinal,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
}

func extract(ext protocol.Extractor, headers http.Header, body []byte) (string, error) {
	value, err := extractRaw(ext, headers, body)
	if err != nil {
		return "", err
	}
	return postProcessExtractedValue(ext, value)
}

func extractRaw(ext protocol.Extractor, headers http.Header, body []byte) (string, error) {
	switch ext.Source {
	case "", "body":
		return extractFromBody(ext, body)
	case "header":
		return extractFromHeaders(ext, headers)
	default:
		return "", fmt.Errorf("unsupported extraction source: %s", ext.Source)
	}
}

// extractAll returns every match for a save-all (mode "all") extractor, with
// per-value post-processing applied. Zero matches return an empty slice and no
// error; the caller decides whether that counts as a miss.
func extractAll(ext protocol.Extractor, headers http.Header, body []byte) ([]string, error) {
	content := body
	var values []string
	directHeaderLookup := false
	switch ext.Source {
	case "", "body":
	case "header":
		if ext.Type == "" || ext.Type == "header" {
			directHeaderLookup = true
			values = append(values, headers.Values(ext.Expression)...)
			break
		}
		content = []byte(flattenHeaders(headers))
	default:
		return nil, fmt.Errorf("unsupported extraction source: %s", ext.Source)
	}

	var err error
	if !directHeaderLookup {
		switch ext.Type {
		case "regex":
			values, err = extractAllRegex(ext.Expression, content)
		case "boundary":
			values, err = extractAllBoundary(ext.Expression, content)
		case "jsonpath", "json_path":
			values, err = extractAllJSONPath(ext.Expression, content)
		case "xpath":
			values, err = extractAllXPath(ext.Expression, content)
		default:
			return nil, fmt.Errorf("extraction type %q does not support mode \"all\"", ext.Type)
		}
	}
	if err != nil {
		return nil, err
	}
	for i, value := range values {
		processed, err := postProcessExtractedValue(ext, value)
		if err != nil {
			return nil, err
		}
		values[i] = processed
	}
	if len(values) > maxAllExtractorMatches {
		return nil, fmt.Errorf("%w: all extractor matches exceed %d", errExtractorResourceLimit, maxAllExtractorMatches)
	}
	var totalBytes int64
	for _, value := range values {
		totalBytes += int64(len(value))
		if totalBytes > maxAllExtractorBytes {
			return nil, fmt.Errorf("%w: all extractor output exceeds %d bytes", errExtractorResourceLimit, maxAllExtractorBytes)
		}
	}
	return values, nil
}

// postProcessExtractedValue applies the optional save_offset / save_length /
// convert transforms to one matched value.
func postProcessExtractedValue(ext protocol.Extractor, value string) (string, error) {
	if ext.SaveOffset > 0 {
		if ext.SaveOffset >= len(value) {
			value = ""
		} else {
			value = value[ext.SaveOffset:]
		}
	}
	if ext.SaveLength > 0 && ext.SaveLength < len(value) {
		value = value[:ext.SaveLength]
	}
	if len(value) > maxExtractorTransformInputBytes {
		return "", fmt.Errorf("%w: extractor transform input exceeds %d bytes", errExtractorResourceLimit, maxExtractorTransformInputBytes)
	}
	var processed string
	switch ext.Convert {
	case "":
		if len(value) > maxAllExtractorBytes {
			return "", fmt.Errorf("%w: extractor output exceeds %d bytes", errExtractorResourceLimit, maxAllExtractorBytes)
		}
		processed = value
	case "url_encode":
		// Query escaping never contracts input. Reject an impossible-to-store
		// capture before QueryEscape can allocate up to three times its size.
		if len(value) > maxAllExtractorBytes {
			return "", fmt.Errorf("%w: extractor output exceeds %d bytes", errExtractorResourceLimit, maxAllExtractorBytes)
		}
		processed = url.QueryEscape(value)
	case "url_decode":
		decoded, err := url.QueryUnescape(value)
		if err != nil {
			processed = value
		} else {
			processed = decoded
		}
	case "html_decode":
		processed = html.UnescapeString(value)
	case "html_url_encode":
		// HTML-format data to URL format: decode entities first, then
		// percent-encode with %20 (not form-style '+') for spaces.
		decoded := html.UnescapeString(value)
		if len(decoded) > maxAllExtractorBytes {
			return "", fmt.Errorf("%w: extractor output exceeds %d bytes", errExtractorResourceLimit, maxAllExtractorBytes)
		}
		encoded := url.QueryEscape(decoded)
		processed = strings.ReplaceAll(encoded, "+", "%20")
	default:
		return "", fmt.Errorf("unsupported extractor convert mode: %s", ext.Convert)
	}
	if len(processed) > maxAllExtractorBytes {
		return "", fmt.Errorf("%w: extractor output exceeds %d bytes", errExtractorResourceLimit, maxAllExtractorBytes)
	}
	return processed, nil
}

func extractFromHeaders(ext protocol.Extractor, headers http.Header) (string, error) {
	switch ext.Type {
	case "", "header":
		v := headers.Get(ext.Expression)
		if v == "" {
			return "", fmt.Errorf("header %q not found", ext.Expression)
		}
		return v, nil
	case "regex":
		return extractRegex(ext.Expression, []byte(flattenHeaders(headers)))
	case "boundary":
		return extractBoundary(ext.Expression, []byte(flattenHeaders(headers)))
	default:
		return "", fmt.Errorf("unsupported header extraction type: %s", ext.Type)
	}
}

func flattenHeaders(headers http.Header) string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		for _, value := range headers.Values(key) {
			builder.WriteString(key)
			builder.WriteString(": ")
			builder.WriteString(value)
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

func extractFromBody(ext protocol.Extractor, body []byte) (string, error) {
	switch ext.Type {
	case "jsonpath", "json_path":
		return extractJSONPath(ext.Expression, body)
	case "jmespath":
		return extractJMESPath(ext.Expression, body)
	case "css":
		return extractCSS(ext.Expression, body)
	case "xpath":
		return extractXPath(ext.Expression, body)
	case "regex":
		return extractRegex(ext.Expression, body)
	case "regex_count":
		return extractRegexCount(ext.Expression, body)
	case "regex_ordinal":
		return extractRegexOrdinal(ext.Expression, body)
	case "boundary":
		return extractBoundary(ext.Expression, body)
	case "sse_concat":
		return extractSSEConcat(ext.Expression, body)
	case "sse_event":
		return extractSSEEvent(ext.Expression, body)
	default:
		return "", fmt.Errorf("unsupported extraction type: %s", ext.Type)
	}
}

type structuredTreeBudget struct {
	nodes int
	depth int
}

func (budget *structuredTreeBudget) addNode(format string) error {
	return budget.addNodes(1, format)
}

func (budget *structuredTreeBudget) addNodes(count int, format string) error {
	if count < 0 || count > maxExtractorStructuredNodes-budget.nodes {
		return fmt.Errorf("%w: %s structure exceeds %d nodes", errExtractorResourceLimit, format, maxExtractorStructuredNodes)
	}
	budget.nodes += count
	return nil
}

func (budget *structuredTreeBudget) push(format string) error {
	budget.depth++
	if budget.depth > maxExtractorStructuredDepth {
		return fmt.Errorf("%w: %s structure exceeds depth %d", errExtractorResourceLimit, format, maxExtractorStructuredDepth)
	}
	return nil
}

func (budget *structuredTreeBudget) pop() {
	if budget.depth > 0 {
		budget.depth--
	}
}

// validateExtractorJSONStructure performs a token-only pass before json.Decode
// materializes maps and slices. Object-key strings are counted too because they
// are independently retained allocations in the decoded map.
func validateExtractorJSONStructure(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var budget structuredTreeBudget
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("invalid JSON body: %w", err)
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{', '[':
				if err := budget.addNode("JSON"); err != nil {
					return err
				}
				if err := budget.push("JSON"); err != nil {
					return err
				}
			case '}', ']':
				budget.pop()
			}
			continue
		}
		if err := budget.addNode("JSON"); err != nil {
			return err
		}
	}
}

// validateExtractorXMLStructure performs a strict streaming XML pass. Syntax
// errors are returned without the resource-limit sentinel so ordinary HTML can
// retain the historical XPath fallback; resource-limit errors always fail
// closed and can never be reinterpreted by the more forgiving HTML parser.
func validateExtractorXMLStructure(body []byte) error {
	if err := validateExtractorXMLAttributeCount(body); err != nil {
		return err
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	var budget structuredTreeBudget
	rootElements := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if rootElements != 1 {
				return fmt.Errorf("XML payload must contain exactly one root element")
			}
			return nil
		}
		if err != nil {
			return err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if budget.depth == 0 {
				rootElements++
				if rootElements > 1 {
					return fmt.Errorf("XML payload must contain exactly one root element")
				}
			}
			if err := budget.addNode("XML"); err != nil {
				return err
			}
			if err := budget.push("XML"); err != nil {
				return err
			}
			if len(typed.Attr) > maxExtractorAttributesPerElement {
				return fmt.Errorf("%w: XML element exceeds %d attributes", errExtractorResourceLimit, maxExtractorAttributesPerElement)
			}
			if err := budget.addNodes(len(typed.Attr), "XML"); err != nil {
				return err
			}
		case xml.EndElement:
			budget.pop()
		case xml.CharData:
			if budget.depth == 0 {
				if len(bytes.TrimSpace(typed)) > 0 {
					return fmt.Errorf("XML payload contains text outside the root element")
				}
				continue
			}
			if len(typed) > 0 {
				if err := budget.addNode("XML"); err != nil {
					return err
				}
			}
		case xml.Comment, xml.Directive, xml.ProcInst:
			if err := budget.addNode("XML"); err != nil {
				return err
			}
		}
	}
}

// encoding/xml materializes a complete []xml.Attr for each start tag before
// returning the token. Count attributes with the streaming HTML tokenizer
// first so one enormous XML tag cannot allocate an unbounded attribute slice
// before the ordinary XML preflight gets a chance to enforce its ceiling.
func validateExtractorXMLAttributeCount(body []byte) error {
	tokenizer := xhtml.NewTokenizer(bytes.NewReader(body))
	for {
		tokenType := tokenizer.Next()
		switch tokenType {
		case xhtml.ErrorToken:
			if err := tokenizer.Err(); err != nil && err != io.EOF {
				return fmt.Errorf("invalid XML body: %w", err)
			}
			return nil
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			_, hasAttribute := tokenizer.TagName()
			attributeCount := 0
			for hasAttribute {
				_, _, hasAttribute = tokenizer.TagAttr()
				attributeCount++
				if attributeCount > maxExtractorAttributesPerElement {
					return fmt.Errorf("%w: XML element exceeds %d attributes", errExtractorResourceLimit, maxExtractorAttributesPerElement)
				}
			}
		}
	}
}

var htmlVoidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

var htmlOptionalCloseOnSameStart = map[string]bool{
	"dd": true, "dt": true, "li": true, "option": true, "optgroup": true,
	"p": true, "rt": true, "rp": true, "thead": true, "tbody": true,
	"tfoot": true, "tr": true, "td": true, "th": true,
}

func validateExtractorHTMLStructure(body []byte) error {
	tokenizer := xhtml.NewTokenizer(bytes.NewReader(body))
	var budget structuredTreeBudget
	stack := make([]string, 0, 16)
	for {
		tokenType := tokenizer.Next()
		switch tokenType {
		case xhtml.ErrorToken:
			if err := tokenizer.Err(); err != nil && err != io.EOF {
				return fmt.Errorf("invalid HTML body: %w", err)
			}
			return nil
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			nameBytes, hasAttribute := tokenizer.TagName()
			name := strings.ToLower(string(nameBytes))
			attributeCount := 0
			for hasAttribute {
				_, _, hasAttribute = tokenizer.TagAttr()
				attributeCount++
				if attributeCount > maxExtractorAttributesPerElement {
					return fmt.Errorf("%w: HTML element exceeds %d attributes", errExtractorResourceLimit, maxExtractorAttributesPerElement)
				}
			}
			if err := budget.addNode("HTML"); err != nil {
				return err
			}
			if err := budget.addNodes(attributeCount, "HTML"); err != nil {
				return err
			}
			// In HTML (unlike XML), a self-closing slash on a non-void element
			// is ignored by the tree builder: `<div/>` still leaves div open.
			// Mirror that behavior so repeated slash-suffixed tags cannot bypass
			// the depth limit enforced before html.Parse.
			if htmlVoidElements[name] {
				continue
			}
			if len(stack) > 0 && stack[len(stack)-1] == name && htmlOptionalCloseOnSameStart[name] {
				stack = stack[:len(stack)-1]
				budget.pop()
			}
			stack = append(stack, name)
			if err := budget.push("HTML"); err != nil {
				return err
			}
		case xhtml.EndTagToken:
			nameBytes, _ := tokenizer.TagName()
			name := strings.ToLower(string(nameBytes))
			for index := len(stack) - 1; index >= 0; index-- {
				if stack[index] != name {
					continue
				}
				for len(stack) > index {
					stack = stack[:len(stack)-1]
					budget.pop()
				}
				break
			}
		case xhtml.TextToken:
			if len(tokenizer.Raw()) > 0 {
				if err := budget.addNode("HTML"); err != nil {
					return err
				}
			}
		case xhtml.CommentToken, xhtml.DoctypeToken:
			if err := budget.addNode("HTML"); err != nil {
				return err
			}
		}
	}
}

type xpathBodyKind uint8

const (
	xpathBodyXML xpathBodyKind = iota
	xpathBodyHTML
)

func preflightXPathBody(body []byte, namespaces map[string]string) (xpathBodyKind, error) {
	xmlErr := validateExtractorXMLStructure(body)
	if xmlErr == nil {
		return xpathBodyXML, nil
	}
	if errors.Is(xmlErr, errExtractorResourceLimit) {
		return xpathBodyXML, xmlErr
	}
	if xpathRequiresStrictXML(body, namespaces) {
		return xpathBodyXML, fmt.Errorf("invalid XML body: %w", xmlErr)
	}
	if err := validateExtractorHTMLStructure(body); err != nil {
		return xpathBodyHTML, err
	}
	return xpathBodyHTML, nil
}

func extractCSS(expr string, body []byte) (string, error) {
	selector, attribute := splitCSSExpression(expr)
	if selector == "" {
		return "", fmt.Errorf("css selector is required")
	}
	if err := validateExtractorHTMLStructure(body); err != nil {
		return "", err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("invalid HTML body: %w", err)
	}

	match := doc.Find(selector).First()
	if match.Length() == 0 {
		return "", fmt.Errorf("no match for CSS selector %q", selector)
	}

	if attribute != "" {
		value, ok := match.Attr(attribute)
		if !ok {
			return "", fmt.Errorf("attribute %q not found for CSS selector %q", attribute, selector)
		}
		return value, nil
	}

	return strings.TrimSpace(match.Text()), nil
}

func extractXPath(expr string, body []byte) (string, error) {
	expr, fragment, namespaces, err := decodeXPathExpression(expr)
	if err != nil {
		return "", fmt.Errorf("invalid encoded XPath expression: %w", err)
	}
	if expr == "" {
		return "", fmt.Errorf("xpath expression is required")
	}
	selector, err := compileXPathSelector(expr, namespaces)
	if err != nil {
		return "", fmt.Errorf("invalid XPath expression: %w", err)
	}
	bodyKind, err := preflightXPathBody(body, namespaces)
	if err != nil {
		return "", err
	}
	if bodyKind == xpathBodyXML {
		doc, err := xmlquery.Parse(strings.NewReader(string(body)))
		if err != nil {
			return "", fmt.Errorf("invalid XML body: %w", err)
		}
		node := xmlquery.QuerySelector(doc, selector)
		if node == nil {
			return "", fmt.Errorf("no match for XPath %q", expr)
		}
		if fragment {
			return node.OutputXMLWithOptions(xmlquery.WithOutputSelf(), xmlquery.WithoutPreserveSpace()), nil
		}
		return node.InnerText(), nil
	}

	doc, err := htmlquery.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("invalid HTML/XML body: %w", err)
	}

	node := htmlquery.QuerySelector(doc, selector)
	if node == nil {
		return "", fmt.Errorf("no match for XPath %q", expr)
	}
	if fragment {
		return htmlquery.OutputHTML(node, true), nil
	}

	return htmlquery.InnerText(node), nil
}

func xpathRequiresStrictXML(body []byte, namespaces map[string]string) bool {
	if len(namespaces) > 0 {
		return true
	}
	trimmed := bytes.TrimSpace(body)
	trimmed = bytes.TrimPrefix(trimmed, []byte{0xef, 0xbb, 0xbf})
	return bytes.HasPrefix(trimmed, []byte("<?xml"))
}

func decodeXPathExpression(expr string) (string, bool, map[string]string, error) {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, xpathFragmentExpressionPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(expr, xpathFragmentExpressionPrefix)), true, nil, nil
	}
	if strings.HasPrefix(expr, xpathExpressionConfigPrefix) {
		encoded := strings.TrimSpace(strings.TrimPrefix(expr, xpathExpressionConfigPrefix))
		payload, err := base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			return "", false, nil, err
		}
		var config xpathExpressionConfig
		if err := json.Unmarshal(payload, &config); err != nil {
			return "", false, nil, err
		}
		return strings.TrimSpace(config.Expression), config.Fragment, config.Namespaces, nil
	}
	return expr, false, nil, nil
}

func compileXPathSelector(expr string, namespaces map[string]string) (*xpath.Expr, error) {
	cacheKey := xpathSelectorCacheKey(expr, namespaces)
	if err := validateExtractorExpressionSize(cacheKey); err != nil {
		return nil, err
	}
	if cached, ok := compiledXPathCache.get(cacheKey); ok {
		return cached, nil
	}
	var (
		selector *xpath.Expr
		err      error
	)
	if len(namespaces) == 0 {
		selector, err = xpath.Compile(expr)
	} else {
		selector, err = xpath.CompileWithNS(expr, namespaces)
	}
	if err != nil {
		return nil, err
	}
	return compiledXPathCache.put(cacheKey, selector), nil
}

func xpathSelectorCacheKey(expr string, namespaces map[string]string) string {
	if len(namespaces) == 0 {
		return expr
	}
	keys := make([]string, 0, len(namespaces))
	for key := range namespaces {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(expr)
	for _, key := range keys {
		builder.WriteByte(0)
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(namespaces[key])
	}
	return builder.String()
}

func splitCSSExpression(expr string) (selector string, attribute string) {
	parts := strings.SplitN(expr, "|||", 2)
	selector = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		attribute = strings.TrimSpace(parts[1])
	}
	return selector, attribute
}

func extractJSONPath(expr string, body []byte) (string, error) {
	current, err := extractJSONPathValue(expr, body)
	if err != nil {
		return "", err
	}
	return jsonPathValueString(current), nil
}

func extractJSONPathValue(expr string, body []byte) (any, error) {
	current, err := decodeExtractorJSON(body)
	if err != nil {
		return nil, err
	}

	if expr == "$" {
		return current, nil
	}
	if !strings.HasPrefix(expr, "$") {
		return nil, fmt.Errorf("jsonpath must start with $")
	}
	path := strings.TrimPrefix(expr, "$")
	for len(path) > 0 {
		switch path[0] {
		case '.':
			path = path[1:]
			nextSep := len(path)
			if dot := strings.Index(path, "."); dot >= 0 && dot < nextSep {
				nextSep = dot
			}
			if bracket := strings.Index(path, "["); bracket >= 0 && bracket < nextSep {
				nextSep = bracket
			}
			if nextSep == 0 {
				return nil, fmt.Errorf("empty jsonpath field in %q", expr)
			}
			field := path[:nextSep]
			obj, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected object at %q", field)
			}
			var exists bool
			current, exists = obj[field]
			if !exists {
				return nil, fmt.Errorf("field %q not found", field)
			}
			path = path[nextSep:]
		case '[':
			close := strings.Index(path, "]")
			if close < 0 {
				return nil, fmt.Errorf("unterminated jsonpath index in %q", expr)
			}
			indexText := path[1:close]
			idx, err := strconv.Atoi(indexText)
			if err != nil {
				return nil, fmt.Errorf("unsupported jsonpath index %q", indexText)
			}
			arr, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("expected array for index %d", idx)
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("index %d out of range", idx)
			}
			current = arr[idx]
			path = path[close+1:]
		default:
			return nil, fmt.Errorf("unsupported jsonpath segment in %q", expr)
		}
	}

	return current, nil
}

func decodeExtractorJSON(body []byte) (any, error) {
	if !utf8.Valid(body) {
		return nil, fmt.Errorf("invalid JSON body: input is not valid UTF-8")
	}
	if err := validateExtractorJSONStructure(body); err != nil {
		return nil, err
	}
	var current any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&current); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("invalid JSON body: multiple JSON documents")
		}
		return nil, fmt.Errorf("invalid JSON body: trailing data: %w", err)
	}
	return current, nil
}

func jsonPathValueString(value any) string {
	switch typed := value.(type) {
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case []any, map[string]any:
		// A composite (e.g. the whole document for expression "$") must round-trip
		// through JSON, not Go's fmt map[k:v] syntax, so a later template
		// substitution injects valid JSON into a request body.
		if encoded, err := json.Marshal(typed); err == nil {
			return string(encoded)
		}
		return fmt.Sprintf("%v", value)
	default:
		return fmt.Sprintf("%v", value)
	}
}

// stringifyJSONLeaf renders one JSON value as a variable string: scalars use
// their plain form, composites round-trip through JSON encoding.
func stringifyJSONLeaf(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case []any, map[string]any:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(encoded)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func extractJMESPath(expr string, body []byte) (string, error) {
	current, err := decodeExtractorJSON(body)
	if err != nil {
		return "", err
	}

	compiled, err := compileJMESPathCached(expr)
	if err != nil {
		return "", fmt.Errorf("invalid JMESPath expression: %w", err)
	}
	result, err := compiled.Search(current)
	if err != nil {
		return "", fmt.Errorf("evaluate JMESPath expression: %w", err)
	}
	if result == nil {
		return "", fmt.Errorf("no match for JMESPath %q", expr)
	}

	return stringifyJSONLeaf(result), nil
}

func extractRegex(expr string, body []byte) (string, error) {
	re, err := compileRegexCached(expr)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	matches := re.FindSubmatch(body)
	if len(matches) == 0 {
		return "", fmt.Errorf("no match for regex %q", expr)
	}
	matched := matches[0]
	if len(matches) > 1 {
		matched = matches[1]
	}
	if len(matched) > maxExtractorTransformInputBytes {
		return "", fmt.Errorf("%w: regex capture exceeds %d bytes", errExtractorResourceLimit, maxExtractorTransformInputBytes)
	}
	return string(matched), nil
}

func extractRegexCount(expr string, body []byte) (string, error) {
	re, err := compileRegexCached(expr)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	matches := re.FindAllIndex(body, maxAllExtractorMatches+1)
	if len(matches) > maxAllExtractorMatches {
		return "", fmt.Errorf("%w: regex matches exceed %d", errExtractorResourceLimit, maxAllExtractorMatches)
	}
	return fmt.Sprintf("%d", len(matches)), nil
}

func extractRegexOrdinal(expr string, body []byte) (string, error) {
	config, err := decodeRegexOrdinalExpression(expr)
	if err != nil {
		return "", err
	}
	re, err := compileRegexCached(config.Expression)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	if config.Ordinal > maxAllExtractorMatches {
		return "", fmt.Errorf("%w: regex matches exceed %d", errExtractorResourceLimit, maxAllExtractorMatches)
	}
	matches := re.FindAllSubmatch(body, config.Ordinal)
	if len(matches) < config.Ordinal {
		return "", fmt.Errorf("regex %q match ordinal %d not found", config.Expression, config.Ordinal)
	}
	match := matches[config.Ordinal-1]
	if len(match) < 2 {
		return "", fmt.Errorf("regex %q ordinal %d did not capture a value", config.Expression, config.Ordinal)
	}
	if len(match[1]) > maxExtractorTransformInputBytes {
		return "", fmt.Errorf("%w: regex capture exceeds %d bytes", errExtractorResourceLimit, maxExtractorTransformInputBytes)
	}
	return string(match[1]), nil
}

func decodeRegexOrdinalExpression(expr string) (regexOrdinalExpressionConfig, error) {
	if !strings.HasPrefix(expr, regexOrdinalExpressionConfigPrefix) {
		return regexOrdinalExpressionConfig{Expression: expr, Ordinal: 1}, nil
	}
	var config regexOrdinalExpressionConfig
	payload := strings.TrimPrefix(expr, regexOrdinalExpressionConfigPrefix)
	if err := json.Unmarshal([]byte(payload), &config); err != nil {
		return config, fmt.Errorf("invalid regex ordinal config: %w", err)
	}
	if strings.TrimSpace(config.Expression) == "" {
		return config, fmt.Errorf("regex ordinal expression is required")
	}
	if config.Ordinal <= 0 {
		return config, fmt.Errorf("regex ordinal must be greater than zero")
	}
	return config, nil
}

// asciiLowerFold lowercases ASCII letters only, preserving byte length so
// indices computed on the folded string remain valid on the original.
// Unicode-aware folding (strings.ToLower) can change byte length — U+212A
// "K" shrinks, U+023A "Ⱥ" grows — which would corrupt extracted slices.
func asciiLowerFold(s string) string {
	hasUpper := false
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return s
	}
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func extractBoundary(expr string, body []byte) (string, error) {
	config, err := decodeBoundaryExpression(expr)
	if err != nil {
		return "", err
	}

	s := string(body)
	haystack := s
	left, right := config.Left, config.Right
	if config.CaseInsensitive {
		haystack = asciiLowerFold(s)
		left = asciiLowerFold(left)
		right = asciiLowerFold(right)
	}

	ordinal := config.Ordinal
	if ordinal <= 0 {
		ordinal = 1
	}

	pos := 0
	for occurrence := 1; ; occurrence++ {
		li := strings.Index(haystack[pos:], left)
		if li < 0 {
			if occurrence == 1 {
				return "", fmt.Errorf("left boundary %q not found", config.Left)
			}
			return "", fmt.Errorf("boundary match ordinal %d not found (only %d occurrences)", ordinal, occurrence-1)
		}
		start := pos + li + len(left)
		// An empty right boundary extends the match to the end of the content.
		ri := len(haystack) - start
		if right != "" {
			ri = strings.Index(haystack[start:], right)
		}
		if ri < 0 {
			if occurrence == 1 {
				return "", fmt.Errorf("right boundary %q not found", config.Right)
			}
			return "", fmt.Errorf("boundary match ordinal %d not found (only %d occurrences)", ordinal, occurrence-1)
		}
		if occurrence == ordinal {
			return s[start : start+ri], nil
		}
		pos = start + ri + len(right)
	}
}

func extractAllRegex(expr string, body []byte) ([]string, error) {
	re, err := compileRegexCached(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}
	matches := re.FindAllSubmatch(body, maxAllExtractorMatches+1)
	if len(matches) > maxAllExtractorMatches {
		return nil, fmt.Errorf("%w: regex matches exceed %d", errExtractorResourceLimit, maxAllExtractorMatches)
	}
	values := make([]string, 0, len(matches))
	var totalBytes int64
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		value := string(match[1])
		totalBytes += int64(len(value))
		if totalBytes > maxAllExtractorBytes {
			return nil, fmt.Errorf("%w: regex output exceeds %d bytes", errExtractorResourceLimit, maxAllExtractorBytes)
		}
		values = append(values, value)
	}
	return values, nil
}

func extractAllBoundary(expr string, body []byte) ([]string, error) {
	config, err := decodeBoundaryExpression(expr)
	if err != nil {
		return nil, err
	}
	s := string(body)
	haystack := s
	left, right := config.Left, config.Right
	if config.CaseInsensitive {
		haystack = asciiLowerFold(s)
		left = asciiLowerFold(left)
		right = asciiLowerFold(right)
	}

	var values []string
	pos := 0
	for pos <= len(haystack) {
		li := strings.Index(haystack[pos:], left)
		if li < 0 {
			break
		}
		start := pos + li + len(left)
		ri := len(haystack) - start
		if right != "" {
			ri = strings.Index(haystack[start:], right)
			if ri < 0 {
				break
			}
		}
		values = append(values, s[start:start+ri])
		if len(values) > maxAllExtractorMatches {
			return nil, fmt.Errorf("%w: boundary matches exceed %d", errExtractorResourceLimit, maxAllExtractorMatches)
		}
		if right == "" {
			// An empty right boundary consumes the remainder; only one match
			// is possible.
			break
		}
		next := start + ri + len(right)
		if next <= pos {
			break
		}
		pos = next
	}
	return values, nil
}

func extractAllJSONPath(expr string, body []byte) ([]string, error) {
	value, err := extractJSONPathValue(expr, body)
	if err != nil {
		return nil, err
	}
	if arr, ok := value.([]any); ok {
		if len(arr) > maxAllExtractorMatches {
			return nil, fmt.Errorf("%w: JSONPath matches exceed %d", errExtractorResourceLimit, maxAllExtractorMatches)
		}
		values := make([]string, 0, len(arr))
		var totalBytes int64
		for _, item := range arr {
			value := stringifyJSONLeaf(item)
			totalBytes += int64(len(value))
			if totalBytes > maxAllExtractorBytes {
				return nil, fmt.Errorf("%w: JSONPath output exceeds %d bytes", errExtractorResourceLimit, maxAllExtractorBytes)
			}
			values = append(values, value)
		}
		return values, nil
	}
	return []string{stringifyJSONLeaf(value)}, nil
}

func extractAllXPath(expr string, body []byte) ([]string, error) {
	expr, fragment, namespaces, err := decodeXPathExpression(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid encoded XPath expression: %w", err)
	}
	if expr == "" {
		return nil, fmt.Errorf("xpath expression is required")
	}
	selector, err := compileXPathSelector(expr, namespaces)
	if err != nil {
		return nil, fmt.Errorf("invalid XPath expression: %w", err)
	}
	bodyKind, err := preflightXPathBody(body, namespaces)
	if err != nil {
		return nil, err
	}
	if bodyKind == xpathBodyXML {
		doc, err := xmlquery.Parse(strings.NewReader(string(body)))
		if err != nil {
			return nil, fmt.Errorf("invalid XML body: %w", err)
		}
		if xpathMatchLimitExceeded(selector, xmlquery.CreateXPathNavigator(doc)) {
			return nil, fmt.Errorf("%w: XPath matches exceed %d", errExtractorResourceLimit, maxAllExtractorMatches)
		}
		nodes := xmlquery.QuerySelectorAll(doc, selector)
		values := make([]string, 0, len(nodes))
		var totalBytes int64
		for _, node := range nodes {
			var value string
			if fragment {
				value = node.OutputXMLWithOptions(xmlquery.WithOutputSelf(), xmlquery.WithoutPreserveSpace())
			} else {
				value = node.InnerText()
			}
			totalBytes += int64(len(value))
			if totalBytes > maxAllExtractorBytes {
				return nil, fmt.Errorf("%w: XPath output exceeds %d bytes", errExtractorResourceLimit, maxAllExtractorBytes)
			}
			values = append(values, value)
		}
		return values, nil
	}

	doc, err := htmlquery.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("invalid HTML/XML body: %w", err)
	}
	if xpathMatchLimitExceeded(selector, htmlquery.CreateXPathNavigator(doc)) {
		return nil, fmt.Errorf("%w: XPath matches exceed %d", errExtractorResourceLimit, maxAllExtractorMatches)
	}
	nodes := htmlquery.QuerySelectorAll(doc, selector)
	values := make([]string, 0, len(nodes))
	var totalBytes int64
	for _, node := range nodes {
		var value string
		if fragment {
			value = htmlquery.OutputHTML(node, true)
		} else {
			value = htmlquery.InnerText(node)
		}
		totalBytes += int64(len(value))
		if totalBytes > maxAllExtractorBytes {
			return nil, fmt.Errorf("%w: XPath output exceeds %d bytes", errExtractorResourceLimit, maxAllExtractorBytes)
		}
		values = append(values, value)
	}
	return values, nil
}

func xpathMatchLimitExceeded(selector *xpath.Expr, navigator xpath.NodeNavigator) bool {
	iterator := selector.Select(navigator)
	count := 0
	for iterator.MoveNext() {
		count++
		if count > maxAllExtractorMatches {
			return true
		}
	}
	return false
}

func decodeBoundaryExpression(expr string) (boundaryExpressionConfig, error) {
	if strings.HasPrefix(expr, boundaryExpressionConfigPrefix) {
		var config boundaryExpressionConfig
		payload := strings.TrimPrefix(expr, boundaryExpressionConfigPrefix)
		if err := json.Unmarshal([]byte(payload), &config); err != nil {
			return config, fmt.Errorf("invalid boundary config: %w", err)
		}
		if config.Left == "" && config.Right == "" {
			return config, fmt.Errorf("boundary config requires a left or right boundary")
		}
		return config, nil
	}
	parts := strings.SplitN(expr, "|||", 2)
	if len(parts) != 2 {
		return boundaryExpressionConfig{}, fmt.Errorf("boundary expression must use ||| separator: left|||right")
	}
	return boundaryExpressionConfig{Left: parts[0], Right: parts[1], Ordinal: 1}, nil
}
