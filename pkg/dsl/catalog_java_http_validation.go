package dsl

import (
	"fmt"
	"strings"
)

func validateJavaHTTPScriptIssues(script *Script) []string {
	var issues []string
	javaCfg := script.Config.JavaHTTP
	if strings.TrimSpace(javaCfg.Codec) == "" {
		issues = append(issues, "config.java_http.codec is required")
	} else if javaCfg.Codec != "java_serialized_object_base64" {
		issues = append(issues, fmt.Sprintf("config.java_http.codec %q is not supported", javaCfg.Codec))
	}
	if javaCfg.HelperMode != "" && !isAllowedValue(javaCfg.HelperMode, "embedded") {
		issues = append(issues, "config.java_http.helper_mode must be embedded; sidecar is not wired for java_http")
	}
	if javaCfg.BodyEncoding != "" && !isAllowedValue(javaCfg.BodyEncoding, "base64") {
		issues = append(issues, "config.java_http.body_encoding must be base64")
	}
	if strings.TrimSpace(javaCfg.ObjectSchema) != "" {
		issues = append(issues, "config.java_http.object_schema is not supported by embedded java_http")
	}
	if len(javaCfg.RequestMapping) > 0 {
		issues = append(issues, "config.java_http.request_mapping is not supported by embedded java_http")
	}
	if strings.TrimSpace(javaCfg.ExtractorType) != "" {
		issues = append(issues, "config.java_http.extractor_type is not supported by embedded java_http")
	}
	return issues
}
