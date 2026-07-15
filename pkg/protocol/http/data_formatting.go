package http

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

type dataFormattingTransform struct {
	Type    string
	Options map[string]string
}

func parseDataFormattingAutoDetect(config map[string]any) bool {
	value, ok := config["web_data_formatting_auto_detect"].(bool)
	return ok && value
}

func parseDataFormattingChain(config map[string]any) []dataFormattingTransform {
	rawChain, ok := config["web_data_formatting_chain"]
	if !ok {
		return nil
	}
	items, ok := rawChain.([]map[string]any)
	if ok {
		return parseDataFormattingItems(items)
	}
	rawItems, ok := rawChain.([]any)
	if !ok {
		return nil
	}
	items = make([]map[string]any, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		items = append(items, item)
	}
	return parseDataFormattingItems(items)
}

func parseDataFormattingItems(items []map[string]any) []dataFormattingTransform {
	transforms := make([]dataFormattingTransform, 0, len(items))
	for _, item := range items {
		transformType, _ := item["type"].(string)
		transformType = strings.ToLower(strings.TrimSpace(transformType))
		if transformType == "" {
			continue
		}
		transforms = append(transforms, dataFormattingTransform{
			Type:    transformType,
			Options: coerceStringMap(item["options"]),
		})
	}
	return transforms
}

func coerceStringMap(raw any) map[string]string {
	if raw == nil {
		return nil
	}
	if typed, ok := raw.(map[string]string); ok {
		cloned := make(map[string]string, len(typed))
		for key, value := range typed {
			cloned[key] = value
		}
		return cloned
	}
	items, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	coerced := make(map[string]string, len(items))
	for key, value := range items {
		if text, ok := value.(string); ok {
			coerced[key] = text
		}
	}
	return coerced
}

func applyDataFormattingChain(body []byte, autoDetect bool, chain []dataFormattingTransform, maxBytes int64) ([]byte, error) {
	// Formatting transforms return new byte slices and never mutate their
	// inputs. Keep the original view until a transform actually changes it so
	// the ordinary (formatting-disabled) HTTP hot path does not clone every
	// response body.
	formatted := body
	for _, transform := range chain {
		var err error
		formatted, err = applyDataFormattingTransformBounded(formatted, transform, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("data formatting transform %q failed: %w", transform.Type, err)
		}
		if maxBytes > 0 && int64(len(formatted)) > maxBytes {
			return nil, fmt.Errorf("data formatting transform %q produced %d bytes, limit is %d", transform.Type, len(formatted), maxBytes)
		}
	}
	if autoDetect {
		var err error
		formatted, err = applyAutoDetectedDataFormattingBounded(formatted, len(chain) == 0, maxBytes)
		if err != nil {
			return nil, err
		}
	}
	return formatted, nil
}

func applyAutoDetectedDataFormattingBounded(body []byte, allowBase64 bool, maxBytes int64) ([]byte, error) {
	formatted := body
	if bytes.HasPrefix(bytes.TrimSpace(formatted), []byte{0x1f, 0x8b}) {
		decoded, err := decodeGzipBody(bytes.TrimSpace(formatted), maxBytes)
		if err != nil {
			return nil, fmt.Errorf("auto-detected gzip decode failed: %w", err)
		}
		formatted = decoded
	}
	if allowBase64 {
		decoded, decodeErr := decodeBase64Body(formatted)
		if decodeErr == nil && (isLikelyAutoDecodedPayload(decoded) || bytes.HasPrefix(bytes.TrimSpace(decoded), []byte{0x1f, 0x8b})) {
			formatted = decoded
		}
		if bytes.HasPrefix(bytes.TrimSpace(formatted), []byte{0x1f, 0x8b}) {
			decoded, err := decodeGzipBody(bytes.TrimSpace(formatted), maxBytes)
			if err != nil {
				return nil, fmt.Errorf("auto-detected gzip decode failed: %w", err)
			}
			formatted = decoded
		}
	}
	if maxBytes > 0 && int64(len(formatted)) > maxBytes {
		return nil, fmt.Errorf("auto-detected formatting produced %d bytes, limit is %d", len(formatted), maxBytes)
	}
	return formatted, nil
}

func applyDataFormattingTransformBounded(body []byte, transform dataFormattingTransform, maxBytes int64) ([]byte, error) {
	switch transform.Type {
	case "gzip":
		switch mode := strings.ToLower(strings.TrimSpace(transform.Options["mode"])); mode {
		case "", "decode":
			return decodeGzipBody(body, maxBytes)
		case "encode":
			return encodeGzipBodyBounded(body, maxBytes)
		default:
			return nil, fmt.Errorf("unsupported gzip mode %q", mode)
		}
	case "json_to_xml":
		return jsonToXMLBounded(body, maxBytes)
	case "xml_to_json":
		return xmlToJSONBounded(body, maxBytes)
	case "url_encode":
		return transformStringChunksBounded(body, maxBytes, url.QueryEscape)
	case "html_escape":
		return transformStringChunksBounded(body, maxBytes, html.EscapeString)
	}
	return applyDataFormattingTransform(body, transform)
}

func applyAutoDetectedDataFormatting(body []byte, allowBase64 bool) []byte {
	formatted := body
	if decoded, ok := detectGzipPayload(formatted); ok {
		formatted = decoded
	}
	if allowBase64 {
		if decoded, ok := detectBase64Payload(formatted); ok {
			formatted = decoded
			if gzipDecoded, ok := detectGzipPayload(formatted); ok {
				formatted = gzipDecoded
			}
		}
	}
	return formatted
}

func applyDataFormattingTransform(body []byte, transform dataFormattingTransform) ([]byte, error) {
	switch transform.Type {
	case "base64":
		decoded, err := decodeBase64Body(body)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	case "gzip":
		mode := strings.ToLower(strings.TrimSpace(transform.Options["mode"]))
		switch mode {
		case "", "decode":
			decoded, err := decodeGzipBody(body, 0)
			if err != nil {
				return nil, err
			}
			return decoded, nil
		case "encode":
			encoded, err := encodeGzipBody(body)
			if err != nil {
				return nil, err
			}
			return encoded, nil
		default:
			return nil, fmt.Errorf("unsupported gzip mode %q", mode)
		}
	case "json_to_xml":
		converted, err := jsonToXML(body)
		if err != nil {
			return nil, err
		}
		return converted, nil
	case "xml_to_json":
		converted, err := xmlToJSON(body)
		if err != nil {
			return nil, err
		}
		return converted, nil
	case "url_decode":
		decoded, err := url.QueryUnescape(strings.TrimSpace(string(body)))
		if err != nil {
			return nil, err
		}
		return []byte(decoded), nil
	case "url_encode":
		return []byte(url.QueryEscape(string(body))), nil
	case "html_unescape":
		return []byte(html.UnescapeString(string(body))), nil
	case "html_escape":
		return []byte(html.EscapeString(string(body))), nil
	default:
		return body, nil
	}
}

func decodeBase64Body(body []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return []byte{}, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err == nil {
		return decoded, nil
	}
	decoded, rawErr := base64.RawStdEncoding.DecodeString(trimmed)
	if rawErr == nil {
		return decoded, nil
	}
	return nil, err
}

// decodeGzipBody decompresses body. maxBytes > 0 bounds the decoded size
// (decompression-bomb guard); maxBytes <= 0 is unbounded (request-side callers
// pass 0 since those payloads are operator-authored, not remote).
func decodeGzipBody(body []byte, maxBytes int64, leases ...*responseCaptureLease) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var lease *responseCaptureLease
	if len(leases) > 0 {
		lease = leases[0]
	}
	return readAllLimitedWithLease(reader, maxBytes, lease)
}

func encodeGzipBody(body []byte) ([]byte, error) {
	return encodeGzipBodyBounded(body, 0)
}

func encodeGzipBodyBounded(body []byte, maxBytes int64) ([]byte, error) {
	buf := newBoundedFormattingBuffer(maxBytes)
	writer := gzip.NewWriter(buf)
	if _, err := writer.Write(body); err != nil {
		writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func transformStringChunksBounded(body []byte, maxBytes int64, transform func(string) string) ([]byte, error) {
	buf := newBoundedFormattingBuffer(maxBytes)
	const chunkSize = 4096
	for offset := 0; offset < len(body); offset += chunkSize {
		end := offset + chunkSize
		if end > len(body) {
			end = len(body)
		}
		if _, err := buf.WriteString(transform(string(body[offset:end]))); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

type boundedFormattingBuffer struct {
	buf bytes.Buffer
	max int64
}

func newBoundedFormattingBuffer(maxBytes int64) *boundedFormattingBuffer {
	return &boundedFormattingBuffer{max: maxBytes}
}

func (b *boundedFormattingBuffer) Write(content []byte) (int, error) {
	if b.max > 0 && (int64(b.buf.Len()) > b.max || int64(len(content)) > b.max-int64(b.buf.Len())) {
		return 0, fmt.Errorf("formatted output exceeds byte limit %d", b.max)
	}
	return b.buf.Write(content)
}

func (b *boundedFormattingBuffer) WriteString(content string) (int, error) {
	return b.Write([]byte(content))
}

func (b *boundedFormattingBuffer) WriteByte(value byte) error {
	_, err := b.Write([]byte{value})
	return err
}

func (b *boundedFormattingBuffer) Bytes() []byte {
	return b.buf.Bytes()
}

func detectBase64Payload(body []byte) ([]byte, bool) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, false
	}
	decoded, err := decodeBase64Body([]byte(trimmed))
	if err != nil {
		return nil, false
	}
	if isLikelyAutoDecodedPayload(decoded) {
		return decoded, true
	}
	if gzipDecoded, ok := detectGzipPayload(decoded); ok {
		return gzipDecoded, true
	}
	return nil, false
}

func detectGzipPayload(body []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) < 2 || trimmed[0] != 0x1f || trimmed[1] != 0x8b {
		return nil, false
	}
	decoded, err := decodeGzipBody(trimmed, 0)
	if err != nil || !isLikelyAutoDecodedPayload(decoded) {
		return nil, false
	}
	return decoded, true
}

func isLikelyAutoDecodedPayload(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || !utf8.Valid(trimmed) {
		return false
	}
	if json.Valid(trimmed) {
		return true
	}
	if trimmed[0] == '<' {
		return true
	}
	text := string(trimmed)
	return strings.Contains(text, "=") || strings.Contains(text, "&")
}

type xmlJSONNode struct {
	name      string
	attrs     map[string]string
	text      []string
	textBytes int64
	children  map[string][]any
}

const (
	maxXMLJSONNodes             = 100_000
	maxXMLJSONStructuralEntries = 100_000
	maxXMLJSONDepth             = 256
	maxXMLJSONAttributesPerNode = 4_096
	maxXMLJSONTextPartsPerNode  = 4_096
	maxXMLJSONNameBytes         = 4 << 10
	maxXMLJSONTextSegmentBytes  = 1 << 20
	maxXMLJSONRetainedTextBytes = 64 << 20
	maxJSONXMLNodes             = 100_000
	maxJSONXMLDepth             = 256
	maxJSONXMLNameBytes         = 4 << 10
	maxJSONXMLScalarBytes       = 1 << 20
	maxJSONXMLRetainedBytes     = 64 << 20
)

func jsonToXML(body []byte) ([]byte, error) {
	return jsonToXMLBounded(body, 0)
}

func jsonToXMLBounded(body []byte, maxBytes int64) ([]byte, error) {
	outputLimit := maxBytes
	if outputLimit <= 0 || outputLimit > maxJSONXMLRetainedBytes {
		outputLimit = maxJSONXMLRetainedBytes
	}
	buf := newBoundedFormattingBuffer(outputLimit)
	if _, err := buf.WriteString("<json>"); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	budget := jsonXMLBudget{retainedLimit: maxJSONXMLRetainedBytes}
	if maxBytes > 0 && maxBytes < budget.retainedLimit {
		budget.retainedLimit = maxBytes
	}
	if err := writeJSONTokenXMLValue(decoder, buf, "value", 0, &budget); err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected trailing JSON token %v", token)
	}
	if _, err := buf.WriteString("</json>"); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type jsonXMLBudget struct {
	nodes         int
	retainedBytes int64
	retainedLimit int64
}

func (budget *jsonXMLBudget) retain(size int, kind string) error {
	if size < 0 || int64(size) > budget.retainedLimit-budget.retainedBytes {
		return fmt.Errorf("JSON-to-XML retained %s exceeds %d bytes", kind, budget.retainedLimit)
	}
	budget.retainedBytes += int64(size)
	return nil
}

// writeJSONTokenXMLValue converts directly from the decoder token stream. It
// never materializes a map/slice tree, so deeply nested or very wide JSON
// cannot multiply the input into an unbounded intermediate object graph before
// the formatted-output limit is enforced.
func writeJSONTokenXMLValue(decoder *json.Decoder, buf *boundedFormattingBuffer, name string, depth int, budget *jsonXMLBudget) error {
	if depth >= maxJSONXMLDepth {
		return fmt.Errorf("JSON-to-XML depth limit exceeded (%d)", maxJSONXMLDepth)
	}
	budget.nodes++
	if budget.nodes > maxJSONXMLNodes {
		return fmt.Errorf("JSON-to-XML node limit exceeded (%d)", maxJSONXMLNodes)
	}
	if len(name) > maxJSONXMLNameBytes {
		return fmt.Errorf("JSON-to-XML element name exceeds %d bytes", maxJSONXMLNameBytes)
	}
	if err := budget.retain(len(name), "names"); err != nil {
		return err
	}

	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if err := writeStartElement(buf, name); err != nil {
		return err
	}
	switch typed := token.(type) {
	case json.Delim:
		switch typed {
		case '{':
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("JSON object key is not a string")
				}
				if len(key) > maxJSONXMLNameBytes {
					return fmt.Errorf("JSON-to-XML object key exceeds %d bytes", maxJSONXMLNameBytes)
				}
				if err := writeJSONTokenXMLValue(decoder, buf, sanitizeXMLName(key), depth+1, budget); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil {
				return err
			}
			if end != json.Delim('}') {
				return fmt.Errorf("malformed JSON object")
			}
		case '[':
			for decoder.More() {
				if err := writeJSONTokenXMLValue(decoder, buf, "item", depth+1, budget); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil {
				return err
			}
			if end != json.Delim(']') {
				return fmt.Errorf("malformed JSON array")
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", typed)
		}
	case string:
		if len(typed) > maxJSONXMLScalarBytes {
			return fmt.Errorf("JSON-to-XML scalar exceeds %d bytes", maxJSONXMLScalarBytes)
		}
		if err := budget.retain(len(typed), "scalars"); err != nil {
			return err
		}
		if err := xml.EscapeText(buf, []byte(typed)); err != nil {
			return err
		}
	case json.Number:
		value := typed.String()
		if err := budget.retain(len(value), "scalars"); err != nil {
			return err
		}
		if err := xml.EscapeText(buf, []byte(value)); err != nil {
			return err
		}
	case bool:
		if _, err := buf.WriteString(fmt.Sprintf("%t", typed)); err != nil {
			return err
		}
	case nil:
	default:
		return fmt.Errorf("unsupported JSON token %T", token)
	}
	return writeEndElement(buf, name)
}

func xmlToJSON(body []byte) ([]byte, error) {
	return xmlToJSONBounded(body, 0)
}

func xmlToJSONBounded(body []byte, maxBytes int64) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	stack := make([]*xmlJSONNode, 0)
	var root map[string]any
	rootSeen := false
	nodeLimit := maxXMLJSONNodes
	retainedLimit := int64(maxXMLJSONRetainedTextBytes)
	if maxBytes > 0 {
		if byOutput := maxBytes / 8; byOutput < int64(nodeLimit) {
			nodeLimit = int(max(1, byOutput))
		}
		if maxBytes < retainedLimit {
			retainedLimit = maxBytes
		}
	}
	nodes := 0
	structuralEntries := 0
	var retainedBytes int64
	retainStructure := func(entries int) error {
		if entries < 0 || structuralEntries > maxXMLJSONStructuralEntries-entries {
			return fmt.Errorf("XML-to-JSON structural entry limit exceeded (%d)", maxXMLJSONStructuralEntries)
		}
		structuralEntries += entries
		return nil
	}
	retain := func(size int) error {
		if size < 0 || int64(size) > retainedLimit-retainedBytes {
			return fmt.Errorf("XML-to-JSON retained text exceeds %d bytes", retainedLimit)
		}
		retainedBytes += int64(size)
		return nil
	}

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch typed := token.(type) {
		case xml.StartElement:
			if len(stack) == 0 {
				if rootSeen {
					return nil, fmt.Errorf("XML payload must contain exactly one root element")
				}
				rootSeen = true
			}
			nodes++
			if nodes > nodeLimit {
				return nil, fmt.Errorf("XML-to-JSON node limit exceeded (%d)", nodeLimit)
			}
			if err := retainStructure(1); err != nil {
				return nil, err
			}
			if len(stack) >= maxXMLJSONDepth {
				return nil, fmt.Errorf("XML-to-JSON depth limit exceeded (%d)", maxXMLJSONDepth)
			}
			if len(typed.Name.Local) > maxXMLJSONNameBytes {
				return nil, fmt.Errorf("XML element name exceeds %d bytes", maxXMLJSONNameBytes)
			}
			if err := retain(len(typed.Name.Local)); err != nil {
				return nil, err
			}
			node := &xmlJSONNode{
				name:     sanitizeXMLName(typed.Name.Local),
				attrs:    make(map[string]string),
				children: make(map[string][]any),
			}
			if len(typed.Attr) > maxXMLJSONAttributesPerNode {
				return nil, fmt.Errorf("XML element attribute limit exceeded (%d)", maxXMLJSONAttributesPerNode)
			}
			for _, attr := range typed.Attr {
				if err := retainStructure(1); err != nil {
					return nil, err
				}
				if len(attr.Name.Local) > maxXMLJSONNameBytes || len(attr.Value) > maxXMLJSONTextSegmentBytes {
					return nil, fmt.Errorf("XML attribute exceeds name/value limits (%d/%d bytes)", maxXMLJSONNameBytes, maxXMLJSONTextSegmentBytes)
				}
				if err := retain(len(attr.Name.Local) + len(attr.Value)); err != nil {
					return nil, err
				}
				key := sanitizeXMLName(attr.Name.Local)
				if key != "" {
					node.attrs[key] = attr.Value
				}
			}
			if len(node.attrs) == 0 {
				node.attrs = nil
			}
			stack = append(stack, node)
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(typed)) != "" {
					return nil, fmt.Errorf("non-whitespace text outside XML root element")
				}
				continue
			}
			if len(typed) > maxXMLJSONTextSegmentBytes {
				return nil, fmt.Errorf("XML text segment exceeds %d bytes", maxXMLJSONTextSegmentBytes)
			}
			if text := strings.TrimSpace(string(typed)); text != "" {
				node := stack[len(stack)-1]
				if len(node.text) >= maxXMLJSONTextPartsPerNode {
					return nil, fmt.Errorf("XML element text-part limit exceeded (%d)", maxXMLJSONTextPartsPerNode)
				}
				if err := retainStructure(1); err != nil {
					return nil, err
				}
				additional := int64(len(text))
				if len(node.text) > 0 {
					additional++
				}
				if node.textBytes+additional > maxXMLJSONTextSegmentBytes {
					return nil, fmt.Errorf("XML element text exceeds %d bytes", maxXMLJSONTextSegmentBytes)
				}
				if err := retain(int(additional)); err != nil {
					return nil, err
				}
				node.textBytes += additional
				node.text = append(node.text, text)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("unexpected closing element %q", typed.Name.Local)
			}
			node := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			value := node.toJSONValue()
			if len(stack) == 0 {
				root = map[string]any{node.name: value}
				continue
			}
			parent := stack[len(stack)-1]
			parent.children[node.name] = append(parent.children[node.name], value)
		}
	}

	if len(stack) != 0 {
		return nil, fmt.Errorf("unclosed XML element %q", stack[len(stack)-1].name)
	}
	if root == nil {
		return nil, fmt.Errorf("empty XML payload")
	}
	buf := newBoundedFormattingBuffer(maxBytes)
	if err := writeBoundedJSONValue(buf, root); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (node *xmlJSONNode) toJSONValue() any {
	text := strings.Join(node.text, " ")
	if len(node.attrs) == 0 && len(node.children) == 0 {
		return text
	}

	value := make(map[string]any)
	if len(node.attrs) > 0 {
		value["@attributes"] = node.attrs
	}
	if text != "" {
		value["#text"] = text
	}

	keys := make([]string, 0, len(node.children))
	for key := range node.children {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		children := node.children[key]
		if len(children) == 1 {
			value[key] = children[0]
			continue
		}
		value[key] = children
	}
	return value
}

func writeBoundedJSONValue(buf *boundedFormattingBuffer, value any) error {
	switch typed := value.(type) {
	case map[string]any:
		if err := buf.WriteByte('{'); err != nil {
			return err
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for index, key := range keys {
			if index > 0 {
				if err := buf.WriteByte(','); err != nil {
					return err
				}
			}
			if err := writeBoundedJSONString(buf, key); err != nil {
				return err
			}
			if err := buf.WriteByte(':'); err != nil {
				return err
			}
			if err := writeBoundedJSONValue(buf, typed[key]); err != nil {
				return err
			}
		}
		return buf.WriteByte('}')
	case map[string]string:
		if err := buf.WriteByte('{'); err != nil {
			return err
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for index, key := range keys {
			if index > 0 {
				if err := buf.WriteByte(','); err != nil {
					return err
				}
			}
			if err := writeBoundedJSONString(buf, key); err != nil {
				return err
			}
			if err := buf.WriteByte(':'); err != nil {
				return err
			}
			if err := writeBoundedJSONString(buf, typed[key]); err != nil {
				return err
			}
		}
		return buf.WriteByte('}')
	case []any:
		if err := buf.WriteByte('['); err != nil {
			return err
		}
		for index, item := range typed {
			if index > 0 {
				if err := buf.WriteByte(','); err != nil {
					return err
				}
			}
			if err := writeBoundedJSONValue(buf, item); err != nil {
				return err
			}
		}
		return buf.WriteByte(']')
	case string:
		return writeBoundedJSONString(buf, typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		_, err = buf.Write(encoded)
		return err
	}
}

func writeBoundedJSONString(buf *boundedFormattingBuffer, value string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = buf.Write(encoded)
	return err
}

func writeXMLValue(buf *boundedFormattingBuffer, name string, value any) error {
	switch typed := value.(type) {
	case map[string]any:
		if err := writeStartElement(buf, name); err != nil {
			return err
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := writeXMLValue(buf, sanitizeXMLName(key), typed[key]); err != nil {
				return err
			}
		}
		return writeEndElement(buf, name)
	case []any:
		if err := writeStartElement(buf, name); err != nil {
			return err
		}
		for _, item := range typed {
			if err := writeXMLValue(buf, "item", item); err != nil {
				return err
			}
		}
		return writeEndElement(buf, name)
	case nil:
		if err := writeStartElement(buf, name); err != nil {
			return err
		}
		return writeEndElement(buf, name)
	case string:
		if err := writeStartElement(buf, name); err != nil {
			return err
		}
		if err := xml.EscapeText(buf, []byte(typed)); err != nil {
			return err
		}
		return writeEndElement(buf, name)
	case bool:
		if err := writeStartElement(buf, name); err != nil {
			return err
		}
		if err := xml.EscapeText(buf, []byte(fmt.Sprintf("%t", typed))); err != nil {
			return err
		}
		return writeEndElement(buf, name)
	case float64, float32, int, int32, int64, uint, uint32, uint64:
		if err := writeStartElement(buf, name); err != nil {
			return err
		}
		if err := xml.EscapeText(buf, []byte(fmt.Sprintf("%v", typed))); err != nil {
			return err
		}
		return writeEndElement(buf, name)
	default:
		if err := writeStartElement(buf, name); err != nil {
			return err
		}
		if err := xml.EscapeText(buf, []byte(fmt.Sprintf("%v", typed))); err != nil {
			return err
		}
		return writeEndElement(buf, name)
	}
}

func writeStartElement(buf *boundedFormattingBuffer, name string) error {
	if err := buf.WriteByte('<'); err != nil {
		return err
	}
	if _, err := buf.WriteString(name); err != nil {
		return err
	}
	return buf.WriteByte('>')
}

func writeEndElement(buf *boundedFormattingBuffer, name string) error {
	if _, err := buf.WriteString("</"); err != nil {
		return err
	}
	if _, err := buf.WriteString(name); err != nil {
		return err
	}
	return buf.WriteByte('>')
}

func sanitizeXMLName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "item"
	}
	var builder strings.Builder
	for index, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == '-' || (index > 0 && r >= '0' && r <= '9')
		if !valid {
			builder.WriteByte('_')
			continue
		}
		if index == 0 && r >= '0' && r <= '9' {
			builder.WriteByte('_')
		}
		builder.WriteRune(r)
	}
	if builder.Len() == 0 {
		return "item"
	}
	return builder.String()
}
