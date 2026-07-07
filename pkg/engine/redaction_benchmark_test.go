package engine

import (
	"strings"
	"testing"
)

func BenchmarkScrubSensitiveTextClean1KB(b *testing.B) {
	payload := strings.Repeat(`{"status":"ok","id":"cart-123","message":"ordinary payload"}`, 18)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := scrubSensitiveText(payload); got != payload {
			b.Fatal("clean payload was unexpectedly redacted")
		}
	}
}

func BenchmarkScrubSensitiveTextSensitive50KB(b *testing.B) {
	chunk := `{"access_token":"abc123","password":"hunter2","ssn":"123-45-6789","jwt":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature"}`
	payload := strings.Repeat(chunk, (50*1024/len(chunk))+1)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := scrubSensitiveText(payload); strings.Contains(got, "hunter2") {
			b.Fatal("sensitive payload was not redacted")
		}
	}
}
