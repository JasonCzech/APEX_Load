package enterprisearchive

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/strftime"
)

// parameterHelperReviewReasonCode gates a parameter helper call whose
// options APEX does not reproduce (a hexadecimal length, a date format with
// unsupported specifiers).
const parameterHelperReviewReasonCode = "parameter_helper_unsupported"

// sourceParameterHelperReview returns a gate for web_save_param_length and
// web_convert_date_param calls the runtime cannot reproduce exactly.
func sourceParameterHelperReview(fn ParsedFunction) (string, string) {
	options := map[string]string{}
	for _, arg := range sourceControlArgs(fn) {
		if key, value, ok := strings.Cut(arg, "="); ok {
			options[strings.ToLower(strings.TrimSpace(key))] = value
		}
	}
	switch fn.Name {
	case "web_save_param_length":
		if base := strings.TrimSpace(options["base"]); base != "" && !strings.EqualFold(base, "decimal") {
			return parameterHelperReviewReasonCode, fmt.Sprintf("web_save_param_length saves the length with Base=%s; APEX saves decimal lengths only.", base)
		}
	case "web_convert_date_param":
		for _, key := range []string{"sourceformat", "targetformat"} {
			format, present := options[key]
			if !present || strings.TrimSpace(format) == "" {
				return parameterHelperReviewReasonCode, fmt.Sprintf("web_convert_date_param has no %s; the conversion cannot be reproduced.", map[string]string{"sourceformat": "SourceFormat", "targetformat": "TargetFormat"}[key])
			}
			if !strftime.Supported(format) {
				return parameterHelperReviewReasonCode, fmt.Sprintf("web_convert_date_param uses the date format %q, which has a specifier APEX does not support.", format)
			}
		}
	}
	return "", ""
}
