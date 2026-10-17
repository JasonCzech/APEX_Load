package browserresource

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

func CursorShape(raw string) bool {
	if len(raw) == 0 || len(raw) > MaxCursorBytes || strings.Count(raw, ".") != 1 {
		return false
	}
	parts := strings.SplitN(raw, ".", 2)
	if len(parts[0]) == 0 || len(parts[1]) != 43 {
		return false
	}
	for _, part := range parts {
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				return false
			}
		}
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	return err == nil && len(signature) == sha256.Size
}
