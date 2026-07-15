package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteErrorScrubsCredentialBearingClientErrors(t *testing.T) {
	secret := "short-secret"
	recorder := httptest.NewRecorder()
	writeError(recorder, http.StatusBadRequest, "connect https://user:"+secret+"@example.test/path failed")

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if strings.Contains(payload["error"], secret) {
		t.Fatalf("client error leaked URL credential: %q", payload["error"])
	}
}
