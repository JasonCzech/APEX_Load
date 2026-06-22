package sample

import (
	"encoding/base64"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func detectJavaHTTPConfig(exchanges []exchange) (dsl.JavaHTTPConfig, bool) {
	for _, ex := range exchanges {
		contentType := headerLookup(ex.Headers, "Content-Type")
		accept := headerLookup(ex.Headers, "Accept")
		if isJavaHTTPExchange(ex, contentType) {
			if !isJavaSerializedContentType(contentType) {
				contentType = "application/x-java-serialized-object"
			}
			if strings.TrimSpace(accept) == "" {
				accept = contentType
			}
			return dsl.JavaHTTPConfig{
				Codec:                "java_serialized_object_base64",
				HelperMode:           "embedded",
				ContentType:          contentType,
				Accept:               accept,
				BodyEncoding:         "base64",
				PreserveBinaryResult: true,
			}, true
		}
	}
	return dsl.JavaHTTPConfig{}, false
}

func isJavaHTTPExchange(ex exchange, contentType string) bool {
	if isJavaSerializedContentType(contentType) {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(ex.BodyEncoding), "base64") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ex.Body))
	return err == nil && len(decoded) >= 2 && decoded[0] == 0xac && decoded[1] == 0xed
}

func isJavaSerializedContentType(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "application/x-java-serialized-object")
}
