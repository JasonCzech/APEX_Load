package engine

import (
	"container/heap"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
)

const (
	maxReplayDebugEvidenceBytes      = 256 << 10
	maxReplayDebugEvidenceEntries    = 4096
	maxReplayDebugEvidenceCollection = 512
	maxReplayDebugEvidenceDepth      = 6
	maxReplayStreamedChildren        = 128
	maxReplayStreamedBytes           = 1 << 20
)

type replayDebugEvidenceBudget struct{ bytes, entries, omitted int }

// Debug evidence has its own larger limits than normal publication. The count
// records each truncated value or omitted member, including unsupported types.
func cloneReplayDebugEvidence(input map[string]any, allowance int) (map[string]any, int) {
	if len(input) == 0 {
		return nil, 0
	}
	budget := &replayDebugEvidenceBudget{bytes: min(maxReplayDebugEvidenceBytes, max(0, allowance)), entries: maxReplayDebugEvidenceEntries}
	value, ok := cloneReplayDebugEvidenceValue(input, budget, 0)
	if !ok {
		return nil, budget.omitted
	}
	out, _ := value.(map[string]any)
	return out, budget.omitted
}

func cloneReplayDebugEvidenceValue(value any, budget *replayDebugEvidenceBudget, depth int) (any, bool) {
	if depth > maxReplayDebugEvidenceDepth || budget.entries <= 0 || budget.bytes <= 0 {
		budget.omitted++
		return nil, false
	}
	budget.entries--
	switch v := value.(type) {
	case nil, bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		budget.bytes -= min(budget.bytes, 16)
		return v, true
	case json.Number:
		if len(v) > min(budget.bytes, 128) {
			budget.omitted++
			return nil, false
		}
		if _, err := json.Marshal(v); err != nil {
			budget.omitted++
			return nil, false
		}
		budget.bytes -= len(v)
		return json.Number(strings.Clone(string(v))), true
	case string:
		if len(v) > budget.bytes {
			v = boundedtext.TruncateUTF8(v, budget.bytes)
			budget.omitted++
		}
		budget.bytes -= len(v)
		return strings.Clone(v), true
	case map[string]string:
		keys := boundedtext.SmallestStringKeys(v, maxReplayDebugEvidenceCollection)
		budget.omitted += len(v) - len(keys)
		out := make(map[string]string, len(keys))
		for _, key := range keys {
			if len(key) > budget.bytes {
				budget.omitted++
				continue
			}
			budget.bytes -= len(key)
			item, ok := cloneReplayDebugEvidenceValue(v[key], budget, depth+1)
			if ok {
				out[strings.Clone(key)] = item.(string)
			}
		}
		return out, true
	case map[string]any:
		keys := boundedtext.SmallestStringKeys(v, maxReplayDebugEvidenceCollection)
		budget.omitted += len(v) - len(keys)
		out := make(map[string]any, len(keys))
		for _, key := range keys {
			if len(key) > budget.bytes {
				budget.omitted++
				continue
			}
			budget.bytes -= len(key)
			item, ok := cloneReplayDebugEvidenceValue(v[key], budget, depth+1)
			if ok {
				out[strings.Clone(key)] = item
			}
		}
		return out, true
	case []string:
		limit := min(len(v), maxReplayDebugEvidenceCollection)
		budget.omitted += len(v) - limit
		out := make([]any, 0, limit)
		for _, item := range v[:limit] {
			if value, ok := cloneReplayDebugEvidenceValue(item, budget, depth+1); ok {
				out = append(out, value)
			}
		}
		return out, true
	case []any:
		limit := min(len(v), maxReplayDebugEvidenceCollection)
		budget.omitted += len(v) - limit
		out := make([]any, 0, limit)
		for _, item := range v[:limit] {
			if value, ok := cloneReplayDebugEvidenceValue(item, budget, depth+1); ok {
				out = append(out, value)
			}
		}
		return out, true
	default:
		return cloneReplayDebugTypedValue(reflect.ValueOf(value), budget, depth)
	}
}

// Normalize adapter-owned typed values without invoking MarshalJSON or ever
// serializing an unbounded graph. Pointers/cycles share the same depth limit.
func cloneReplayDebugTypedValue(v reflect.Value, budget *replayDebugEvidenceBudget, depth int) (any, bool) {
	if !v.IsValid() {
		return nil, true
	}
	if depth > maxReplayDebugEvidenceDepth || budget.entries <= 0 || budget.bytes <= 0 {
		budget.omitted++
		return nil, false
	}
	if v.CanInterface() {
		if tm, ok := v.Interface().(time.Time); ok {
			return cloneReplayDebugEvidenceValue(tm.Format(time.RFC3339Nano), budget, depth+1)
		}
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return nil, true
		}
		return cloneReplayDebugTypedValue(v.Elem(), budget, depth+1)
	case reflect.Struct:
		out := make(map[string]any)
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			field, value := t.Field(i), v.Field(i)
			if field.PkgPath != "" {
				continue
			}
			parts := strings.Split(field.Tag.Get("json"), ",")
			if parts[0] == "-" {
				continue
			}
			name := parts[0]
			if name == "" {
				name = field.Name
			}
			omit := false
			for _, opt := range parts[1:] {
				if opt == "omitempty" {
					omit = true
				}
			}
			if omit && (value.IsZero() || ((value.Kind() == reflect.Slice || value.Kind() == reflect.Map || value.Kind() == reflect.String) && value.Len() == 0)) {
				continue
			}
			if len(out) >= maxReplayDebugEvidenceCollection || len(name) > budget.bytes {
				budget.omitted++
				continue
			}
			budget.bytes -= len(name)
			if value.CanInterface() {
				if cloned, ok := cloneReplayDebugEvidenceValue(value.Interface(), budget, depth+1); ok {
					out[strings.Clone(name)] = cloned
				}
			} else {
				budget.omitted++
			}
		}
		return out, true
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil, true
		}
		if v.Type().Elem().Kind() == reflect.Uint8 && v.Kind() == reflect.Slice {
			limit := min(v.Len(), budget.bytes/4*3)
			if limit < v.Len() {
				budget.omitted++
			}
			data := make([]byte, limit)
			for i := range data {
				data[i] = byte(v.Index(i).Uint())
			}
			encoded := base64.StdEncoding.EncodeToString(data)
			budget.bytes -= len(encoded)
			return encoded, true
		}
		limit := min(v.Len(), maxReplayDebugEvidenceCollection)
		budget.omitted += v.Len() - limit
		out := make([]any, 0, limit)
		for i := 0; i < limit; i++ {
			item := v.Index(i)
			if item.CanInterface() {
				if cloned, ok := cloneReplayDebugEvidenceValue(item.Interface(), budget, depth+1); ok {
					out = append(out, cloned)
				}
			} else {
				budget.omitted++
			}
		}
		return out, true
	case reflect.Map:
		if v.IsNil() {
			return nil, true
		}
		if v.Type().Key().Kind() != reflect.String {
			budget.omitted++
			return nil, false
		}
		keys := make(replayEvidenceKeyHeap, 0, min(v.Len(), maxReplayDebugEvidenceCollection))
		iter := v.MapRange()
		for iter.Next() {
			key := iter.Key().String()
			if len(keys) < maxReplayDebugEvidenceCollection {
				heap.Push(&keys, key)
			} else if key < keys[0] {
				keys[0] = key
				heap.Fix(&keys, 0)
			}
		}
		budget.omitted += v.Len() - len(keys)
		sort.Strings(keys)
		out := make(map[string]any, len(keys))
		for _, key := range keys {
			if len(key) > budget.bytes {
				budget.omitted++
				continue
			}
			budget.bytes -= len(key)
			item := v.MapIndex(reflect.ValueOf(key).Convert(v.Type().Key()))
			if item.CanInterface() {
				if cloned, ok := cloneReplayDebugEvidenceValue(item.Interface(), budget, depth+1); ok {
					out[strings.Clone(key)] = cloned
				}
			} else {
				budget.omitted++
			}
		}
		return out, true
	case reflect.String:
		return cloneReplayDebugEvidenceValue(v.String(), budget, depth+1)
	case reflect.Bool:
		return cloneReplayDebugEvidenceValue(v.Bool(), budget, depth+1)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cloneReplayDebugEvidenceValue(v.Int(), budget, depth+1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return cloneReplayDebugEvidenceValue(v.Uint(), budget, depth+1)
	case reflect.Float32, reflect.Float64:
		return cloneReplayDebugEvidenceValue(v.Float(), budget, depth+1)
	default:
		budget.omitted++
		return nil, false
	}
}

type replayEvidenceKeyHeap []string

func (h replayEvidenceKeyHeap) Len() int           { return len(h) }
func (h replayEvidenceKeyHeap) Less(i, j int) bool { return h[i] > h[j] }
func (h replayEvidenceKeyHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *replayEvidenceKeyHeap) Push(v any)        { *h = append(*h, v.(string)) }
func (h *replayEvidenceKeyHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}
