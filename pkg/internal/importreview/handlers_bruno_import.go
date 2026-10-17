package importreview

import (
	"strconv"
)

func SetJSONPointerStringValue(root any, pointer string, value string) bool {
	tokens, err := SplitJSONPointer(pointer)
	if err != nil || len(tokens) == 0 {
		return false
	}
	current := root
	for _, token := range tokens[:len(tokens)-1] {
		switch typed := current.(type) {
		case map[string]any:
			child, exists := typed[token]
			if !exists {
				return false
			}
			current = child
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return false
			}
			current = typed[index]
		default:
			return false
		}
	}
	last := tokens[len(tokens)-1]
	switch typed := current.(type) {
	case map[string]any:
		if _, ok := typed[last].(string); !ok {
			return false
		}
		typed[last] = value
		return true
	case []any:
		index, err := strconv.Atoi(last)
		if err != nil || index < 0 || index >= len(typed) {
			return false
		}
		if _, ok := typed[index].(string); !ok {
			return false
		}
		typed[index] = value
		return true
	default:
		return false
	}
}
