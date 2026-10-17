package importreview

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"sort"
	"strings"
)

func ImportCommitSurfaceBody(ref dslwalk.SurfaceRef) (string, bool) {
	if ref.Action != nil {
		return ref.Action.Body, strings.TrimSpace(ref.Action.Body) != ""
	}
	if ref.Request == nil {
		return "", false
	}
	body, ok := ref.Request["body"].(string)
	return body, ok && strings.TrimSpace(body) != ""
}

func SetImportCommitSurfaceBody(ref dslwalk.SurfaceRef, body string) {
	if ref.Action != nil {
		ref.Action.Body = body
		return
	}
	if ref.Request != nil {
		ref.Request["body"] = body
	}
}

func ImportCommitSurfaceContentType(ref dslwalk.SurfaceRef) string {
	if ref.Action != nil {
		if len(ref.Action.HeaderList) > 0 {
			value := ""
			for _, header := range ref.Action.HeaderList {
				if strings.EqualFold(strings.TrimSpace(header.Name), "Content-Type") {
					value = header.Value
				}
			}
			return value
		}
		return ImportCommitHeaderMapContentType(ref.Action.Headers)
	}
	if ref.Request == nil {
		return ""
	}
	if raw, exists := ref.Request["header_list"]; exists {
		payload, err := json.Marshal(raw)
		if err == nil {
			var headers dsl.HeaderList
			if json.Unmarshal(payload, &headers) == nil && len(headers) > 0 {
				value := ""
				for _, header := range headers {
					if strings.EqualFold(strings.TrimSpace(header.Name), "Content-Type") {
						value = header.Value
					}
				}
				return value
			}
		}
	}
	raw, exists := ref.Request["headers"]
	if !exists {
		return ""
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	var headers map[string]string
	if json.Unmarshal(payload, &headers) != nil {
		return ""
	}
	return ImportCommitHeaderMapContentType(headers)
}

func ImportCommitHeaderMapContentType(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	value := ""
	for _, key := range keys {
		if strings.EqualFold(strings.TrimSpace(key), "Content-Type") {
			value = headers[key]
		}
	}
	return value
}
