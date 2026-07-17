package enterprisearchive

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
)

const (
	maskedBodySecretArgKey = "__masked_body_secret"
	maskedBodyFuncArgKey   = "__masked_body_function"
)

func secretNameForLRUnmaskLocation(fn *ParsedFunction) string {
	if fn == nil {
		return ""
	}
	// The logical reference is public, so bind it only to public source
	// structure. Hashing the opaque lr_unmask payload would turn the reference
	// into a dictionary oracle for protected source material.
	digest := sha256.New()
	for _, part := range []string{
		fn.Filename,
		fn.Phase,
		fmt.Sprintf("%d", fn.LineNumber),
		fmt.Sprintf("%d", fn.EndLine),
		fn.Name,
		fn.StepName,
	} {
		_, _ = fmt.Fprintf(digest, "%d:", len(part))
		_, _ = digest.Write([]byte(part))
	}
	sum := digest.Sum(nil)
	return fmt.Sprintf("lr_unmask_%x", sum[:8])
}

func maskedBodySecretReference(secretName string) string {
	return "{{secret." + secretName + "}}"
}

func applyMaskedBodyReference(fn *ParsedFunction) {
	if fn == nil {
		return
	}
	if secretName, ok := findLRUnmaskPayload(fn); ok {
		if fn.Args == nil {
			fn.Args = make(map[string]string)
		}
		fn.Args[maskedBodySecretArgKey] = secretName
		fn.Args[maskedBodyFuncArgKey] = "lr_unmask"
	}
}

func findLRUnmaskPayload(fn *ParsedFunction) (secretName string, ok bool) {
	if fn == nil {
		return "", false
	}
	for _, candidate := range []string{fn.Body, fn.Args["Body"]} {
		if _, found := extractLRUnmaskPayload(candidate); found {
			return secretNameForLRUnmaskLocation(fn), true
		}
	}
	for _, arg := range fn.CallArgs {
		if !strings.EqualFold(strings.TrimSpace(arg.NestedCall), "lr_unmask") && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(arg.Value)), "lr_unmask") {
			continue
		}
		if _, found := extractLRUnmaskPayload(convertutil.FirstNonEmpty(arg.Value, arg.Raw)); found {
			return secretNameForLRUnmaskLocation(fn), true
		}
	}
	return "", false
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
