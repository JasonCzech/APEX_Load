package correlate

import (
	"strconv"
	"strings"
)

// setCookieCaptureSuffix ends every Set-Cookie extractor that
// setCookieExtractorForOccurrence writes, after the quoted cookie name.
const setCookieCaptureSuffix = `=[ \t]*"?([^";\s,]+)"?(?:[ \t]*;|[ \t]*$)`

// GeneratedSetCookieExtractor reports whether expression is exactly a
// Set-Cookie extractor this package generates, and returns the cookie name
// it reads. Such an expression holds no recorded value: the name is an
// identifier (Salesforce's __Host-ERIC_PROD<digits> is long enough to look
// like a token) and the value is what the regex captures. It is recognized
// by regenerating it, so any other text, however similar, is not.
func GeneratedSetCookieExtractor(expression string) (cookieName string, ok bool) {
	if !strings.HasSuffix(expression, setCookieCaptureSuffix) {
		return "", false
	}
	capture := strings.LastIndex(expression, `^(?i:`)
	if capture < 0 {
		return "", false
	}
	headerAndName := expression[capture+len(`^(?i:`) : len(expression)-len(setCookieCaptureSuffix)]
	quotedHeader, quotedName, found := strings.Cut(headerAndName, `):[ \t]*`)
	if !found {
		return "", false
	}
	header, headerOK := unquoteRegexMeta(quotedHeader)
	name, nameOK := unquoteRegexMeta(quotedName)
	if !headerOK || !nameOK || !strings.EqualFold(header, "set-cookie") || name == "" {
		return "", false
	}
	ordinal := 0
	if open := strings.LastIndex(expression[:capture], "){"); open >= 0 && strings.HasSuffix(expression[:capture], "}") {
		parsed, err := strconv.Atoi(expression[open+2 : capture-1])
		if err != nil {
			return "", false
		}
		ordinal = parsed
	}
	regenerated, valid := setCookieExtractorForOccurrence(header, name, ordinal)
	if !valid || regenerated != expression {
		return "", false
	}
	return name, true
}

// unquoteRegexMeta reverses regexp.QuoteMeta: every backslash escapes the
// one character after it, and only QuoteMeta's special characters are ever
// escaped.
func unquoteRegexMeta(quoted string) (string, bool) {
	var out strings.Builder
	for index := 0; index < len(quoted); index++ {
		if quoted[index] != '\\' {
			out.WriteByte(quoted[index])
			continue
		}
		index++
		if index >= len(quoted) || !strings.ContainsRune(`\.+*?()|[]{}^$`, rune(quoted[index])) {
			return "", false
		}
		out.WriteByte(quoted[index])
	}
	return out.String(), true
}
