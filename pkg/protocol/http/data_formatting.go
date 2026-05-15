package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"sort"
	"strings"
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

func applyDataFormattingChain(body []byte, autoDetect bool, chain []dataFormattingTransform) ([]byte, error) {
	formatted := append([]byte(nil), body...)
	for _, transform := range chain {
		var err error
		formatted, err = applyDataFormattingTransform(formatted, transform)
		if err != nil {
			return nil, fmt.Errorf("data formatting transform %q failed: %w", transform.Type, err)
		}
	}
	if autoDetect && len(chain) == 0 {
		return formatted, nil
	}
	return formatted, nil
}

func applyDataFormattingTransform(body []byte, transform dataFormattingTransform) ([]byte, error) {
	switch transform.Type {
	case "base64":
		decoded, err := decodeBase64Body(body)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	case "json_to_xml":
		converted, err := jsonToXML(body)
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
	case "html_unescape":
		return []byte(strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&", "&quot;", `"`, "&#39;", "'").Replace(string(body))), nil
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