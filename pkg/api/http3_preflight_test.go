package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"github.com/jasonczech/apexload/pkg/storage"
)

func http3PreflightFixture(source, group, origin, policy string, ok bool, skipped string) HTTP3PreflightResult {
	result := HTTP3PreflightResult{Source: source, Group: group, Origin: origin, Policy: policy, OK: ok, Skipped: skipped}
	if ok {
		result.HandshakeMs, result.Address = 4.2, "10.0.0.5:443"
	} else if skipped == "" {
		result.ErrorClass, result.Error = "quic_handshake_timeout", "timeout: no recent network activity"
	}
	return result
}

func TestHTTP3PreflightBlocksOnlyFailedExact3OriginsAndListsFailuresFirst(t *testing.T) {
	results := []HTTP3PreflightResult{
		http3PreflightFixture("gen-a", "buyers", "https://shop.example", "3", true, ""),
		http3PreflightFixture("gen-a", "browsers", "https://cdn.example", "auto+h3", false, ""),
		http3PreflightFixture("gen-b", "buyers", "https://api.example", "3", false, ""),
		http3PreflightFixture("gen-b", "buyers", "https://{{param.host}}", "3", false, "dynamic target: the origin is known only during the run"),
	}
	blocked := newHTTP3Preflight(httpprotocol.HTTP3PreflightBlock, append([]HTTP3PreflightResult(nil), results...), nil)
	if !blocked.Blocked {
		t.Fatal("a failed exact-3 origin must block under the block policy")
	}
	order := []string{"https://api.example", "https://cdn.example", "https://{{param.host}}", "https://shop.example"}
	for index, origin := range order {
		if blocked.Results[index].Origin != origin {
			t.Fatalf("result %d = %s, want %s (blocking, failed, skipped, completed)", index, blocked.Results[index].Origin, origin)
		}
	}
	runner := &TestRunner{}
	err := runner.recordHTTP3Preflight(&ManagedTest{ID: "run"}, blocked)
	if err == nil || !strings.Contains(err.Error(), `group "buyers" origin https://api.example from gen-b (quic_handshake_timeout)`) || !strings.Contains(err.Error(), httpprotocol.EnvHTTP3Preflight+"=block") || strings.Contains(err.Error(), "cdn.example") {
		t.Fatalf("block error = %v", err)
	}

	reported := newHTTP3Preflight(httpprotocol.HTTP3PreflightReport, append([]HTTP3PreflightResult(nil), results...), nil)
	if reported.Blocked {
		t.Fatal("the report policy never blocks")
	}
	test := &ManagedTest{ID: "run"}
	if err := runner.recordHTTP3Preflight(test, reported); err != nil || test.http3Preflight != reported {
		t.Fatalf("report policy: err=%v recorded=%v", err, test.http3Preflight)
	}
	// auto+h3 and skipped origins never block.
	if newHTTP3Preflight(httpprotocol.HTTP3PreflightBlock, []HTTP3PreflightResult{results[1], results[3]}, nil).Blocked {
		t.Fatal("auto+h3 failures and unchecked origins must not block")
	}

	many := make([]HTTP3PreflightResult, 0, maxStoredHTTP3PreflightResults+10)
	for index := 0; index < maxStoredHTTP3PreflightResults+9; index++ {
		many = append(many, http3PreflightFixture(fmt.Sprintf("gen-%03d", index), "buyers", "https://shop.example", "3", true, ""))
	}
	many = append(many, http3PreflightFixture("gen-zzz", "buyers", "https://shop.example", "3", false, ""))
	bounded := newHTTP3Preflight(httpprotocol.HTTP3PreflightReport, many, nil)
	if len(bounded.Results) != maxStoredHTTP3PreflightResults || bounded.Omitted != 10 || bounded.Results[0].Source != "gen-zzz" {
		t.Fatalf("bounded = %d results, %d omitted, first %s", len(bounded.Results), bounded.Omitted, bounded.Results[0].Source)
	}
}

func TestHTTP3PreflightRendersInEveryReportFormat(t *testing.T) {
	preflight := newHTTP3Preflight(httpprotocol.HTTP3PreflightReport, []HTTP3PreflightResult{
		http3PreflightFixture("gen-a", "buyers", "https://shop.example", "3", true, ""),
		http3PreflightFixture("gen-b", "buyers", "https://api.example", "3", false, ""),
	}, []HTTP3PreflightUnchecked{{Source: "gen-c", Region: "east", Reason: "check failed: deadline exceeded"}})
	rows := reportHTTP3PreflightRows(preflight)
	if len(rows) != 3 || rows[0].Result != "Failed" || rows[0].Detail != "quic_handshake_timeout: timeout: no recent network activity" || http3PreflightFailureDetail("timeout", "not_sent: timeout: no recent network activity") != "not_sent: timeout: no recent network activity" ||
		rows[1].Result != "Handshake completed" || rows[1].Detail != "4.2 ms to 10.0.0.5:443" ||
		rows[2].Source != "gen-c (east)" || rows[2].Result != "Not checked" {
		t.Fatalf("rows = %+v", rows)
	}
	lines := strings.Join(reportHTTP3PreflightLines(preflight), "\n")
	if !strings.Contains(lines, "Policy report") || !strings.Contains(lines, "- gen-b, group buyers, https://api.example (3): Failed, quic_handshake_timeout") || !strings.Contains(lines, "- gen-c (east): Not checked, check failed") {
		t.Fatalf("PDF lines = %s", lines)
	}
	csv := map[string]string{}
	if err := writeHTTP3PreflightCSVRows(func(section, key, field, value string) error {
		csv[key+"."+field] = value
		return nil
	}, preflight); err != nil {
		t.Fatal(err)
	}
	if csv["http3_preflight.policy"] != "report" || csv["http3_preflight:1.result"] != "Failed" || csv["http3_preflight:3.source"] != "gen-c (east)" {
		t.Fatalf("CSV = %v", csv)
	}

	finding := http3PreflightFinding(preflight)
	if finding == nil || finding.Severity != "major" || !strings.Contains(finding.Observed, "1 exact-3 check(s) failed") {
		t.Fatalf("exact-3 finding = %+v", finding)
	}
	upgradeOnly := newHTTP3Preflight(httpprotocol.HTTP3PreflightReport, []HTTP3PreflightResult{http3PreflightFixture("gen-a", "web", "https://cdn.example", "auto+h3", false, "")}, nil)
	if finding := http3PreflightFinding(upgradeOnly); finding == nil || finding.Severity != "minor" || !strings.Contains(finding.Observed, "stay on TCP") {
		t.Fatalf("auto+h3 finding = %+v", finding)
	}
	blocked := newHTTP3Preflight(httpprotocol.HTTP3PreflightBlock, preflight.Results, nil)
	if http3PreflightFinding(blocked) != nil {
		t.Fatal("a run the preflight refused carries the reason in its error, not a finding")
	}
	if http3PreflightFinding(newHTTP3Preflight(httpprotocol.HTTP3PreflightReport, []HTTP3PreflightResult{preflight.Results[1]}, nil)) != nil {
		t.Fatal("completed handshakes are not a finding")
	}
}

func newHTTP3PreflightRunner(t *testing.T) *TestRunner {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewTestRunner(1, NewHub(), store)
}

func http3PreflightScript(id, base string) *dsl.Script {
	return &dsl.Script{ID: id, Name: id, Protocol: "http", Version: "1.0.0", Config: dsl.ScriptConfig{
		BaseURL: base,
		HTTP:    dsl.HTTPConfig{Version: "3", TLS: dsl.TLSConfig{InsecureSkipVerify: true}, QUIC: &dsl.HTTPQUICConfig{HandshakeTimeoutMs: 1000}},
	}, Actions: []dsl.Action{{Name: "home", Type: "GET", Target: "/"}}}
}

func TestLocalRunHTTP3PreflightBlocksAFailedExact3OriginBeforeAnyTraffic(t *testing.T) {
	t.Setenv(httpprotocol.EnvHTTP3, "true")
	t.Setenv(httpprotocol.EnvHTTP3Preflight, httpprotocol.HTTP3PreflightBlock)
	dead, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadPort := dead.LocalAddr().(*net.UDPAddr).Port
	_ = dead.Close()
	runner := newHTTP3PreflightRunner(t)
	runner.StoreScript(http3PreflightScript("h3-dead", fmt.Sprintf("https://127.0.0.1:%d", deadPort)))
	test, err := runner.CreateTest(CreateTestRequest{Name: "HTTP3 blocked", Duration: "2s", ScriptID: "h3-dead", VUsers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatal(err)
	}
	failed := waitForTestStatus(t, runner, test.ID, "FAILED")
	if !strings.Contains(failed.Error, "HTTP/3 preflight failed for exact-3") || !strings.Contains(failed.Error, "no load-test traffic was sent") {
		t.Fatalf("error = %q", failed.Error)
	}
	if failed.http3Preflight == nil || !failed.http3Preflight.Blocked || len(failed.http3Preflight.Results) != 1 || failed.http3Preflight.Results[0].Source != http3PreflightLocalSource {
		t.Fatalf("preflight = %+v", failed.http3Preflight)
	}
}

func TestLocalRunHTTP3PreflightIsRecordedInTheArchiveReport(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	t.Setenv(httpprotocol.EnvHTTP3, "true")
	t.Setenv(httpprotocol.EnvHTTP3Preflight, "")
	runner := newHTTP3PreflightRunner(t)
	runner.StoreScript(http3PreflightScript("h3-live", fmt.Sprintf("https://127.0.0.1:%d", fixture.Port)))
	test, err := runner.CreateTest(CreateTestRequest{Name: "HTTP3 reported", Duration: "1s", ScriptID: "h3-live", VUsers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatal(err)
	}
	completed := waitForTestStatusWithin(t, runner, test.ID, 30*time.Second, "COMPLETE")
	if completed.Archive == nil {
		t.Fatal("archive missing")
	}
	report, err := buildRunArchiveReport(completed.Archive)
	if err != nil {
		t.Fatal(err)
	}
	preflight := report.HTTP3Preflight
	if preflight == nil || preflight.Policy != httpprotocol.HTTP3PreflightReport || preflight.Blocked || len(preflight.Results) != 1 {
		t.Fatalf("report preflight = %+v", preflight)
	}
	if result := preflight.Results[0]; !result.OK || result.Group == "" || result.Source != http3PreflightLocalSource || result.HandshakeMs <= 0 {
		t.Fatalf("result = %+v", result)
	}
	for _, finding := range deriveReportFindings(report) {
		if finding.Title == "HTTP/3 preflight failures" {
			t.Fatalf("a completed handshake produced a finding: %+v", finding)
		}
	}
}

func TestLocalRunSkipsTheHTTP3PreflightWhenTheGroupHasNoHTTP3Origin(t *testing.T) {
	t.Setenv(httpprotocol.EnvHTTP3, "true")
	test := &ManagedTest{ID: "tcp"}
	runner := &TestRunner{}
	group := engine.GroupConfig{Name: "tcp", Script: &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://shop.example"}, Actions: []dsl.Action{{Name: "a", Type: "GET", Target: "/"}}}}
	if err := runner.runLocalHTTP3Preflight(test, []engine.GroupConfig{group}); err != nil || test.http3Preflight != nil {
		t.Fatalf("TCP-only run: err=%v preflight=%+v", err, test.http3Preflight)
	}
	t.Setenv(httpprotocol.EnvHTTP3Preflight, httpprotocol.HTTP3PreflightOff)
	group.Script = http3PreflightScript("h3", "https://127.0.0.1:1")
	if err := runner.runLocalHTTP3Preflight(test, []engine.GroupConfig{group}); err != nil || test.http3Preflight != nil {
		t.Fatalf("policy off: err=%v preflight=%+v", err, test.http3Preflight)
	}
}

type http3ProbeFakeClient struct {
	startupLifecycleFakeClient
	results []engine.HTTP3ProbeResult
	err     error
	calls   atomic.Int32
	gotTest string
	gotGen  uint64
}

func (c *http3ProbeFakeClient) ProbeHTTP3(_ context.Context, testID string, generation uint64) ([]engine.HTTP3ProbeResult, error) {
	c.calls.Add(1)
	c.gotTest, c.gotGen = testID, generation
	return c.results, c.err
}

func TestDistributedHTTP3PreflightAsksOnlyGeneratorsThatRunHTTP3Groups(t *testing.T) {
	t.Setenv(httpprotocol.EnvHTTP3Preflight, httpprotocol.HTTP3PreflightBlock)
	backend := newDistributedExecutionBackend([]string{"gen-a|region=east", "gen-b|region=west", "gen-c", "gen-d"}, time.Second, nil)
	h3 := http3PreflightScript("h3", "https://shop.example")
	tcp := &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://legacy.example"}, Actions: []dsl.Action{{Name: "a", Type: "GET", Target: "/"}}}
	runner := &TestRunner{}
	test := &ManagedTest{ID: "run", groupSpecs: []engine.GroupConfig{{Name: "buyers", Script: h3}, {Name: "legacy", Script: tcp}}}
	shards := map[string][]GeneratorShardSummary{
		"buyers": {{Group: "buyers", Target: "gen-a"}, {Group: "buyers", Target: "gen-b"}, {Group: "buyers", Target: "gen-d"}},
		"legacy": {{Group: "legacy", Target: "gen-c"}},
	}
	ok := &http3ProbeFakeClient{results: []engine.HTTP3ProbeResult{{Group: "buyers", Origin: "https://shop.example", Policy: "3", OK: true, HandshakeMs: 3}}}
	failing := &http3ProbeFakeClient{results: []engine.HTTP3ProbeResult{{Group: "buyers", Origin: "https://shop.example", Policy: "3", ErrorClass: "quic_handshake_timeout", Error: "timeout"}}}
	tcpOnly := &http3ProbeFakeClient{}
	legacy := &startupLifecycleFakeClient{}
	feature := []string{generatorrpc.FeatureHTTP3PreflightV1}
	clients := map[string]generatorClient{"gen-a": ok, "gen-b": failing, "gen-c": tcpOnly, "gen-d": legacy}
	loads := map[string]generatorLoadSnapshot{"gen-a": {CompatibleFeatures: feature}, "gen-b": {CompatibleFeatures: feature}, "gen-c": {CompatibleFeatures: feature}, "gen-d": {}}

	err := backend.runDistributedHTTP3Preflight(context.Background(), runner, test, shards, clients, loads, 7)
	if err == nil || !strings.Contains(err.Error(), "from gen-b (quic_handshake_timeout)") {
		t.Fatalf("block error = %v", err)
	}
	if ok.calls.Load() != 1 || ok.gotTest != "run" || ok.gotGen != 7 || failing.calls.Load() != 1 {
		t.Fatalf("probe calls: gen-a %d (%s, %d), gen-b %d", ok.calls.Load(), ok.gotTest, ok.gotGen, failing.calls.Load())
	}
	if tcpOnly.calls.Load() != 0 {
		t.Fatal("a generator that runs only TCP groups was probed")
	}
	preflight := test.http3Preflight
	if preflight == nil || !preflight.Blocked || len(preflight.Results) != 2 || preflight.Results[0].Source != "gen-b" || preflight.Results[0].Region != "west" || preflight.Results[1].Region != "east" {
		t.Fatalf("preflight = %+v", preflight)
	}
	if len(preflight.Unchecked) != 1 || preflight.Unchecked[0].Source != "gen-d" || !strings.Contains(preflight.Unchecked[0].Reason, "does not support") {
		t.Fatalf("unchecked = %+v", preflight.Unchecked)
	}

	// The report policy records the same results and lets the run start; a
	// generator whose check errors is listed, never blocking.
	t.Setenv(httpprotocol.EnvHTTP3Preflight, "")
	failing.err = errors.New("deadline exceeded")
	if err := backend.runDistributedHTTP3Preflight(context.Background(), runner, test, shards, clients, loads, 7); err != nil {
		t.Fatalf("report policy: %v", err)
	}
	if test.http3Preflight.Blocked || len(test.http3Preflight.Unchecked) != 2 || !strings.Contains(test.http3Preflight.Unchecked[0].Reason+test.http3Preflight.Unchecked[1].Reason, "check failed: deadline exceeded") {
		t.Fatalf("report preflight = %+v", test.http3Preflight)
	}

	t.Setenv(httpprotocol.EnvHTTP3Preflight, httpprotocol.HTTP3PreflightOff)
	ok.calls.Store(0)
	if err := backend.runDistributedHTTP3Preflight(context.Background(), runner, test, shards, clients, loads, 7); err != nil || ok.calls.Load() != 0 {
		t.Fatalf("policy off: err=%v calls=%d", err, ok.calls.Load())
	}
}
