package perfevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// JavaFixtureTarget is run in a separate process, outside the execution cgroup.
// It performs deterministic functional assertions, not production simulation.
func JavaFixtureTarget() http.Handler {
	body := []byte(`{"id":17,"payload":"` + strings.Repeat("x", 4096) + `"}`)
	hash := sha256.Sum256(body)
	expected := hex.EncodeToString(hash[:])
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/order" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		case http.MethodPost:
			data, err := io.ReadAll(io.LimitReader(r.Body, 65537))
			if err != nil || len(data) > 65536 || string(data) != string(body) || r.Header.Get("X-Content-SHA256") != expected {
				http.Error(w, "mismatch", http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(w, "ok")
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}
