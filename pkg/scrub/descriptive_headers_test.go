package scrub

import (
	"strings"
	"testing"
)

func TestValueDescriptiveHeader(t *testing.T) {
	const token = "aZ9qR7xW5mN3vB1cD8fG6hJ4kL2pS0tU"
	const jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	for _, tt := range []struct{ name, header, value, want string }{
		{"product", "User-Agent", "SalesforceTouchContainer/2.0", "SalesforceTouchContainer/2.0"},
		{"device ID", "User-Agent", "uid_12345678-1234-1234-1234-123456789abc", "uid_12345678-1234-1234-1234-123456789abc"},
		{"bare token", "User-Agent", token, Redacted},
		{"unexpected version", "User-Agent", "Client/" + token, Redacted},
		{"JWT", "User-Agent", "Client/1.0 " + jwt, "Client/1.0 " + Redacted},
		{"structured", "User-Agent", "Client/1.0 password=" + token, "Client/1.0 password=" + Redacted},
		{"RUM", "x-dtpc", "6$123456789_1234567890123v987654321e42", "6$123456789_1234567890123v987654321e42"},
		{"RUM variant", "x-dtpc", "6$123456789_123456789hABCDEFGHIJKLMNOPQRSTUVWXYv12-345e6", "6$123456789_123456789hABCDEFGHIJKLMNOPQRSTUVWXYv12-345e6"},
		{"RUM token", "x-dtpc", token, Redacted},
		{"RUM JWT", "x-dtpc", jwt, Redacted},
		{"other header", "unknown", token, Redacted},
		{"empty", "User-Agent", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValueDescriptiveHeader(tt.header, tt.value, Policy{}); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
	// Explicit policy rules must win even over valid descriptive syntax.
	got := ValueDescriptiveHeader("User-Agent", "SalesforceTouchContainer/2.0", Policy{ExtraPatterns: []Rule{{Pattern: "SalesforceTouchContainer", Replacement: Redacted}}})
	if strings.Contains(got, "SalesforceTouchContainer") {
		t.Fatal("custom policy was bypassed")
	}
}
