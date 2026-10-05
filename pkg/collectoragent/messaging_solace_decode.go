package collectoragent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/analysis"
)

const (
	solaceManagementBodyLimit = 1 << 20
	solaceManagementJSONDepth = 32
	solaceManagementJSONNodes = 8192
	solaceManagementJSONText  = 4096
)

func solaceManagementDecodeError(reason string) error {
	return &managementObservationError{reason: reason}
}

// Only the selected individual entity can become evidence. Unknown bounded
// provider fields are ignored; no response URI or collection can cause traffic.
func decodeSolaceManagementDescription(ctx context.Context, body []byte, route solaceManagementRoute, scope analysis.ManagementScope) (analysis.SolaceSEMP, error) {
	result := analysis.SolaceSEMP{Depth: kafkaManagementUnavailableInteger("not_observed")}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(body) > solaceManagementBodyLimit {
		return result, solaceManagementDecodeError("budget_exhausted")
	}
	if scope.Validate() != nil || scope.Kind != analysis.ManagementKindSolace || route.entity.Solace == nil {
		return result, solaceManagementDecodeError("permission_denied")
	}
	id, err := route.entity.ID()
	if err != nil {
		return result, solaceManagementDecodeError("permission_denied")
	}
	approved := false
	for _, entity := range scope.Entities {
		known, _ := entity.ID()
		approved = approved || known == id
	}
	origin, host, port, originErr := canonicalSolaceOrigin(route.origin)
	address, network, addressErr := solaceStaticAddress(route.address.String(), port)
	expected, routeErr := buildSolaceManagementRoute(origin, host, address, network, route.entity)
	if !approved || originErr != nil || addressErr != nil || routeErr != nil || scope.Provider != host || !solaceSameRoute(expected, route) {
		return result, solaceManagementDecodeError("permission_denied")
	}
	root, err := decodeSolaceManagementDocument(ctx, body)
	if err != nil {
		return result, err
	}
	if err := validateSolaceManagementMeta(root, "GET"); err != nil {
		return result, err
	}
	data, ok := root["data"].(map[string]any)
	if !ok || data["msgVpnName"] != route.entity.Solace.VPN {
		return result, solaceManagementDecodeError("identity_mismatch")
	}
	field, name := "queueName", route.entity.Solace.Queue
	if name == "" {
		field, name = "topicEndpointName", route.entity.Solace.TopicEndpoint
	}
	if data[field] != name {
		return result, solaceManagementDecodeError("identity_mismatch")
	}
	otherField := "topicEndpointName"
	if field == "topicEndpointName" {
		otherField = "queueName"
	}
	if data[otherField] != nil {
		return result, solaceManagementDecodeError("identity_mismatch")
	}
	if relationship, exists := data["deadMsgQueue"]; exists && relationship != nil {
		dmq, ok := relationship.(string)
		if !ok || len(dmq) > 256 || dmq != "" && !managementLabel(dmq, 256) {
			return result, solaceManagementDecodeError("invalid")
		}
		for _, entity := range scope.Entities {
			if entity.Solace != nil && entity.Solace.VPN == route.entity.Solace.VPN && entity.Solace.Queue == dmq && dmq != "" {
				result.DeadMessageQueue = dmq
			}
		}
	}
	if collections, exists := root["collections"]; exists && collections != nil {
		object, ok := collections.(map[string]any)
		if !ok {
			return result, solaceManagementDecodeError("invalid")
		}
		if messages, exists := object["msgs"]; exists && messages != nil {
			object, ok := messages.(map[string]any)
			if !ok {
				return result, solaceManagementDecodeError("invalid")
			}
			if count, exists := object["count"]; exists && count != nil {
				depth, err := solaceJSONInteger(count)
				if err != nil {
					return result, err
				}
				result.Depth, err = analysis.ObservedManagementInteger(depth)
				if err != nil {
					return result, solaceManagementDecodeError("invalid")
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := result.Validate(); err != nil {
		return result, solaceManagementDecodeError("invalid")
	}
	return result, nil
}

// A successful HTTP status alone cannot prove own-session logout. Only this
// fixed action's successful provider envelope supplies logical session proof.
func decodeSolaceManagementLogout(ctx context.Context, body []byte) error {
	root, err := decodeSolaceManagementDocument(ctx, body)
	if err != nil {
		return err
	}
	if err := validateSolaceManagementMeta(root, "PUT"); err != nil {
		return err
	}
	return ctx.Err()
}

func decodeSolaceManagementDocument(ctx context.Context, body []byte) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(body) > solaceManagementBodyLimit {
		return nil, solaceManagementDecodeError("budget_exhausted")
	}
	if !utf8.Valid(body) || !solaceJSONUnicodeValid(body) {
		return nil, solaceManagementDecodeError("invalid")
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '<' {
		return nil, solaceManagementDecodeError("unsupported")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	nodes := 0
	value, err := readSolaceManagementJSON(ctx, decoder, 0, &nodes)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, solaceManagementDecodeError("invalid")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, solaceManagementDecodeError("invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return root, nil
}
func validateSolaceManagementMeta(root map[string]any, method string) error {
	meta, ok := root["meta"].(map[string]any)
	if !ok {
		return solaceManagementDecodeError("invalid")
	}
	if failure := meta["error"]; failure != nil {
		object, ok := failure.(map[string]any)
		if !ok {
			return solaceManagementDecodeError("invalid")
		}
		code, err := solaceJSONInteger(object["code"])
		if err != nil || code > 2147483647 {
			return solaceManagementDecodeError("invalid")
		}
		reason := "invalid"
		switch code {
		case 6:
			reason = "not_observed"
		case 14:
			reason = "unsupported"
		case 72:
			reason = "permission_denied"
		}
		return solaceManagementDecodeError(reason)
	}
	if root["error"] != nil || meta["paging"] != nil {
		return solaceManagementDecodeError("invalid")
	}
	code, err := solaceJSONInteger(meta["responseCode"])
	if err != nil || code != 200 {
		return solaceManagementDecodeError("invalid")
	}
	request, ok := meta["request"].(map[string]any)
	if !ok || request["method"] != method {
		return solaceManagementDecodeError("invalid")
	}
	return nil
}

func solaceJSONInteger(value any) (int64, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, solaceManagementDecodeError("invalid")
	}
	result, err := analysis.ParseManagementInteger(string(number))
	if err != nil {
		return 0, solaceManagementDecodeError("invalid")
	}
	return result, nil
}

func readSolaceManagementJSON(ctx context.Context, decoder *json.Decoder, depth int, nodes *int) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	*nodes++
	if depth > solaceManagementJSONDepth || *nodes > solaceManagementJSONNodes {
		return nil, solaceManagementDecodeError("budget_exhausted")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, solaceManagementDecodeError("invalid")
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, solaceManagementDecodeError("invalid")
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, solaceManagementDecodeError("invalid")
				}
				if len(key) > solaceManagementJSONText {
					return nil, solaceManagementDecodeError("budget_exhausted")
				}
				if _, duplicate := object[key]; duplicate {
					return nil, solaceManagementDecodeError("invalid")
				}
				next, err := readSolaceManagementJSON(ctx, decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				object[key] = next
			}
			closeToken, err := decoder.Token()
			if err != nil || closeToken != json.Delim('}') {
				return nil, solaceManagementDecodeError("invalid")
			}
			return object, nil
		case '[':
			var array []any
			for decoder.More() {
				next, err := readSolaceManagementJSON(ctx, decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				array = append(array, next)
			}
			closeToken, err := decoder.Token()
			if err != nil || closeToken != json.Delim(']') {
				return nil, solaceManagementDecodeError("invalid")
			}
			return array, nil
		default:
			return nil, solaceManagementDecodeError("invalid")
		}
	case string:
		if len(value) > solaceManagementJSONText {
			return nil, solaceManagementDecodeError("budget_exhausted")
		}
		return value, nil
	case json.Number:
		if len(value) > 32 {
			return nil, solaceManagementDecodeError("budget_exhausted")
		}
		return value, nil
	default:
		return token, nil
	}
}

// encoding/json substitutes U+FFFD for unpaired UTF-16 escapes. Reject that
// ambiguity before identities are compared; valid surrogate pairs remain data.
func solaceJSONUnicodeValid(body []byte) bool {
	inString := false
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '"':
			inString = !inString
		case '\\':
			if !inString {
				continue
			}
			i++
			if i >= len(body) {
				return false
			}
			if body[i] != 'u' {
				continue
			}
			if i+4 >= len(body) {
				return false
			}
			n, err := strconv.ParseUint(string(body[i+1:i+5]), 16, 16)
			if err != nil {
				return false
			}
			i += 4
			if n >= 0xdc00 && n <= 0xdfff {
				return false
			}
			if n < 0xd800 || n > 0xdbff {
				continue
			}
			if i+6 >= len(body) || body[i+1] != '\\' || body[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(body[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return !inString
}
