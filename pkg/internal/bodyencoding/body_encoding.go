// Package bodyencoding provides the bounded base64 body representation shared
// by DSL admission and structural payload inspection.
package bodyencoding

import (
	"encoding/base64"
	"errors"
	"strings"
)

var ErrLimit = errors.New("decoded body exceeds inspection limit")

// Decode preserves text bodies as untransformed. A positive maxBytes admits
// decoded allocation before decoding; zero retains legacy unbounded behavior.
func Decode(body, encoding string, maxBytes int) ([]byte, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		return nil, false, nil
	}
	encoded := strings.TrimSpace(body)
	if maxBytes > 0 {
		maxEncodedBytes := base64.StdEncoding.EncodedLen(maxBytes)
		// Bound wrapped-input inspection before counting meaningful bytes.
		if len(encoded) > maxEncodedBytes*2 {
			return nil, true, ErrLimit
		}
		encodedBytes := 0
		for index := 0; index < len(encoded); index++ {
			if encoded[index] != '\r' && encoded[index] != '\n' {
				encodedBytes++
			}
		}
		if encodedBytes > maxEncodedBytes {
			return nil, true, ErrLimit
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, true, err
	}
	if maxBytes > 0 && len(decoded) > maxBytes {
		return nil, true, ErrLimit
	}
	return decoded, true, nil
}
