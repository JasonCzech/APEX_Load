package engine

import (
	"context"
	"fmt"
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

	result := &protocol.Result{
		StatusCode:       200,
		DependencyTarget: "api.example.com",
		NetworkTrace: &protocol.HTTPNetworkTrace{
			HTTPProtocol:            "HTTP/2.0",
			RemoteAddr:              "203.0.113.10:443",
			ConnectionReused:        true,
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

