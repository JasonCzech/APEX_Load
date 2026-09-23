package engine

// vuser_printf.go — split from vuser.go (same package, behavior-preserving).

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func (vu *VUser) logErrorMessageAction(ctx context.Context, action dsl.Action) {
	message := vu.renderErrorMessage(action)
	if vu.Script != nil && vu.Script.Config.ErrorHandling.FailTransactionOnErrorMessage != nil && *vu.Script.Config.ErrorHandling.FailTransactionOnErrorMessage {
		for _, state := range vu.sourceTransactions {
			vu.setSourceTransactionStatus(state, sourceTransactionFail)
		}
	}
	vu.runtimeLogger().ErrorContext(ctx, "source error message",
		"vuser", vu.ID,
		"group", vu.Group,
		"action", action.Name,
		"message", message,
	)
}

func (vu *VUser) renderErrorMessage(action dsl.Action) string {
	format := action.Value
	if format == "" {
		if paramsFormat, ok := actionParamString(action.Params, "format"); ok {
			format = paramsFormat
		}
	}
	format = vu.interpolate(format)

	args := errorMessageArgs(action.Params)
	if len(args) == 0 {
		if format != "" {
			return scrubSensitiveTextWithConfig(format, vu.RedactionConfig)
		}
		return scrubSensitiveTextWithConfig(action.Name, vu.RedactionConfig)
	}
	values := make([]any, 0, len(args))
	for _, arg := range args {
		values = append(values, vu.interpolateAny(arg))
	}
	return scrubSensitiveTextWithConfig(renderPrintfLikeMessage(format, values), vu.RedactionConfig)
}

func errorMessageArgs(params map[string]any) []any {
	if params == nil {
		return nil
	}
	raw, ok := params["args"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []any:
		return typed
	case []string:
		args := make([]any, len(typed))
		for i, value := range typed {
			args[i] = value
		}
		return args
	default:
		return []any{typed}
	}
}

func renderPrintfLikeMessage(format string, args []any) string {
	if format == "" {
		parts := make([]string, 0, len(args))
		for _, arg := range args {
			parts = append(parts, fmt.Sprint(arg))
		}
		return strings.Join(parts, " ")
	}

	var out strings.Builder
	argIndex := 0
	for i := 0; i < len(format); i++ {
		ch := format[i]
		if ch != '%' || i == len(format)-1 {
			out.WriteByte(ch)
			continue
		}
		next := format[i+1]
		if next == '%' {
			out.WriteByte('%')
			i++
			continue
		}
		specEnd := printfSpecEnd(format, i+1)
		if specEnd < 0 {
			out.WriteByte(ch)
			continue
		}
		if argIndex < len(args) {
			out.WriteString(formatPrintfArg(format[i:specEnd+1], args[argIndex]))
			argIndex++
			i = specEnd
			continue
		}
		out.WriteByte(ch)
	}
	if argIndex < len(args) {
		for _, arg := range args[argIndex:] {
			if out.Len() > 0 {
				out.WriteByte(' ')
			}
			out.WriteString(fmt.Sprint(arg))
		}
	}
	return out.String()
}

func printfSpecEnd(format string, start int) int {
	for i := start; i < len(format); i++ {
		ch := format[i]
		if isPrintfModifier(ch) {
			continue
		}
		if isPrintfVerb(ch) {
			return i
		}
		return -1
	}
	return -1
}

func isPrintfModifier(ch byte) bool {
	if ch >= '0' && ch <= '9' {
		return true
	}
	return strings.ContainsRune("#0+-.hlLjztI", rune(ch))
}

func formatPrintfArg(spec string, arg any) string {
	if spec == "" {
		return fmt.Sprint(arg)
	}
	verb := spec[len(spec)-1]
	goSpec := normalizePrintfSpec(spec)
	value, ok := normalizePrintfValue(verb, arg)
	if !ok {
		return fmt.Sprint(arg)
	}
	return fmt.Sprintf(goSpec, value)
}

func normalizePrintfSpec(spec string) string {
	if len(spec) < 2 {
		return spec
	}
	verb := spec[len(spec)-1]
	body := stripCPrintfLengthModifiers(spec[1 : len(spec)-1])
	switch verb {
	case 'i', 'u':
		verb = 'd'
	}
	return "%" + body + string(verb)
}

func stripCPrintfLengthModifiers(body string) string {
	for {
		switch {
		case strings.HasSuffix(body, "I64"):
			body = strings.TrimSuffix(body, "I64")
		case strings.HasSuffix(body, "ll"), strings.HasSuffix(body, "hh"):
			body = body[:len(body)-2]
		case body != "" && strings.ContainsRune("hlLjzt", rune(body[len(body)-1])):
			body = body[:len(body)-1]
		default:
			return body
		}
	}
}

func normalizePrintfValue(verb byte, arg any) (any, bool) {
	switch verb {
	case 'v', 'T':
		return arg, true
	case 's', 'q':
		return fmt.Sprint(arg), true
	case 't':
		return printfBoolValue(arg)
	case 'c':
		return printfRuneValue(arg)
	case 'd', 'i':
		return printfSignedValue(arg)
	case 'u', 'b', 'o', 'O', 'x', 'X', 'U':
		return printfUnsignedValue(arg)
	case 'e', 'E', 'f', 'F', 'g', 'G':
		return printfFloatValue(arg)
	case 'p':
		return fmt.Sprint(arg), false
	default:
		return fmt.Sprint(arg), false
	}
}

func printfBoolValue(arg any) (any, bool) {
	switch value := arg.(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		return parsed, err == nil
	default:
		return arg, false
	}
}

func printfRuneValue(arg any) (any, bool) {
	switch value := arg.(type) {
	case rune:
		return value, true
	case byte:
		return rune(value), true
	case string:
		if value == "" {
			return arg, false
		}
		return []rune(value)[0], true
	default:
		return printfSignedValue(arg)
	}
}

func printfSignedValue(arg any) (any, bool) {
	switch value := arg.(type) {
	case int:
		return value, true
	case int8:
		return value, true
	case int16:
		return value, true
	case int32:
		return value, true
	case int64:
		return value, true
	case uint:
		return value, true
	case uint8:
		return value, true
	case uint16:
		return value, true
	case uint32:
		return value, true
	case uint64:
		return value, true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed, err == nil
	default:
		return arg, false
	}
}

func printfUnsignedValue(arg any) (any, bool) {
	switch value := arg.(type) {
	case uint:
		return value, true
	case uint8:
		return value, true
	case uint16:
		return value, true
	case uint32:
		return value, true
	case uint64:
		return value, true
	case int:
		return value, true
	case int8:
		return value, true
	case int16:
		return value, true
	case int32:
		return value, true
	case int64:
		return value, true
	case string:
		trimmed := strings.TrimSpace(value)
		parsed, err := strconv.ParseUint(trimmed, 10, 64)
		if err == nil {
			return parsed, true
		}
		signed, signedErr := strconv.ParseInt(trimmed, 10, 64)
		return signed, signedErr == nil
	default:
		return arg, false
	}
}

func printfFloatValue(arg any) (any, bool) {
	switch value := arg.(type) {
	case float32:
		return value, true
	case float64:
		return value, true
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return parsed, err == nil
	default:
		return arg, false
	}
}

func isPrintfVerb(ch byte) bool {
	return strings.ContainsRune("vTtbcdiuoOqxXUeEfFgGsp", rune(ch))
}
