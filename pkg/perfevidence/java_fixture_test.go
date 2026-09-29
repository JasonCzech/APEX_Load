package perfevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/dsl"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJavaFixtureScript(t *testing.T) {
	for _, w := range []string{"light", "middleware-heavy"} {
		s, err := JavaFixtureScript(w, "http://127.0.0.1:18099", []string{"/operator/fixture.jar"})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(map[string]any{"script": s})
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := dsl.Parse(data)
		if err != nil || len(parsed.Init) != 1 || len(parsed.Teardown) != 1 {
			t.Fatalf("fixture %s: %v", w, err)
		}
	}
	if _, err := JavaFixtureScript("light", "http://user:password@127.0.0.1:80", []string{"/fixture.jar"}); err == nil {
		t.Fatal("credential-bearing fixture accepted")
	}
}

func TestJavaFixtureTarget(t *testing.T) {
	s := httptest.NewServer(JavaFixtureTarget())
	defer s.Close()
	r, err := http.Get(s.URL + "/order")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("GET: %v", err)
	}
	hash := sha256.Sum256(body)
	for _, good := range []bool{true, false} {
		req, _ := http.NewRequest("POST", s.URL+"/order", bytes.NewReader(body))
		if good {
			req.Header.Set("X-Content-SHA256", hex.EncodeToString(hash[:]))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if (resp.StatusCode == 200) != good {
			t.Fatalf("POST status %d", resp.StatusCode)
		}
	}
}
