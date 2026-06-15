package sample

// rest.go — split from converter.go (same package, behavior-preserving).

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func parseRESTJSON(content string) ([]exchange, []string, error) {
	var payload any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil, nil, fmt.Errorf("failed to parse JSON sample: %w", err)
	}
	items, err := extractRESTItems(payload)
	if err != nil {
		return nil, nil, err
	}
	exchanges := make([]exchange, 0, len(items))
	for _, item := range items {
		ex, err := parseRESTItem(item)
		if err != nil {
			return nil, nil, err
		}
		exchanges = append(exchanges, ex)
	}
	return exchanges, nil, nil
}

func extractRESTItems(payload any) ([]map[string]any, error) {
	switch value := payload.(type) {
	case []any:
		items := make([]map[string]any, 0, len(value))
		for _, item := range value {
			mapped, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("REST JSON array entries must be objects")
			}
			items = append(items, mapped)
		}
		return items, nil
	case map[string]any:
		if requests, ok := value["requests"].([]any); ok {
			items := make([]map[string]any, 0, len(requests))
			for _, item := range requests {
				mapped, ok := item.(map[string]any)
				if !ok {
					return nil, errors.New("REST JSON requests entries must be objects")
				}
				items = append(items, mapped)
			}
			return items, nil
		}
		return []map[string]any{value}, nil
	default:
		return nil, errors.New("REST JSON must be an object or array")
	}
}

func parseRESTItem(item map[string]any) (exchange, error) {
	requestData := item
	if nested, ok := item["request"].(map[string]any); ok {
		requestData = nested
	}
	ex := exchange{
		Method:  strings.ToUpper(stringValue(requestData["method"])),
		URL:     stringValue(requestData["url"]),
		Headers: mapValueToStringMap(requestData["headers"]),
		Body:    bodyValue(requestData["body"]),
	}
	if ex.Method == "" || ex.URL == "" {
		return exchange{}, errors.New("REST JSON request entries must include method and url")
	}
	if responseData, ok := item["response"].(map[string]any); ok {
		ex.Response = &response{
			Status:  intValue(responseData["status"]),
			Headers: mapValueToStringMap(responseData["headers"]),
			Body:    bodyValue(responseData["body"]),
		}
	}
	return ex, nil
}
