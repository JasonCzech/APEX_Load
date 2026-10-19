package scrub

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/credential"
)

// SensitiveName reports whether name denotes credential material. It accepts
// common structured notations (for example credentials[client_secret] and
// auth.private_key), including percent-encoded names, while deliberately using
// exact normalized components to avoid substring false positives.
func SensitiveName(name string) bool {
	return credential.SensitiveName(name)
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
	// Charge every visited node, including empty containers. Charging only
	// primitive leaves lets a map or nested slice containing empty containers
	// traverse arbitrarily many attacker-controlled entries without consuming
	// the advertised budget.
	budget.entries--
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
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
		return Redacted, true
	}
	if value.Type() == reflect.TypeOf(time.Time{}) {
		return value.Interface().(time.Time).UTC().Format(time.RFC3339Nano), true
	}
	switch value.Kind() {
	case reflect.String:
		raw := truncateStructuredString(value.String(), maxStructuredStringBytes)
		return Value(raw, policy), true
	case reflect.Bool:
		return value.Bool(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint(), true
	case reflect.Float32, reflect.Float64:
		return value.Float(), true
	case reflect.Map:
		if value.IsNil() {
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

// publicCacheKeyParams are query parameters whose values are public content
// hashes, not credentials: Salesforce Aura's application and component-
// definition cache keys (/auraCmpDef?_au=4385_ZsFss-efN4RuL6razjopzA), served
// identically to every user of an org. They are long mixed-case base64url
// runs, so the credential-shape heuristic would otherwise flag every one.
var publicCacheKeyParams = map[string]bool{"_au": true, "_uid": true}

// auraCacheKeyValue is the exact shape of an Aura cache key: a numeric
// version, an underscore and a 22-character base64url hash.
var auraCacheKeyValue = regexp.MustCompile(`^[0-9]{1,10}_[A-Za-z0-9_-]{22}$`)

// PublicCacheKey reports whether a query parameter holds a public cache key
// the credential-shape heuristic must leave alone: an Aura cache-key name with
// a value of exactly that shape. Anything else under those names (a JWT, an
// e-mail address) is judged as usual, and SensitiveName always wins.
func PublicCacheKey(name, value string) bool {
	return publicCacheKeyParams[strings.TrimSpace(name)] && !SensitiveName(name) && auraCacheKeyValue.MatchString(value)
}
