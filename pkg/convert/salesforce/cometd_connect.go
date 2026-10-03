package salesforce

import (
	"encoding/json"
	"io"
	"net/url"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Loop synthesis inspects only modest single-message Bayeux requests. Bodies
// outside these bounds remain discrete recorded requests.
const (
	maxCometdConnectBodyBytes  = 64 << 10
	maxCometdConnectFormFields = 256
	maxCometdConnectTokens     = 8192
	maxCometdConnectDepth      = 32
)

func cometdConnectMessageBody(body string) (string, bool) {
	if len(body) > maxCometdConnectBodyBytes {
		return "", false
	}
	trimmed := strings.TrimSpace(body)
	if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
		return trimmed, true
	}
	message := ""
	found := false
	for fields, rest := 0, body; ; fields++ {
		if fields >= maxCometdConnectFormFields {
			return "", false
		}
		field, tail, more := strings.Cut(rest, "&")
		key, value, _ := strings.Cut(field, "=")
		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			return "", false
		}
		decodedValue, err := url.QueryUnescape(value)
		if err != nil || strings.Contains(field, ";") {
			return "", false
		}
		if decodedKey == "message" {
			if found {
				return "", false
			}
			message = decodedValue
			found = true
		}
		if !more {
			break
		}
		rest = tail
	}
	return message, found
}

func singleBayeuxConnectMessage(raw string) (map[string]any, bool, bool) {
	if len(raw) > maxCometdConnectBodyBytes || !utf8.ValidString(raw) || !validBayeuxJSONStringEscapes(raw) {
		return nil, false, false
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	remaining := maxCometdConnectTokens
	if !consumeBayeuxJSONValue(decoder, &remaining, 0) {
		return nil, false, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, false, false
	}
	decoder = json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false, false
	}
	array := false
	if messages, ok := value.([]any); ok {
		if len(messages) != 1 {
			return nil, false, false
		}
		array = true
		value = messages[0]
	}
	message, ok := value.(map[string]any)
	if !ok {
		return nil, false, false
	}
	channel, _ := message["channel"].(string)
	if channel != "/meta/connect" {
		return nil, false, false
	}
	if value, exists := message["id"]; exists {
		if id, ok := value.(string); !ok || id == "" {
			return nil, false, false
		}
	}
	return message, array, true
}

func consumeBayeuxJSONValue(decoder *json.Decoder, remaining *int, depth int) bool {
	if depth > maxCometdConnectDepth || *remaining <= 0 {
		return false
	}
	*remaining -= 1
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return true
	}
	switch delimiter {
	case '{':
		members := map[string]bool{}
		for decoder.More() {
			if *remaining <= 0 {
				return false
			}
			*remaining -= 1
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok || members[name] {
				return false
			}
			members[name] = true
			if !consumeBayeuxJSONValue(decoder, remaining, depth+1) {
				return false
			}
		}
	case '[':
		for decoder.More() {
			if !consumeBayeuxJSONValue(decoder, remaining, depth+1) {
				return false
			}
		}
	default:
		return false
	}
	if *remaining <= 0 {
		return false
	}
	*remaining -= 1
	close, err := decoder.Token()
	return err == nil && ((delimiter == '{' && close == json.Delim('}')) || (delimiter == '[' && close == json.Delim(']')))
}

// encoding/json replaces lone surrogate escapes. Reject them so changing the
// message ID never normalizes an unselected captured JSON string or member key.
func validBayeuxJSONStringEscapes(raw string) bool {
	quoted := false
	for index := 0; index < len(raw); index++ {
		if raw[index] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || raw[index] != '\\' {
			continue
		}
		index++
		if index >= len(raw) {
			return false
		}
		if raw[index] != 'u' {
			continue
		}
		value, ok := bayeuxHexQuad(raw, index+1)
		if !ok {
			return false
		}
		index += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value < 0xd800 || value > 0xdbff {
			continue
		}
		if index+6 >= len(raw) || raw[index+1] != '\\' || raw[index+2] != 'u' {
			return false
		}
		low, ok := bayeuxHexQuad(raw, index+3)
		if !ok || low < 0xdc00 || low > 0xdfff {
			return false
		}
		index += 6
	}
	return true
}

func bayeuxHexQuad(raw string, start int) (uint16, bool) {
	if start+4 > len(raw) {
		return 0, false
	}
	var value uint16
	for index := start; index < start+4; index++ {
		var digit byte
		switch character := raw[index]; {
		case character >= '0' && character <= '9':
			digit = character - '0'
		case character >= 'a' && character <= 'f':
			digit = character - 'a' + 10
		case character >= 'A' && character <= 'F':
			digit = character - 'A' + 10
		default:
			return 0, false
		}
		value = value*16 + uint16(digit)
	}
	return value, true
}

// Repeating the first request is safe only when the remaining requests have the
// same execution semantics. Source bookkeeping and display names may differ;
// unknown metadata remains significant, including all execution/review gates.
func cometdConnectComparisonsEqual(first, second dsl.Action) bool {
	return reflect.DeepEqual(first, second)
}

func cometdConnectComparisonAction(action dsl.Action) (dsl.Action, bool) {
	if actionClassification(&action) != "cometd" || strings.TrimSpace(action.BodyEncoding) != "" || len(action.Extractors) > dsl.MaxActionExtractors || len(action.Metadata) > maxCometdConnectTokens {
		return dsl.Action{}, false
	}
	messageBody, ok := cometdConnectMessageBody(action.Body)
	if !ok {
		return dsl.Action{}, false
	}
	message, array, ok := singleBayeuxConnectMessage(messageBody)
	if !ok {
		return dsl.Action{}, false
	}
	// Absent IDs remain absent. A present, validated ID is the only payload
	// field allowed to vary between repeated connect requests.
	if _, exists := message["id"]; exists {
		message["id"] = cometdLoopIndexTemplate
	}
	var value any = message
	if array {
		value = []map[string]any{message}
	}
	wire, err := json.Marshal(value)
	if err != nil {
		return dsl.Action{}, false
	}
	if strings.HasPrefix(strings.TrimSpace(action.Body), "[") || strings.HasPrefix(strings.TrimSpace(action.Body), "{") {
		action.Body = string(wire)
	} else {
		rewritten, ok := replaceCometdFormMessage(action.Body, preserveTemplateBraces(url.QueryEscape(string(wire))))
		if !ok {
			return dsl.Action{}, false
		}
		action.Body = rewritten
	}
	action.Name = ""
	metadata := map[string]any{}
	for key, value := range action.Metadata {
		switch key {
		case "har.pageref", "har.server_ip_address", "har.timings", "har.cache", "har.request.query_string", "har.response.cookies", "har.response.http_version":
			continue
		case "salesforce":
			if source, ok := value.(map[string]any); ok {
				if len(source) > maxCometdConnectTokens {
					return dsl.Action{}, false
				}
				remaining := map[string]any{}
				for field, item := range source {
					switch field {
					case "classification", "flow_kind", "object", "aura_descriptor", "sequence", "har_match_id":
						continue
					}
					remaining[field] = item
				}
				if len(remaining) > 0 {
					metadata[key] = remaining
				}
				continue
			}
		}
		metadata[key] = value
	}
	if len(metadata) == 0 {
		action.Metadata = nil
	} else {
		action.Metadata = metadata
	}
	nodes, bytes := 16384, 256<<10
	if !boundedCometdComparisonValue(reflect.ValueOf(action), &nodes, &bytes, 0) {
		return dsl.Action{}, false
	}
	return action, true
}

func boundedCometdComparisonValue(value reflect.Value, nodes, bytes *int, depth int) bool {
	if depth > 64 || *nodes <= 0 {
		return false
	}
	*nodes -= 1
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		return value.IsNil() || boundedCometdComparisonValue(value.Elem(), nodes, bytes, depth+1)
	case reflect.String:
		if value.Len() > *bytes {
			return false
		}
		*bytes -= value.Len()
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if !boundedCometdComparisonValue(value.Field(index), nodes, bytes, depth+1) {
				return false
			}
		}
	case reflect.Map:
		if value.Len() > *nodes {
			return false
		}
		for entries := value.MapRange(); entries.Next(); {
			if !boundedCometdComparisonValue(entries.Key(), nodes, bytes, depth+1) || !boundedCometdComparisonValue(entries.Value(), nodes, bytes, depth+1) {
				return false
			}
		}
	case reflect.Array, reflect.Slice:
		if value.Len() > *nodes {
			return false
		}
		for index := 0; index < value.Len(); index++ {
			if !boundedCometdComparisonValue(value.Index(index), nodes, bytes, depth+1) {
				return false
			}
		}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64, reflect.Invalid:
	default:
		return false
	}
	return true
}

// Admission already proved a unique bounded message field. Replacing just its
// raw value span keeps all other form fields, spelling, order and duplicates.
func replaceCometdFormMessage(body, encodedMessage string) (string, bool) {
	if _, ok := cometdConnectMessageBody(body); !ok {
		return "", false
	}
	for offset, rest := 0, body; ; {
		field, tail, more := strings.Cut(rest, "&")
		key, _, hasValue := strings.Cut(field, "=")
		decodedKey, err := url.QueryUnescape(key)
		if err == nil && decodedKey == "message" && hasValue {
			start, end := offset+len(key)+1, offset+len(field)
			return body[:start] + encodedMessage + body[end:], true
		}
		if !more {
			return "", false
		}
		offset += len(field) + 1
		rest = tail
	}
}
