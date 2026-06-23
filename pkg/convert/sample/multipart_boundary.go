package sample

import (
	"mime"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	defaultCurlMultipartBoundary = "----apexload-curl-boundary"
	defaultHARMultipartBoundary  = "----apexload-har-boundary"
)

func multipartBoundaryOrDefault(contentType string, fallback string) string {
	if _, params, err := mime.ParseMediaType(strings.TrimSpace(contentType)); err == nil {
		if boundary := strings.TrimSpace(params["boundary"]); boundary != "" {
			return boundary
		}
	}
	return fallback
}

func ensureMultipartContentTypeBoundary(headers map[string]string, boundary string) {
	if headers == nil {
		return
	}
	key := "Content-Type"
	contentType := headerLookup(headers, key)
	for existingKey := range headers {
		if strings.EqualFold(existingKey, key) {
			key = existingKey
			break
		}
	}
	if strings.TrimSpace(contentType) == "" {
		headers[key] = "multipart/form-data; boundary=" + boundary
		return
	}
	if !strings.Contains(strings.ToLower(contentType), "multipart/form-data") {
		return
	}
	if _, params, err := mime.ParseMediaType(contentType); err == nil && strings.TrimSpace(params["boundary"]) != "" {
		return
	}
	headers[key] = strings.TrimRight(contentType, " ;") + "; boundary=" + boundary
}

// reconcileMultipartBoundaryInList syncs the ordered HeaderList's multipart
// Content-Type to the (boundary-corrected) value in the headers map. Replay
// prefers HeaderList over the map (pkg/protocol/http builds request headers from
// action.HeaderList), so without this an import whose recorded Content-Type
// lacked a boundary keeps the bare value in HeaderList and replays as an
// unparseable multipart body even though the map was fixed. It updates an
// existing Content-Type entry in place (any casing) or appends one if absent.
func reconcileMultipartBoundaryInList(list dsl.HeaderList, headers map[string]string) dsl.HeaderList {
	contentType := headerLookup(headers, "Content-Type")
	if !strings.Contains(strings.ToLower(contentType), "multipart/form-data") {
		return list
	}
	updated := false
	for i := range list {
		if strings.EqualFold(list[i].Name, "Content-Type") {
			list[i].Value = contentType
			updated = true
		}
	}
	if !updated {
		list = append(list, dsl.HeaderEntry{Name: "Content-Type", Value: contentType})
	}
	return list
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
