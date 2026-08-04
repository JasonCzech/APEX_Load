package websocket

import "testing"

func TestExtractRegexWithoutCaptureReturnsFullMatch(t *testing.T) {
	got, err := extractRegex(`order-\d+`, []byte("id=order-42"))
	if err != nil || got != "order-42" {
		t.Fatalf("extractRegex() = %q, %v", got, err)
	}
}
