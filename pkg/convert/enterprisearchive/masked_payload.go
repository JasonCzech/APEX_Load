package enterprisearchive

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
)

const (
	maskedBodySecretArgKey = "__masked_body_secret"
	maskedBodyHashArgKey   = "__masked_body_hash"
	maskedBodyFuncArgKey   = "__masked_body_function"
)

func secretNameForLRUnmaskPayload(payload string) (name string, hash string) {
	sum := sha256.Sum256([]byte(strings.TrimSpace(payload)))
	hash = fmt.Sprintf("%x", sum[:])
	return "lr_unmask_" + hash[:16], hash
}

func maskedBodySecretReference(secretName string) string {
	return "{{secret." + secretName + "}}"
}

func applyMaskedBodyReference(fn *ParsedFunction) {
	if fn == nil {
		return
	}
	if secretName, hash, ok := findLRUnmaskPayload(fn); ok {
		if fn.Args == nil {
			fn.Args = make(map[string]string)
		}
		fn.Args[maskedBodySecretArgKey] = secretName
		fn.Args[maskedBodyHashArgKey] = hash
		fn.Args[maskedBodyFuncArgKey] = "lr_unmask"
	}
}

func findLRUnmaskPayload(fn *ParsedFunction) (secretName string, hash string, ok bool) {
	for _, candidate := range []string{fn.Body, fn.Args["Body"]} {
		if payload, found := extractLRUnmaskPayload(candidate); found {
			secretName, hash = secretNameForLRUnmaskPayload(payload)
			return secretName, hash, true
		}
	}
	for _, arg := range fn.CallArgs {
		if !strings.EqualFold(strings.TrimSpace(arg.NestedCall), "lr_unmask") && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(arg.Value)), "lr_unmask") {
			continue
		}
		if payload, found := extractLRUnmaskPayload(convertutil.FirstNonEmpty(arg.Value, arg.Raw)); found {
			secretName, hash = secretNameForLRUnmaskPayload(payload)
			return secretName, hash, true
		}
	}
	return "", "", false
}

func extractLRUnmaskPayload(value string) (string, bool) {
	value = strings.TrimSpace(cleanConfigValue(value))
	if value == "" || !strings.HasPrefix(strings.ToLower(value), "lr_unmask") {
		return "", false
	}
	open := strings.Index(value, "(")
	close := strings.LastIndex(value, ")")
	if open < 0 || close <= open {
		return "", false
	}
	args := splitCFunctionArgs(value[open+1 : close])
	if len(args) == 0 {
		return "", false
	}
	payload := strings.TrimSpace(cleanConfigValue(args[0]))
	if payload == "" {
		return "", false
	}
	return payload, true
}
