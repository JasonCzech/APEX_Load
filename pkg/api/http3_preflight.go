package api

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// RunArchiveHTTP3Preflight is the scenario HTTP/3 preflight of a run: one
// QUIC handshake, without a request, to each https origin a group's HTTP/3
// policy reaches, from each load source that runs the group.
type RunArchiveHTTP3Preflight struct {
	// Policy is APEXLOAD_HTTP3_PREFLIGHT on the controller: report or block.
	Policy  string                 `json:"policy"`
	Results []HTTP3PreflightResult `json:"results,omitempty"`
	// Omitted counts results beyond the stored bound; failures are kept first.
	Omitted int `json:"omitted,omitempty"`
	// Unchecked lists load sources that could not run the check.
	Unchecked []HTTP3PreflightUnchecked `json:"unchecked,omitempty"`
	// Blocked is set when the block policy refused to start the run.
	Blocked bool `json:"blocked,omitempty"`
}

// HTTP3PreflightResult is one origin check from one load source. The fields
// repeat engine.HTTP3ProbeResult's rather than embed it, so the API contract
// lists them.
type HTTP3PreflightResult struct {
	// Source is the generator target, or "controller" for a local run.
	Source      string    `json:"source"`
	Region      string    `json:"region,omitempty"`
	Group       string    `json:"group"`
	Origin      string    `json:"origin"`
	Policy      string    `json:"policy"` // 3 or auto+h3
	OK          bool      `json:"ok"`
	ErrorClass  string    `json:"error_class,omitempty"`
	Error       string    `json:"error,omitempty"`
	Address     string    `json:"address,omitempty"`
	HandshakeMs float64   `json:"handshake_ms,omitempty"`
	Skipped     string    `json:"skipped,omitempty"` // why the origin was not checked
	CheckedAt   time.Time `json:"checked_at"`
}

func newHTTP3PreflightResult(result engine.HTTP3ProbeResult, source, region string) HTTP3PreflightResult {
	return HTTP3PreflightResult{Source: source, Region: region, Group: result.Group, Origin: result.Origin, Policy: result.Policy, OK: result.OK,
		ErrorClass: result.ErrorClass, Error: result.Error, Address: result.Address, HandshakeMs: result.HandshakeMs, Skipped: result.Skipped, CheckedAt: result.CheckedAt}
}

// Blocking reports whether the check can block a run under the block policy.
func (r HTTP3PreflightResult) Blocking() bool {
	return engine.HTTP3ProbeResult{Policy: r.Policy, OK: r.OK, Skipped: r.Skipped}.Blocking()
}

// HTTP3PreflightUnchecked is a load source whose check did not run.
type HTTP3PreflightUnchecked struct {
	Source string `json:"source"`
	Region string `json:"region,omitempty"`
	Reason string `json:"reason"`
}

const (
	// http3PreflightTimeout bounds the whole preflight; it runs while the
	// run's work is staged, so it must stay well inside the stage TTL.
	http3PreflightTimeout          = 20 * time.Second
	http3PreflightSourceConcurrent = 16
	maxStoredHTTP3PreflightResults = 512
	http3PreflightLocalSource      = "controller"
)

// http3PreflightPolicy is the controller's preflight policy.
var http3PreflightPolicy = func() string { return httpprotocol.HTTP3PreflightPolicy(os.Getenv) }

// http3ProbeClient is implemented by generator clients that can run the
// preflight (http3_preflight_v1).
type http3ProbeClient interface {
	ProbeHTTP3(ctx context.Context, testID string, generation uint64) ([]engine.HTTP3ProbeResult, error)
}

// http3PreflightGroups names the groups that have an HTTP/3 origin to check.
func http3PreflightGroups(specs []engine.GroupConfig) map[string]bool {
	groups := make(map[string]bool)
	for _, spec := range specs {
		if len(dsl.HTTP3ProbeOrigins(spec.Script)) > 0 {
			groups[spec.Name] = true
		}
	}
	return groups
}

// runLocalHTTP3Preflight checks a local run's HTTP/3 origins from the
// controller before its engine is built. Under the block policy it returns
// the error that fails the run.
func (tr *TestRunner) runLocalHTTP3Preflight(test *ManagedTest, groups []engine.GroupConfig) error {
	policy := http3PreflightPolicy()
	if policy == httpprotocol.HTTP3PreflightOff || len(http3PreflightGroups(groups)) == 0 || httpprotocol.HTTP3ExecutionError() != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), http3PreflightTimeout)
	defer cancel()
	var results []HTTP3PreflightResult
	for _, group := range groups {
		for _, result := range engine.ProbeHTTP3Group(ctx, group) {
			results = append(results, newHTTP3PreflightResult(result, http3PreflightLocalSource, ""))
		}
	}
	return tr.recordHTTP3Preflight(test, newHTTP3Preflight(policy, results, nil))
}

// runDistributedHTTP3Preflight asks every generator that runs an HTTP/3
// group to check its origins while the work is staged and before commit.
// Under the block policy it returns the error that fails the startup.
func (b *distributedExecutionBackend) runDistributedHTTP3Preflight(ctx context.Context, tr *TestRunner, test *ManagedTest, shards map[string][]GeneratorShardSummary, clients map[string]generatorClient, loads map[string]generatorLoadSnapshot, generation uint64) error {
	policy := http3PreflightPolicy()
	if policy == httpprotocol.HTTP3PreflightOff {
		return nil
	}
	tr.mu.RLock()
	groups := http3PreflightGroups(test.groupSpecs)
	tr.mu.RUnlock()
	if len(groups) == 0 {
		return nil
	}
	targets := make([]string, 0)
	seen := make(map[string]bool)
	for _, shard := range flattenGroupShards(shards) {
		if groups[shard.Group] && !seen[shard.Target] && clients[shard.Target] != nil {
			seen[shard.Target] = true
			targets = append(targets, shard.Target)
		}
	}
	sort.Strings(targets)
	ctx, cancel := context.WithTimeout(ctx, http3PreflightTimeout)
	defer cancel()
	var (
		mu        sync.Mutex
		results   []HTTP3PreflightResult
		unchecked []HTTP3PreflightUnchecked
		wg        sync.WaitGroup
	)
	slots := make(chan struct{}, http3PreflightSourceConcurrent)
	for _, target := range targets {
		region := b.regionForTarget(target)
		client, supported := clients[target].(http3ProbeClient)
		if !supported || !stringSliceContainsEqualFold(loads[target].CompatibleFeatures, generatorrpc.FeatureHTTP3PreflightV1) {
			mu.Lock()
			unchecked = append(unchecked, HTTP3PreflightUnchecked{Source: target, Region: region, Reason: "generator does not support the HTTP/3 preflight"})
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(target, region string, client http3ProbeClient) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			probed, err := client.ProbeHTTP3(ctx, test.ID, generation)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				unchecked = append(unchecked, HTTP3PreflightUnchecked{Source: target, Region: region, Reason: "check failed: " + err.Error()})
				return
			}
			for _, result := range probed {
				if groups[result.Group] {
					results = append(results, newHTTP3PreflightResult(result, target, region))
				}
			}
		}(target, region, client)
	}
	wg.Wait()
	return tr.recordHTTP3Preflight(test, newHTTP3Preflight(policy, results, unchecked))
}

func newHTTP3Preflight(policy string, results []HTTP3PreflightResult, unchecked []HTTP3PreflightUnchecked) *RunArchiveHTTP3Preflight {
	sort.SliceStable(results, func(i, j int) bool {
		if left, right := http3PreflightRank(results[i]), http3PreflightRank(results[j]); left != right {
			return left < right
		}
		if results[i].Source != results[j].Source {
			return results[i].Source < results[j].Source
		}
		if results[i].Group != results[j].Group {
			return results[i].Group < results[j].Group
		}
		return results[i].Origin < results[j].Origin
	})
	sort.Slice(unchecked, func(i, j int) bool { return unchecked[i].Source < unchecked[j].Source })
	preflight := &RunArchiveHTTP3Preflight{Policy: policy, Results: results, Unchecked: unchecked}
	if len(preflight.Results) > maxStoredHTTP3PreflightResults {
		preflight.Omitted = len(preflight.Results) - maxStoredHTTP3PreflightResults
		preflight.Results = preflight.Results[:maxStoredHTTP3PreflightResults]
	}
	preflight.Blocked = policy == httpprotocol.HTTP3PreflightBlock && http3PreflightBlockingCount(results) > 0
	return preflight
}

// http3PreflightRank orders blocking failures, then other failures, then
// skipped checks, then successes.
func http3PreflightRank(result HTTP3PreflightResult) int {
	switch {
	case result.Blocking():
		return 0
	case result.Skipped == "" && !result.OK:
		return 1
	case result.Skipped != "":
		return 2
	}
	return 3
}

func http3PreflightBlockingCount(results []HTTP3PreflightResult) int {
	count := 0
	for _, result := range results {
		if result.Blocking() {
			count++
		}
	}
	return count
}

// recordHTTP3Preflight keeps the preflight with the run and, when the block
// policy refused it, returns the startup error.
func (tr *TestRunner) recordHTTP3Preflight(test *ManagedTest, preflight *RunArchiveHTTP3Preflight) error {
	checked, failed, skipped := 0, 0, 0
	for _, result := range preflight.Results {
		switch {
		case result.Skipped != "":
			skipped++
		case result.OK:
			checked++
		default:
			checked++
			failed++
		}
	}
	record := slog.Info
	if failed > 0 {
		record = slog.Warn
	}
	record("HTTP/3 preflight", "test_id", test.ID, "policy", preflight.Policy, "checked", checked, "failed", failed, "skipped", skipped, "unchecked_sources", len(preflight.Unchecked), "blocked", preflight.Blocked)
	tr.mu.Lock()
	test.http3Preflight = preflight
	tr.mu.Unlock()
	if !preflight.Blocked {
		return nil
	}
	blocking := make([]string, 0, 3)
	for _, result := range preflight.Results {
		if !result.Blocking() || len(blocking) == cap(blocking) {
			continue
		}
		reason := result.ErrorClass
		if reason == "" {
			reason = result.Error
		}
		blocking = append(blocking, fmt.Sprintf("group %q origin %s from %s (%s)", result.Group, result.Origin, result.Source, reason))
	}
	more := ""
	if extra := http3PreflightBlockingCount(preflight.Results) - len(blocking); extra > 0 {
		more = fmt.Sprintf(" and %d more", extra)
	}
	return fmt.Errorf("HTTP/3 preflight failed for exact-3 %s%s; %s=block refuses to start the run, so no load-test traffic was sent", strings.Join(blocking, "; "), more, httpprotocol.EnvHTTP3Preflight)
}

func cloneHTTP3Preflight(preflight *RunArchiveHTTP3Preflight) *RunArchiveHTTP3Preflight {
	if preflight == nil {
		return nil
	}
	cloned := *preflight
	cloned.Results = append([]HTTP3PreflightResult(nil), preflight.Results...)
	cloned.Unchecked = append([]HTTP3PreflightUnchecked(nil), preflight.Unchecked...)
	return &cloned
}

// reportHTTP3PreflightRow is one check in the report's HTTP/3 preflight
// table.
type reportHTTP3PreflightRow struct {
	Source, Group, Origin, Policy, Result, Detail string
}

func reportHTTP3PreflightRows(preflight *RunArchiveHTTP3Preflight) []reportHTTP3PreflightRow {
	if preflight == nil {
		return nil
	}
	rows := make([]reportHTTP3PreflightRow, 0, len(preflight.Results)+len(preflight.Unchecked))
	for _, result := range preflight.Results {
		row := reportHTTP3PreflightRow{Source: http3PreflightSourceLabel(result.Source, result.Region), Group: result.Group, Origin: result.Origin, Policy: result.Policy}
		switch {
		case result.Skipped != "":
			row.Result, row.Detail = "Not checked", result.Skipped
		case result.OK:
			row.Result = "Handshake completed"
			row.Detail = fmt.Sprintf("%.1f ms", result.HandshakeMs)
			if result.Address != "" {
				row.Detail += " to " + result.Address
			}
		default:
			row.Result = "Failed"
			row.Detail = http3PreflightFailureDetail(result.ErrorClass, result.Error)
		}
		rows = append(rows, row)
	}
	for _, source := range preflight.Unchecked {
		rows = append(rows, reportHTTP3PreflightRow{Source: http3PreflightSourceLabel(source.Source, source.Region), Result: "Not checked", Detail: source.Reason})
	}
	return rows
}

// http3PreflightFailureDetail names the error class unless the error text
// already carries it.
func http3PreflightFailureDetail(class, message string) string {
	if class == "" || strings.Contains(message, class) {
		return message
	}
	if message == "" {
		return class
	}
	return class + ": " + message
}

func http3PreflightSourceLabel(source, region string) string {
	if region == "" {
		return source
	}
	return source + " (" + region + ")"
}

// reportHTTP3PreflightSummary is the sentence above the table.
func reportHTTP3PreflightSummary(preflight *RunArchiveHTTP3Preflight) string {
	if preflight == nil {
		return ""
	}
	summary := fmt.Sprintf("Policy %s: one QUIC handshake, without a request, to each https origin an HTTP/3 policy reaches, from each load source that runs the group, before the run started.", preflight.Policy)
	if preflight.Blocked {
		summary += " An exact-3 origin failed, so the run was not started."
	}
	if preflight.Omitted > 0 {
		summary += fmt.Sprintf(" %d more checks are not listed; failures are listed first.", preflight.Omitted)
	}
	return summary
}

func reportHTTP3PreflightLines(preflight *RunArchiveHTTP3Preflight) []string {
	if preflight == nil {
		return nil
	}
	lines := []string{"HTTP/3 preflight: " + reportHTTP3PreflightSummary(preflight)}
	for _, row := range reportHTTP3PreflightRows(preflight) {
		subject := row.Source
		if row.Origin != "" {
			subject = fmt.Sprintf("%s, group %s, %s (%s)", row.Source, row.Group, row.Origin, row.Policy)
		}
		line := fmt.Sprintf("- %s: %s", subject, row.Result)
		if row.Detail != "" {
			line += ", " + row.Detail
		}
		lines = append(lines, line)
	}
	return lines
}

func writeHTTP3PreflightCSVRows(write reportCSVWriteFunc, preflight *RunArchiveHTTP3Preflight) error {
	if preflight == nil {
		return nil
	}
	if err := write("network_phases", "http3_preflight", "policy", preflight.Policy); err != nil {
		return err
	}
	for index, row := range reportHTTP3PreflightRows(preflight) {
		key := fmt.Sprintf("http3_preflight:%d", index+1)
		for _, field := range [][2]string{{"source", row.Source}, {"group", row.Group}, {"origin", row.Origin}, {"policy", row.Policy}, {"result", row.Result}, {"detail", row.Detail}} {
			if field[1] == "" {
				continue
			}
			if err := write("network_phases", key, field[0], field[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// http3PreflightFinding reports failed handshakes of a run that started:
// major for exact-3 origins, whose requests cannot fall back, and minor for
// auto+h3 origins, which stay on TCP.
func http3PreflightFinding(preflight *RunArchiveHTTP3Preflight) *ReportFinding {
	if preflight == nil || preflight.Blocked {
		return nil
	}
	exact, upgrade := 0, 0
	var first *HTTP3PreflightResult
	for index := range preflight.Results {
		result := &preflight.Results[index]
		if result.Skipped != "" || result.OK {
			continue
		}
		if first == nil {
			first = result
		}
		if result.Blocking() {
			exact++
		} else {
			upgrade++
		}
	}
	if first == nil {
		return nil
	}
	finding := &ReportFinding{Title: "HTTP/3 preflight failures", Severity: "minor",
		Evidence: "QUIC handshakes from each load source before the run started; no request was sent."}
	detail := first.Error
	if first.ErrorClass != "" {
		detail = first.ErrorClass
	}
	observed := fmt.Sprintf("%s could not complete a QUIC handshake with %s (%s).", first.Source, first.Origin, detail)
	if exact > 0 {
		finding.Severity = "major"
		observed += fmt.Sprintf(" %d exact-3 check(s) failed: those requests cannot fall back to TCP.", exact)
	}
	if upgrade > 0 {
		observed += fmt.Sprintf(" %d auto+h3 check(s) failed: those origins stay on TCP when QUIC is unreachable.", upgrade)
	}
	finding.Observed = observed
	return finding
}
