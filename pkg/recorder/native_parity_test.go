package recorder

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/paritytest"
)

// TestNativeRecordingParityCorpus converts every recorded proxy session under
// testdata/native_parity with the production converter, replays it once
// against a recording fixture server, and compares the wire traffic with the
// case's expected.json. It holds native recordings to the same bar as
// imported LoadRunner scripts (pkg/convert/enterprisearchive's corpus).
func TestNativeRecordingParityCorpus(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "native_parity", "*"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dirs)
	if len(dirs) == 0 {
		t.Fatal("no native parity corpus cases found")
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		dir := dir
		t.Run(filepath.Base(dir), func(t *testing.T) {
			exp, err := paritytest.LoadExpectation(dir)
			if err != nil {
				t.Fatal(err)
			}
			server := paritytest.NewServer(exp.Responses)
			defer server.Close()
			session := loadParitySession(t, dir, server)
			result := ConvertSessionWithOptions(session, session.Name, ConvertOptions{})
			if result.Script == nil {
				t.Fatalf("conversion produced no script; warnings: %v", result.Warnings)
			}
			for _, problem := range paritytest.CheckOutcome(context.Background(), server, exp, result.Script, result.Warnings) {
				t.Error(problem)
			}
		})
	}
}

// loadParitySession reads a case's session.json, points it at the fixture
// server, and fills the raw (unredacted) header views the live recorder keeps
// out of JSON, so correlation sees what it would see after a real capture.
func loadParitySession(t *testing.T, dir string, server *paritytest.Server) *Session {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), paritytest.Origin, server.URL))
	host := strings.TrimPrefix(server.URL, "http://")
	raw = []byte(strings.ReplaceAll(string(raw), "parity.test", host))
	var session Session
	if err := json.Unmarshal(raw, &session); err != nil {
		t.Fatalf("session.json: %v", err)
	}
	for i := range session.Requests {
		req := &session.Requests[i]
		if req.HeadersMulti == nil {
			req.HeadersMulti = multiFromSingle(req.Headers)
		}
		if req.ResponseHeadersMulti == nil {
			req.ResponseHeadersMulti = multiFromSingle(req.ResponseHeaders)
		}
		req.RawHeaders, req.RawHeadersMulti = req.Headers, req.HeadersMulti
		req.RawResponseHeaders, req.RawResponseHeadersMulti = req.ResponseHeaders, req.ResponseHeadersMulti
	}
	return &session
}

func multiFromSingle(headers map[string]string) map[string][]string {
	if headers == nil {
		return nil
	}
	multi := make(map[string][]string, len(headers))
	for name, value := range headers {
		multi[name] = []string{value}
	}
	return multi
}
