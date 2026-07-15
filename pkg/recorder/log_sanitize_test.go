package recorder

import (
	"errors"
	"strings"
	"testing"
)

func TestRecorderLogSanitizersRemoveURLCredentials(t *testing.T) {
	raw := "wss://alice:password@example.test/socket?access_token=secret-token-1234567890&view=compact"
	sanitizedURL := recorderLogURL(raw)
	if strings.Contains(sanitizedURL, "alice") || strings.Contains(sanitizedURL, "password") || strings.Contains(sanitizedURL, "secret-token") {
		t.Fatalf("sanitized URL leaked credentials: %q", sanitizedURL)
	}
	if !strings.Contains(sanitizedURL, "view=compact") {
		t.Fatalf("sanitized URL lost non-sensitive context: %q", sanitizedURL)
	}

	sanitizedError := recorderLogError(errors.New("Get \"https://example.test/path?api_key=secret-token-1234567890\": connection refused"))
	if strings.Contains(sanitizedError, "secret-token") {
		t.Fatalf("sanitized error leaked credentials: %q", sanitizedError)
	}
}
