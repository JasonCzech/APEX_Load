package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestReplayHARBodyRedactionAndSnapshotBounds(t *testing.T) {
	h := protocol.NewReplayHARExchange()
	h.RequestBody = &protocol.ReplayHARBody{Text: `{"password":"private-value"}`, State: "retained"}
	h.ResponseBody = &protocol.ReplayHARBody{Text: strings.Repeat("x", 2048), State: "retained"}
	entries := scrubSensitiveNetworkEntries([]protocol.NetworkEntry{{HAR: h}}, nil)
	if strings.Contains(entries[0].HAR.RequestBody.Text, "private-value") {
		t.Fatal("body not scrubbed")
	}
	if h.RequestBody.Text != `{"password":"private-value"}` {
		t.Fatal("scrub mutated source")
	}
	remaining := 1100
	bounded := takeSnapshotHAR(h, &remaining)
	if bounded.ResponseBody.Text != "" || bounded.ResponseBody.State != "total_limit" || remaining < 0 {
		t.Fatal("body escaped snapshot budget")
	}
}

func TestReplayHARBodiesApplyRuntimeAndCustomRedaction(t *testing.T) {
	base, err := NewRedactionConfig([]RedactionRule{{Pattern: "custom-private-value"}})
	if err != nil {
		t.Fatal(err)
	}
	config := WithSecretValueRedaction(base, []string{"runtime-private-value"})
	h := protocol.NewReplayHARExchange()
	h.ResponseBody = &protocol.ReplayHARBody{State: "retained", Text: "body runtime-private-value custom-private-value"}
	e := scrubSensitiveNetworkEntries([]protocol.NetworkEntry{{HAR: h}}, config)
	if strings.Contains(e[0].HAR.ResponseBody.Text, "runtime-private-value") || strings.Contains(e[0].HAR.ResponseBody.Text, "custom-private-value") {
		t.Fatal("runtime secret leaked")
	}
}

func TestAdvancedTraceProtoConfigAndActionLogAttrs(t *testing.T) {
	standardScript := &dsl.Script{
		Name:     "standard",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			Log: dsl.LogConfig{
				Level:           "standard",
				ExtendedOptions: []string{"advanced_trace"},
			},
		},
	}
	if got, _ := buildProtoConfig(standardScript)[protocol.ReplayNetworkTraceConfigKey].(bool); got {
		t.Fatal("expected ReplayNetworkTraceConfigKey=false when log level is standard without on_error_only")
	}

	extendedScript := &dsl.Script{
		Name:     "extended",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			Log: dsl.LogConfig{
				Level:           "extended",
				ExtendedOptions: []string{"advanced_trace"},
			},
		},
	}
	if got, _ := buildProtoConfig(extendedScript)[protocol.ReplayNetworkTraceConfigKey].(bool); !got {
		t.Fatal("expected ReplayNetworkTraceConfigKey=true when log level is extended with advanced_trace")
	}

	reused := true
	result := &protocol.Result{
		StatusCode:       200,
		DependencyTarget: "api.example.com",
		NetworkTrace: &protocol.HTTPNetworkTrace{
			HTTPProtocol:            "HTTP/2.0",
			RemoteAddr:              "203.0.113.10:443",
			ConnectionReused:        &reused,
			WasIdle:                 true,
			IdleTimeMs:              18.5,
			TLSVersion:              "TLS 1.3",
			TLSCipherSuite:          "TLS_AES_128_GCM_SHA256",
			TLSNegotiatedProto:      "h2",
			TLSDidResume:            true,
			RequestCompression:      "gzip",
			ContentEncoding:         "gzip",
			EncodedBodyBytes:        128,
			DecodedBodyBytes:        512,
			CookiesSentCount:        2,
			SetCookiesReceivedCount: 1,
		},
	}
	attrs := actionLogAttrs("step-1", nil, result, extendedScript.Config.Log, false, nil)
	joined := fmt.Sprint(attrs)
	for _, want := range []string{"http_proto HTTP/2.0", "remote_addr 203.0.113.10:443", "tls_version TLS 1.3", "alpn h2", "encoded_bytes 128", "decoded_bytes 512", "cookies_sent 2", "set_cookies 1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in actionLogAttrs: %s", want, joined)
		}
	}
}

func TestNetworkTraceRedactionAndSnapshotBounds(t *testing.T) {
	config := WithSecretValueRedaction(nil, []string{"secret-sni-host.internal"})
	src := &protocol.HTTPNetworkTrace{
		RemoteAddr:     "10.0.0.1:443",
		TLSServerName:  "secret-sni-host.internal",
		TLSCertSubject: "CN=secret-sni-host.internal",
	}
	entries := scrubSensitiveNetworkEntries([]protocol.NetworkEntry{{Trace: src}}, config)
	if entries[0].Trace == nil || strings.Contains(entries[0].Trace.TLSServerName, "secret-sni-host.internal") || strings.Contains(entries[0].Trace.TLSCertSubject, "secret-sni-host.internal") {
		t.Fatalf("expected trace fields to be scrubbed, got %+v", entries[0].Trace)
	}
	if src.TLSServerName != "secret-sni-host.internal" {
		t.Fatal("scrubSensitiveNetworkEntries mutated original trace")
	}

	remaining := 2048
	bounded := takeSnapshotNetworkEntries([]protocol.NetworkEntry{{URL: "https://example.com", Trace: src}}, &remaining)
	if len(bounded) != 1 || bounded[0].Trace == nil || bounded[0].Trace.RemoteAddr != "10.0.0.1:443" {
		t.Fatalf("expected snapshot to preserve trace within budget, got %+v", bounded)
	}
}

func TestNetworkTraceCloneAndConcurrentOmissionBounds(t *testing.T) {
	manyIPs := make([]string, 20)
	for i := range manyIPs {
		manyIPs[i] = fmt.Sprintf("192.0.2.%d", i+1)
	}
	manyCookies := make([]string, 50)
	for i := range manyCookies {
		manyCookies[i] = fmt.Sprintf("cookie_%d", i+1)
	}
	oversizedSubject := strings.Repeat("é", 200) // 400 bytes in UTF-8
	raw := &protocol.HTTPNetworkTrace{
		DNSResolvedIPs:  manyIPs,
		CookieNamesSent: manyCookies,
		TLSCertSubject:  oversizedSubject,
		RemoteAddr:      "192.0.2.1:443",
	}
	cloned := raw.Clone()
	if len(cloned.DNSResolvedIPs) != 8 {
		t.Fatalf("expected DNSResolvedIPs bounded to 8, got %d", len(cloned.DNSResolvedIPs))
	}
	if len(cloned.CookieNamesSent) != 32 {
		t.Fatalf("expected CookieNamesSent bounded to 32, got %d", len(cloned.CookieNamesSent))
	}
	if len(cloned.TLSCertSubject) > 256 || !utf8.ValidString(cloned.TLSCertSubject) {
		t.Fatalf("expected TLSCertSubject bounded to <=256 valid UTF-8 bytes, got len=%d valid=%v", len(cloned.TLSCertSubject), utf8.ValidString(cloned.TLSCertSubject))
	}

	res := &protocol.Result{
		StatusCode:   200,
		NetworkTrace: raw,
	}
	boundResultEvidence(res)
	if res.NetworkTrace == nil || len(res.NetworkTrace.DNSResolvedIPs) != 8 || len(res.NetworkTrace.CookieNamesSent) != 32 {
		t.Fatalf("expected boundResultEvidence to bound NetworkTrace, got %+v", res.NetworkTrace)
	}

	omitConcurrentPublicEvidence(res)
	if res.NetworkTrace != nil {
		t.Fatalf("expected omitConcurrentPublicEvidence to clear NetworkTrace, got %+v", res.NetworkTrace)
	}
}

type networkTraceConfigInspectProtocol struct {
	gotTrace bool
	gotHAR   bool
}

func (p *networkTraceConfigInspectProtocol) Name() string { return "trace-config-inspect" }
func (p *networkTraceConfigInspectProtocol) Init(_ context.Context, cfg map[string]any) error {
	p.gotTrace, _ = cfg[protocol.ReplayNetworkTraceConfigKey].(bool)
	_, p.gotHAR = cfg[protocol.ReplayHARCaptureConfigKey].(*protocol.ReplayHARCapture)
	return nil
}
func (p *networkTraceConfigInspectProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{ActionName: action.Name, StatusCode: 200, Success: true}, nil
}
func (p *networkTraceConfigInspectProtocol) Teardown(_ context.Context) error    { return nil }
func (p *networkTraceConfigInspectProtocol) HealthCheck(_ context.Context) error { return nil }

type noopDebugController struct{}

func (noopDebugController) BeforeAction(context.Context, ReplayDebugAction) error { return nil }

func TestReplayDebugNetworkTraceExplicitDisableOverridesScriptConfig(t *testing.T) {
	var inspected *networkTraceConfigInspectProtocol
	const protoName = "trace-config-inspect"
	protocol.Register(protoName, func() protocol.Protocol {
		inspected = &networkTraceConfigInspectProtocol{}
		return inspected
	})

	script := &dsl.Script{
		Name:     "debug-override-script",
		Protocol: protoName,
		Config: dsl.ScriptConfig{
			Log: dsl.LogConfig{
				Level:           "extended",
				ExtendedOptions: []string{"advanced_trace"},
			},
		},
		Actions: []dsl.Action{
			{Name: "step-1", Type: "get", Target: "https://example.com"},
		},
	}

	falseVal := false
	_, err := ReplayScript(context.Background(), script, ReplayOptions{
		DebugController: noopDebugController{},
		RuntimeProfile: &ReplayRuntimeProfile{
			NetworkTrace: &falseVal,
		},
	})
	if err != nil {
		t.Fatalf("ReplayScript failed: %v", err)
	}
	if inspected == nil {
		t.Fatal("expected protocol Init to be called")
	}
	if inspected.gotTrace {
		t.Fatal("expected ReplayNetworkTraceConfigKey to be disabled when RuntimeProfile.NetworkTrace is explicitly false")
	}
	if !inspected.gotHAR {
		t.Fatal("expected default ReplayHARCaptureConfigKey to remain enabled for debug session when HAROptions is nil")
	}
}

// The network_trace evidence keys cookie_names_sent and
// set_cookie_names_received contain "cookie"; the generic key-name rule must
// not turn those name lists into the string "[REDACTED]", which the debugger
// panel cannot render. Each name is still value-scrubbed.
func TestNetworkTraceEvidenceKeepsCookieNameListsThroughResultRedaction(t *testing.T) {
	config := WithSecretValueRedaction(nil, []string{"leaked-secret-name"})
	fresh := false
	trace := &protocol.HTTPNetworkTrace{
		RemoteAddr:              "203.0.113.7:443",
		ConnectionReused:        &fresh,
		CookiesSentCount:        2,
		CookieNamesSent:         []string{"sid", "leaked-secret-name"},
		SetCookiesReceivedCount: 1,
		SetCookieNamesReceived:  []string{"csrf"},
		StrippedRedirectHeaders: []string{"Cookie"},
	}
	result := &protocol.Result{
		StatusCode:       200,
		NetworkTrace:     trace,
		ProtocolEvidence: map[string]any{"network_trace": trace.EvidenceMap(), "cookie_jar": "kept-secret"},
	}
	scrubSensitiveResultWithOptions(result, config, true)

	if result.ProtocolEvidence["cookie_jar"] != redactedPayloadValue {
		t.Fatalf("expected the ordinary key-name rule to still apply, got %v", result.ProtocolEvidence["cookie_jar"])
	}
	encoded, err := json.Marshal(result.ProtocolEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "leaked-secret-name") {
		t.Fatalf("secret cookie name survived redaction: %s", encoded)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	evidence, ok := decoded["network_trace"].(map[string]any)
	if !ok {
		t.Fatalf("expected network_trace to stay an object, got %T", decoded["network_trace"])
	}
	for _, key := range []string{"cookie_names_sent", "set_cookie_names_received", "stripped_redirect_headers"} {
		if _, isList := evidence[key].([]any); !isList {
			t.Fatalf("expected %s to stay a list after redaction, got %T (%v)", key, evidence[key], evidence[key])
		}
	}
	if reused, present := evidence["connection_reused"]; !present || reused != false {
		t.Fatalf("expected connection_reused=false kept, got %v (present=%v)", reused, present)
	}
	roundTrip := protocol.HTTPNetworkTraceFromEvidence(evidence)
	if roundTrip == nil || len(roundTrip.CookieNamesSent) != 2 || roundTrip.CookieNamesSent[0] != "sid" {
		t.Fatalf("expected both cookie names to round-trip (one value-scrubbed), got %+v", roundTrip)
	}
	if !slices.Equal(roundTrip.SetCookieNamesReceived, []string{"csrf"}) || roundTrip.CookiesSentCount != 2 {
		t.Fatalf("expected the struct and evidence views to agree, got %+v", roundTrip)
	}
	if !slices.Equal(result.NetworkTrace.SetCookieNamesReceived, roundTrip.SetCookieNamesReceived) {
		t.Fatalf("struct view %v and evidence view %v disagree", result.NetworkTrace.SetCookieNamesReceived, roundTrip.SetCookieNamesReceived)
	}
}

// A web_concurrent child's scrubbed network_trace evidence must already be
// JSON-native: boundConcurrentCompletedResult compares the scrubbed evidence
// with its bounded copy and drops all protocol evidence on any difference, and
// the bounder turns []string lists into []any.
func TestConcurrentChildKeepsNetworkTraceEvidenceWithLists(t *testing.T) {
	trace := &protocol.HTTPNetworkTrace{
		RemoteAddr:       "203.0.113.7:443",
		DNSResolvedIPs:   []string{"203.0.113.7", "203.0.113.8"},
		TransferEncoding: []string{"chunked"},
		CookiesSentCount: 1,
		CookieNamesSent:  []string{"sid"},
	}
	result := &protocol.Result{
		Success:          true,
		StatusCode:       200,
		NetworkTrace:     trace,
		ProtocolEvidence: map[string]any{"network_trace": trace.EvidenceMap()},
	}
	if _, err := boundConcurrentCompletedResult(result, 2, nil); err != nil {
		t.Fatalf("boundConcurrentCompletedResult: %v", err)
	}
	if complete, _ := result.ProtocolEvidence["concurrent_group_evidence_details_complete"].(bool); !complete {
		t.Fatalf("expected complete concurrent evidence, got %v", result.ProtocolEvidence)
	}
	if result.NetworkTrace == nil {
		t.Fatal("expected the typed network trace to survive bounding")
	}
	evidence, ok := result.ProtocolEvidence["network_trace"].(map[string]any)
	if !ok {
		t.Fatalf("expected network_trace evidence to survive bounding, got %T", result.ProtocolEvidence["network_trace"])
	}
	names, isList := evidence["cookie_names_sent"].([]any)
	if !isList || len(names) != 1 || names[0] != "sid" {
		t.Fatalf("expected cookie_names_sent to stay a list of names, got %T (%v)", evidence["cookie_names_sent"], evidence["cookie_names_sent"])
	}
	roundTrip := protocol.HTTPNetworkTraceFromEvidence(evidence)
	if roundTrip == nil || !slices.Equal(roundTrip.DNSResolvedIPs, trace.DNSResolvedIPs) || !slices.Equal(roundTrip.TransferEncoding, []string{"chunked"}) {
		t.Fatalf("expected list fields to round-trip, got %+v", roundTrip)
	}
}

// Every byte a retained trace keeps is charged to the snapshot budget.
func TestNetworkTraceSnapshotChargesEveryRetainedString(t *testing.T) {
	longNames := make([]string, 32)
	for i := range longNames {
		longNames[i] = fmt.Sprintf("%03d-%s", i, strings.Repeat("n", 120))
	}
	headers := make([]string, 16)
	for i := range headers {
		headers[i] = fmt.Sprintf("X-Header-%02d-%s", i, strings.Repeat("h", 200))
	}
	trace := &protocol.HTTPNetworkTrace{
		RemoteAddr:              "203.0.113.9:443",
		TLSVersion:              "TLS 1.3",
		TLSCertExpiry:           "2030-01-01T00:00:00Z",
		ContentEncoding:         "gzip",
		CookieNamesSent:         longNames,
		SetCookieNamesReceived:  longNames,
		StrippedRedirectHeaders: headers,
		TransferEncoding:        headers,
		DNSResolvedIPs:          []string{"203.0.113.9", "203.0.113.10"},
	}
	retainedBytes := func(c *protocol.HTTPNetworkTrace) int {
		total := 0
		for _, value := range []string{c.RemoteAddr, c.LocalAddr, c.TLSVersion, c.TLSCipherSuite, c.TLSNegotiatedProto, c.TLSServerName, c.TLSCertSubject, c.TLSCertIssuer, c.TLSCertExpiry, c.HTTPProtocol, c.RequestCompression, c.ContentEncoding} {
			total += len(value)
		}
		for _, list := range [][]string{c.DNSResolvedIPs, c.TransferEncoding, c.StrippedRedirectHeaders, c.CookieNamesSent, c.SetCookieNamesReceived} {
			for _, value := range list {
				total += len(value)
			}
		}
		return total
	}

	const budget = 64 << 10
	remaining := budget
	got := takeSnapshotNetworkTrace(trace, &remaining)
	if got == nil || len(got.CookieNamesSent) != 32 {
		t.Fatalf("expected the trace retained within a large budget, got %+v", got)
	}
	if charged := budget - remaining; charged < retainedBytes(got) {
		t.Fatalf("charged %d bytes for a trace that retains %d", charged, retainedBytes(got))
	}

	const tight = 2048
	remaining = tight
	got = takeSnapshotNetworkTrace(trace, &remaining)
	if remaining < 0 {
		t.Fatalf("snapshot budget overdrawn: remaining=%d", remaining)
	}
	if got != nil && tight-remaining < retainedBytes(got) {
		t.Fatalf("charged %d bytes for a trace that retains %d under a tight budget", tight-remaining, retainedBytes(got))
	}
}

func TestCompactMetricsResultDropsNetworkTrace(t *testing.T) {
	compacted := compactMetricsResult(&protocol.Result{ActionName: "a", NetworkTrace: &protocol.HTTPNetworkTrace{RemoteAddr: "203.0.113.1:443"}})
	if compacted.NetworkTrace != nil {
		t.Fatalf("expected queued metric copies not to retain the network trace, got %+v", compacted.NetworkTrace)
	}
}

// A debug replay with no chosen HAR options records the implicit capture
// defaults, so its report and HAR export describe what was captured.
func TestDebugReplayRuntimeProfileRecordsDefaultHAROptions(t *testing.T) {
	profile := buildReplayRuntimeProfile(&dsl.Script{Name: "s", Protocol: "http"}, ReplayOptions{DebugController: noopDebugController{}})
	if profile.HAROptions == nil || *profile.HAROptions != DefaultDebugReplayHAROptions() {
		t.Fatalf("expected default HAR options recorded, got %+v", profile.HAROptions)
	}
	if !profile.HAROptions.Redirects || !profile.HAROptions.Timings || profile.HAROptions.Bodies != "none" || profile.HAROptions.MaxBodyBytes == 0 {
		t.Fatalf("expected normalized none/redirects/timings defaults, got %+v", profile.HAROptions)
	}
	if plain := buildReplayRuntimeProfile(&dsl.Script{Name: "s", Protocol: "http"}, ReplayOptions{}); plain.HAROptions != nil {
		t.Fatalf("expected a non-debug replay to keep HAR options unset, got %+v", plain.HAROptions)
	}
}
