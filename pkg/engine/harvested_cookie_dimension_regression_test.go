package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	stdhttp "net/http"
	"net/http/httptest"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type dimensionCapturingSink struct {
	mu           sync.Mutex
	results      []protocol.Result
	transactions []protocol.Result
}

func (s *dimensionCapturingSink) RecordResult(result *protocol.Result) error {
	if result == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, *result)
	return nil
}

func (s *dimensionCapturingSink) RecordTransaction(result *protocol.Result) error {
	if result == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transactions = append(s.transactions, *result)
	return nil
}

func (s *dimensionCapturingSink) snapshot() ([]protocol.Result, []protocol.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]protocol.Result(nil), s.results...),
		append([]protocol.Result(nil), s.transactions...)
}

// The headline regression this whole change exists to prevent.
//
// The HTTP adapter used to snapshot every value in its cookie jar into the
// engine's runtime secret matcher. An ordinary `Set-Cookie: locale=en` therefore
// made the two-letter string "en" an exact-match secret, and
// scrubSensitiveResultMetadata (which runs BEFORE Metrics.Record) substring-
// replaced it in every stable dimension key:
//
//   - the action "open_home" became "op[REDACTED]_home"
//   - the group "vendors" became "v[REDACTED]dors", so the group's totals were
//     filed under a scrubbed name while MultiGroupRunResult reads the CONFIGURED
//     name — the group reported ZERO TRANSACTIONS
//   - the neutral extraction "en" became "[REDACTED]"
//
// Every dimension below deliberately contains the substring "en".
func TestShortSUTCookieDoesNotScrubMetricDimensionsOrZeroGroupTotals(t *testing.T) {
	const (
		groupName       = "vendors"
		actionName      = "open_home"
		transactionName = "vendor_journey"
		cookieValue     = "en"
		iterations      = 3
	)
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		stdhttp.SetCookie(w, &stdhttp.Cookie{Name: "locale", Value: cookieValue, Path: "/"})
		stdhttp.SetCookie(w, &stdhttp.Cookie{Name: "cookieconsent", Value: "true", Path: "/"})
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(stdhttp.StatusOK)
		_, _ = w.Write([]byte("locale=" + cookieValue + "\n"))
	}))
	defer server.Close()

	sink := &dimensionCapturingSink{}
	script := &dsl.Script{
		Name:     transactionName,
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL:  server.URL,
			RunLogic: dsl.RunLogicConfig{Iterations: iterations},
		},
		Actions: []dsl.Action{{
			Name:   actionName,
			Type:   "GET",
			Method: "GET",
			Target: "/home",
			Extractors: []dsl.Extractor{{
				Name:       "locale_echo",
				Source:     "body",
				Type:       "regex",
				Expression: `locale=(\w+)`,
			}},
		}},
	}

	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:       groupName,
			Script:     script,
			VUsers:     1,
			ResultSink: sink,
		}},
		Duration: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	runResult, err := mge.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(runResult.Groups) != 1 {
		t.Fatalf("group results = %#v", runResult.Groups)
	}
	group := runResult.Groups[0]
	if group.Name != groupName {
		t.Fatalf("group name = %q, want %q", group.Name, groupName)
	}
	// The zero-transaction symptom: totals filed under a scrubbed group name are
	// unreachable from the configured name.
	if group.TotalTransactions != iterations {
		t.Fatalf("group %q transactions = %d, want %d; a scrubbed group dimension files "+
			"totals under a name MultiGroupRunResult cannot read",
			group.Name, group.TotalTransactions, iterations)
	}
	if runResult.TotalTransactions != iterations {
		t.Fatalf("run transactions = %d, want %d", runResult.TotalTransactions, iterations)
	}

	results, transactions := sink.snapshot()
	if len(results) == 0 {
		t.Fatal("no action results reached the sink")
	}
	if len(transactions) == 0 {
		t.Fatal("no transaction results reached the sink")
	}
	for index := range results {
		result := results[index]
		if result.ActionName != actionName {
			t.Fatalf("result[%d] action dimension = %q, want %q", index, result.ActionName, actionName)
		}
		if result.Group != groupName {
			t.Fatalf("result[%d] group dimension = %q, want %q", index, result.Group, groupName)
		}
		if strings.Contains(result.DependencyTarget, redactedPayloadValue) {
			t.Fatalf("result[%d] dependency dimension = %q", index, result.DependencyTarget)
		}
		// The extraction is neutrally named and carries the cookie value itself.
		if got := result.Extractions["locale_echo"]; got != cookieValue {
			t.Fatalf("result[%d] extraction = %q, want the value the system under test returned (%q)",
				index, got, cookieValue)
		}
	}
	for index := range transactions {
		transaction := transactions[index]
		if transaction.ActionName != transactionName {
			t.Fatalf("transaction[%d] dimension = %q, want %q",
				index, transaction.ActionName, transactionName)
		}
		if transaction.Group != groupName {
			t.Fatalf("transaction[%d] group dimension = %q, want %q",
				index, transaction.Group, groupName)
		}
	}

	// The live metric series are keyed by the same dimensions.
	if snapshot, ok := mge.metrics.LatestSnapshot(); ok {
		for key := range snapshot.ByAction {
			if strings.Contains(key, redactedPayloadValue) {
				t.Fatalf("action metric series key = %q", key)
			}
		}
		for key := range snapshot.ByTransaction {
			if strings.Contains(key, redactedPayloadValue) {
				t.Fatalf("transaction metric series key = %q", key)
			}
		}
		for key := range snapshot.ByGroup {
			if strings.Contains(key, redactedPayloadValue) {
				t.Fatalf("group metric series key = %q", key)
			}
		}
	}
}
