package paramize

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/jasonczech/apexload/pkg/credential"
)

// LooksDynamicValue reports whether a key/value pair looks like a dynamic
// runtime value (session token, CSRF nonce, long opaque identifier) rather
// than stable test data. Lifted from the browser-to-HTTP conversion heuristics
// so every importer shares one rule set.
func LooksDynamicValue(key string, value string) bool {
	lowerKey := strings.ToLower(key)
	if strings.Contains(lowerKey, "token") || strings.Contains(lowerKey, "session") || strings.Contains(lowerKey, "csrf") || strings.Contains(lowerKey, "nonce") {
		return true
	}
	value = strings.TrimSpace(value)
	if len(value) >= 24 {
		return true
	}
	if strings.Count(value, "-") >= 4 {
		return true
	}
	return false
}

// LooksParameterName reports whether a field name looks like business test
// data that should be bound to a data source so each VUser gets distinct
// values (user ids, emails, SKUs, account numbers, ...). The name is split
// into words at separators, case changes and digits, so user_id, userId,
// UserID and user-id read alike, and it is judged by its last word, as an
// English compound is: userId and accountNumber match, userProfileCard and
// browserUserAgent do not. A marker inside an unrelated word (valid_from,
// c_sid_1) never counts, and single-word compounds such as username count.
func LooksParameterName(key string) bool {
	words := parameterNameWords(key)
	for len(words) > 0 && strings.IndexFunc(words[len(words)-1], unicode.IsDigit) == 0 {
		words = words[:len(words)-1] // email2, address_1
	}
	switch {
	case len(words) == 0 || len(words) > maxParameterNameWords:
		return false // descriptor and path keys, not business fields
	case len(words) > 1 && parameterNameAccessors[words[0]]:
		return false // getDeviceId, isUser: methods and flags; x_...: header names
	}
	switch strings.Join(words, "") {
	case "sortorder", "displayorder":
		return false
	}
	head := words[len(words)-1]
	modifier := ""
	if len(words) > 1 {
		modifier = words[len(words)-2]
	}
	if head == "id" && runtimeIDModifiers[modifier] {
		return false // client_id, sessionId, x_request_id
	}
	if parameterNameMarkers[head] || parameterNameCompounds[modifier][head] {
		return true
	}
	for prefix, suffixes := range parameterNameCompounds {
		if strings.HasPrefix(head, prefix) && suffixes[head[len(prefix):]] {
			return true // username, accountnumber, zipcode
		}
	}
	return false
}

const maxParameterNameWords = 5

var (
	parameterNameMarkers = map[string]bool{"id": true, "user": true, "customer": true, "account": true, "order": true, "sku": true, "email": true, "zip": true}
	// Identifying words that complete a business noun: user_name, accountNo.
	parameterNameCompounds = map[string]map[string]bool{
		"user":     {"id": true, "ids": true, "name": true, "number": true, "num": true, "no": true},
		"login":    {"id": true, "name": true},
		"customer": {"id": true, "ids": true, "name": true, "number": true, "num": true, "no": true},
		"account":  {"id": true, "ids": true, "name": true, "number": true, "num": true, "no": true},
		"order":    {"id": true, "ids": true, "number": true, "num": true, "no": true},
		"email":    {"address": true},
		"zip":      {"code": true},
	}
	// Ids the client or server generates per session or request, or that
	// configure the application, rather than test data.
	runtimeIDModifiers = map[string]bool{
		"client": true, "app": true, "application": true, "tenant": true,
		"session": true, "ses": true, "sid": true, "request": true, "req": true, "trace": true, "span": true,
		"correlation": true, "conversation": true, "message": true, "msg": true,
		"nonce": true, "install": true, "instance": true, "action": true, "callback": true,
		"context": true, "local": true,
	}
	parameterNameAccessors = map[string]bool{"get": true, "set": true, "is": true, "has": true, "x": true}
)

// parameterNameWords lowercases key and splits it into words at
// non-alphanumeric separators, lower-to-upper case changes, the end of an
// acronym (SKUCode -> sku, code; a plural s stays: userIDs -> user, ids) and
// letter/digit boundaries.
func parameterNameWords(key string) []string {
	var words []string
	runes := []rune(strings.TrimSpace(key))
	start := -1
	flush := func(end int) {
		if start >= 0 && end > start {
			words = append(words, strings.ToLower(string(runes[start:end])))
		}
		start = -1
	}
	for index, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush(index)
			continue
		}
		if start >= 0 {
			previous := runes[index-1]
			switch {
			case unicode.IsUpper(r) && (unicode.IsLower(previous) || unicode.IsDigit(previous)),
				unicode.IsUpper(r) && unicode.IsUpper(previous) && index+1 < len(runes) && unicode.IsLower(runes[index+1]) && !acronymPlural(runes, index+1),
				unicode.IsDigit(r) != unicode.IsDigit(previous):
				flush(index)
			}
		}
		if start < 0 {
			start = index
		}
	}
	flush(len(runes))
	return words
}

// acronymPlural reports a lone lowercase s at index that ends its word, the
// plural of the acronym before it (IDs, SKUs).
func acronymPlural(runes []rune, index int) bool {
	return runes[index] == 's' && (index+1 == len(runes) || !unicode.IsLower(runes[index+1]))
}

// SensitiveHeaderKey reports whether a header carries credentials.
func SensitiveHeaderKey(key string) bool {
	return credential.SensitiveHeader(key)
}

// EnvKey converts a field or header name into the UPPER_SNAKE environment
// variable key used in {{env.KEY}} replacements.
func EnvKey(name string) string {
	return credential.EnvKey(name)
}

var selectorTokenPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_\-]*`)

// SelectorFieldHint derives a field-name hint from a browser selector so fill
// values can run through the same name heuristics as HTTP parameters, e.g.
// `#user_email` -> "user_email", `input[name="user_id"]` -> "user_id".
func SelectorFieldHint(selector string) string {
	// Selectors normally contain only a handful of tokens. Bound the result set
	// anyway: imported selector text is untrusted, and collecting every match in
	// a pathological value would bypass the shared parameterization work limits.
	if int64(len(selector)) > maxSuggestionRegexSearchBytes {
		return ""
	}
	tokens := selectorTokenPattern.FindAllString(selector, maxSuggestionRegexMatches+1)
	if len(tokens) > maxSuggestionRegexMatches {
		return ""
	}
	skip := map[string]bool{
		"input": true, "textarea": true, "select": true, "button": true,
		"form": true, "div": true, "span": true, "label": true,
		"name": true, "type": true, "data": true, "aria": true, "css": true, "xpath": true, "text": true,
	}
	for i := len(tokens) - 1; i >= 0; i-- {
		token := strings.ToLower(tokens[i])
		if !skip[token] {
			return token
		}
	}
	return ""
}
