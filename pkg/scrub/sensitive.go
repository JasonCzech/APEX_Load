package scrub

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// sensitiveNames is the single normalized vocabulary used by body, URL,
// structured-value, runtime projection, and archive scrubbers. Names are
// normalized by percent-decoding and removing separators, so client_secret,
// client-secret, clientSecret, and client%5Fsecret have the same meaning.
// Entries are exact: a non-secret name such as "author" does not match "auth".
var sensitiveNames = map[string]struct{}{
	"accesskey": {}, "accesskeyid": {}, "accesstoken": {}, "accountkey": {}, "acktoken": {}, "apikey": {},
	"auth": {}, "authentication": {}, "authorization": {}, "basicauth": {},
	"clientsecret": {}, "connectionstring": {}, "cookie": {}, "csrf": {},
	"csrftoken": {}, "deliverytag": {}, "dsn": {}, "idsecret": {},
	"idtoken": {}, "jmssettlementtoken": {}, "locktoken": {}, "password": {},
	"passwd": {}, "privatekey": {}, "proxyauthorization": {}, "receipthandle": {},
	"refreshtoken": {}, "sas": {}, "saskey": {}, "sassignature": {}, "secret": {},
	"secretaccesskey": {}, "sharedaccesskey": {}, "sharedaccesssignature": {},
	"session": {}, "sessionid": {}, "sessiontoken": {}, "sid": {},
	"settlementtoken": {}, "signature": {}, "token": {}, "xsrf": {},
	"xsrftoken": {},
}

// SensitiveName reports whether name denotes credential material. It accepts
// common structured notations (for example credentials[client_secret] and
// auth.private_key), including percent-encoded names, while deliberately using
// exact normalized components to avoid substring false positives.
func SensitiveName(name string) bool {
	name = decodeName(name)
	if name == "" {
		return false
	}
	if _, ok := sensitiveNames[normalizeName(name)]; ok {
		return true
	}
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
	for start := range parts {
		var combined string
		for end := start; end < len(parts) && end < start+4; end++ {
			combined += normalizeName(parts[end])
			if _, ok := sensitiveNames[combined]; ok {
				return true
			}
		}
	}
	return false
}

func decodeName(name string) string {
	name = strings.TrimSpace(name)
	// A small fixed number handles double-encoded form keys without allowing an
	// attacker to turn decoding into an unbounded loop.
	for range 3 {
		decoded, err := url.QueryUnescape(name)
		if err != nil || decoded == name {
			break
		}
		name = decoded
	}
	return strings.TrimSpace(name)
}

func normalizeName(name string) string {
	name = decodeName(name)
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// NamedValue scrubs a value with awareness of its field name. A recognized
// sensitive name is redacted even when the value is short or low entropy.
func NamedValue(name, value string, policy Policy) string {
	if SensitiveName(name) {
		return Redacted
	}
	return Value(value, policy)
}

// StringMap returns a detached, key-aware scrubbed copy.
func StringMap(values map[string]string, policy Policy) map[string]string {
	if len(values) == 0 {
		return values
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = NamedValue(key, value, policy)
	}
	return out
}

const (
	maxStructuredScrubDepth   = 16
	maxStructuredScrubEntries = 4096
	maxStructuredStringBytes  = 1 << 20
)

type structuredBudget struct {
	entries int
	seen    map[visit]struct{}
}

type visit struct {
	typ reflect.Type
	ptr uintptr
}

// Structured produces a detached, recursively scrubbed JSON-compatible value.
// It accepts heterogeneous maps, typed slices, pointers, and structs (using
// JSON field names). Traversal is bounded and cycle-aware so adapter-provided
// evidence cannot turn projection scrubbing into unbounded work.
func Structured(value any, policy Policy) any {
	budget := &structuredBudget{entries: maxStructuredScrubEntries, seen: make(map[visit]struct{})}
	out, ok := scrubReflectValue(reflect.ValueOf(value), "", policy, budget, 0)
	if !ok {
		return nil
	}
	return out
}

func scrubReflectValue(value reflect.Value, name string, policy Policy, budget *structuredBudget, depth int) (any, bool) {
	if budget == nil || budget.entries <= 0 || depth > maxStructuredScrubDepth || !value.IsValid() {
		return nil, false
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			budget.entries--
			return nil, true
		}
		if value.Kind() == reflect.Pointer {
			key := visit{typ: value.Type(), ptr: value.Pointer()}
			if _, exists := budget.seen[key]; exists {
				return nil, false
			}
			budget.seen[key] = struct{}{}
			defer delete(budget.seen, key)
		}
		value = value.Elem()
	}
	if SensitiveName(name) {
		budget.entries--
		return Redacted, true
	}
	if value.Type() == reflect.TypeOf(time.Time{}) {
		budget.entries--
		return value.Interface().(time.Time).UTC().Format(time.RFC3339Nano), true
	}
	switch value.Kind() {
	case reflect.String:
		budget.entries--
		raw := truncateStructuredString(value.String(), maxStructuredStringBytes)
		return Value(raw, policy), true
	case reflect.Bool:
		budget.entries--
		return value.Bool(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		budget.entries--
		return value.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		budget.entries--
		return value.Uint(), true
	case reflect.Float32, reflect.Float64:
		budget.entries--
		return value.Float(), true
	case reflect.Map:
		if value.IsNil() {
			budget.entries--
			return nil, true
		}
		out := make(map[string]any, min(value.Len(), budget.entries))
		iterator := value.MapRange()
		for budget.entries > 0 && iterator.Next() {
			mapKey := iterator.Key()
			keyName := mapKeyString(mapKey)
			if keyName == "" {
				continue
			}
			keyName = truncateStructuredString(keyName, 4<<10)
			item, ok := scrubReflectValue(iterator.Value(), keyName, policy, budget, depth+1)
			if ok {
				out[keyName] = item
			}
		}
		return out, true
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			budget.entries--
			return nil, true
		}
		if value.Kind() == reflect.Slice && value.Pointer() != 0 {
			key := visit{typ: value.Type(), ptr: value.Pointer()}
			if _, exists := budget.seen[key]; exists {
				return nil, false
			}
			budget.seen[key] = struct{}{}
			defer delete(budget.seen, key)
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			limit := min(value.Len(), maxStructuredStringBytes)
			data := make([]byte, limit)
			for index := range limit {
				data[index] = byte(value.Index(index).Uint())
			}
			budget.entries--
			return Value(string(data), policy), true
		}
		limit := min(value.Len(), budget.entries)
		out := make([]any, 0, limit)
		for index := 0; index < limit && budget.entries > 0; index++ {
			item, ok := scrubReflectValue(value.Index(index), "", policy, budget, depth+1)
			if ok {
				out = append(out, item)
			}
		}
		return out, true
	case reflect.Struct:
		out := make(map[string]any)
		typ := value.Type()
		for index := 0; index < value.NumField() && budget.entries > 0; index++ {
			field := typ.Field(index)
			if field.PkgPath != "" { // unexported
				continue
			}
			fieldName := jsonFieldName(field)
			if fieldName == "" || fieldName == "-" {
				continue
			}
			item, ok := scrubReflectValue(value.Field(index), fieldName, policy, budget, depth+1)
			if ok {
				out[fieldName] = item
			}
		}
		return out, true
	default:
		budget.entries--
		return Value(fmt.Sprint(value.Interface()), policy), true
	}
}

func mapKeyString(value reflect.Value) string {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return ""
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return ""
	}
	if value.Kind() == reflect.String {
		return value.String()
	}
	return fmt.Sprint(value.Interface())
}

func jsonFieldName(field reflect.StructField) string {
	name := strings.Split(field.Tag.Get("json"), ",")[0]
	if name == "" {
		name = field.Name
	}
	return name
}

func truncateStructuredString(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
