package sample

import (
	"mime"
	"strings"
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
