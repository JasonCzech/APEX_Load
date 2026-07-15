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
	formatted := append([]byte(nil), body...)
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
	formatted := append([]byte(nil), body...)
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
	if transform.Type == "gzip" && strings.ToLower(strings.TrimSpace(transform.Options["mode"])) != "encode" {
		return decodeGzipBody(body, maxBytes)
	}
	return applyDataFormattingTransform(body, transform)
}

func applyAutoDetectedDataFormatting(body []byte, allowBase64 bool) []byte {
	formatted := append([]byte(nil), body...)
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
func decodeGzipBody(body []byte, maxBytes int64) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return readAllLimited(reader, maxBytes)
}

func encodeGzipBody(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(body); err != nil {
		writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
	name     string
	attrs    map[string]string
	text     []string
	children map[string][]any
}

func jsonToXML(body []byte) ([]byte, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString("<json>")
	if err := writeXMLValue(&buf, "value", payload); err != nil {
		return nil, err
	}
	buf.WriteString("</json>")
	return buf.Bytes(), nil
}

func xmlToJSON(body []byte) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	stack := make([]*xmlJSONNode, 0)
	var root map[string]any

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
			node := &xmlJSONNode{
				name:     sanitizeXMLName(typed.Name.Local),
				attrs:    make(map[string]string),
				children: make(map[string][]any),
			}
			for _, attr := range typed.Attr {
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
				continue
			}
			if text := strings.TrimSpace(string(typed)); text != "" {
				stack[len(stack)-1].text = append(stack[len(stack)-1].text, text)
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
	return json.Marshal(root)
}

func (node *xmlJSONNode) toJSONValue() any {
	text := strings.Join(node.text, " ")
	if len(node.attrs) == 0 && len(node.children) == 0 {
		return text
	}

	value := make(map[string]any)
	if len(node.attrs) > 0 {
		attrs := make(map[string]string, len(node.attrs))
		for key, attrValue := range node.attrs {
			attrs[key] = attrValue
		}
		value["@attributes"] = attrs
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

func writeXMLValue(buf *bytes.Buffer, name string, value any) error {
	switch typed := value.(type) {
	case map[string]any:
		writeStartElement(buf, name)
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
		writeEndElement(buf, name)
	case []any:
		writeStartElement(buf, name)
		for _, item := range typed {
			if err := writeXMLValue(buf, "item", item); err != nil {
				return err
			}
		}
		writeEndElement(buf, name)
	case nil:
		writeStartElement(buf, name)
		writeEndElement(buf, name)
	case string:
		writeStartElement(buf, name)
		xml.EscapeText(buf, []byte(typed))
		writeEndElement(buf, name)
	case bool:
		writeStartElement(buf, name)
		xml.EscapeText(buf, []byte(fmt.Sprintf("%t", typed)))
		writeEndElement(buf, name)
	case float64, float32, int, int32, int64, uint, uint32, uint64:
		writeStartElement(buf, name)
		xml.EscapeText(buf, []byte(fmt.Sprintf("%v", typed)))
		writeEndElement(buf, name)
	default:
		writeStartElement(buf, name)
		xml.EscapeText(buf, []byte(fmt.Sprintf("%v", typed)))
		writeEndElement(buf, name)
	}
	return nil
}

func writeStartElement(buf *bytes.Buffer, name string) {
	buf.WriteByte('<')
	buf.WriteString(name)
	buf.WriteByte('>')
}

func writeEndElement(buf *bytes.Buffer, name string) {
	buf.WriteString("</")
	buf.WriteString(name)
	buf.WriteByte('>')
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
