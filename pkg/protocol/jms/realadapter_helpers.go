package jms

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func firstMatchingString(values map[string]string, names ...string) string {
	for _, name := range names {
		for key, value := range values {
			if strings.EqualFold(key, name) && strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return ""
}

func anyToString(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func newJMSMessageID() string {
	return "ID:" + uuid.NewString()
}
