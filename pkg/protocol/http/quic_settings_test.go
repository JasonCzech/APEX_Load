package http

import (
	"context"
	"strings"
	"testing"
	"time"
)

func intPointer(value int) *int { return &value }

func TestQUICSettingsDefaultsMatchDecisionRecord(t *testing.T) {
	got := DefaultQUICSettings()
	want := QUICSettings{HandshakeTimeout: 5 * time.Second, MaxIdleTimeout: 30 * time.Second, AltSvcCache: "per_vuser", BrokenTTL: 5 * time.Minute}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
}

func TestValidateQUICSettingsBounds(t *testing.T) {
	cases := []struct {
		name                          string
		handshake, maxIdle, keepAlive int
		altSvc                        string
		brokenTTL                     *int
		want                          string
	}{
		{name: "unset"},
		{name: "every field at a bound", handshake: 1000, maxIdle: 600000, keepAlive: 600000, altSvc: "per_iteration", brokenTTL: intPointer(0)},
		{name: "handshake too short", handshake: 999, want: "config.http.quic.handshake_timeout_ms must be between 1000 and 60000"},
		{name: "handshake too long", handshake: 60001, want: "handshake_timeout_ms"},
		{name: "negative handshake", handshake: -1, want: "handshake_timeout_ms"},
		{name: "idle too short", maxIdle: 500, want: "config.http.quic.max_idle_timeout_ms must be between 1000 and 600000"},
		{name: "keep alive above default idle", keepAlive: 30001, want: "config.http.quic.keep_alive_ms must be between 0 and the max idle timeout (30000)"},
		{name: "keep alive above authored idle", maxIdle: 10000, keepAlive: 10001, want: "(10000)"},
		{name: "negative keep alive", keepAlive: -1, want: "keep_alive_ms"},
		{name: "unknown cache scope", altSvc: "per_run", want: "config.http.quic.alt_svc_cache must be per_vuser or per_iteration"},
		{name: "negative broken ttl", brokenTTL: intPointer(-1), want: "config.http.quic.broken_ttl_ms must be between 0 and 600000"},
		{name: "broken ttl too long", brokenTTL: intPointer(600001), want: "broken_ttl_ms"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := ValidateQUICSettings(tc.handshake, tc.maxIdle, tc.keepAlive, tc.altSvc, tc.brokenTTL)
			if tc.want == "" {
				if len(issues) != 0 {
					t.Fatalf("issues = %v", issues)
				}
				return
			}
			if len(issues) != 1 || !strings.Contains(issues[0], tc.want) {
				t.Fatalf("issues = %v, want one containing %q", issues, tc.want)
			}
		})
	}
}

func TestQUICSettingsReachTheSession(t *testing.T) {
	config := QUICSettingsConfig(2000, 60000, 15000, "per_iteration", intPointer(0))
	if len(config) != 5 {
		t.Fatalf("config keys = %v", config)
	}
	if unset := QUICSettingsConfig(0, 0, 0, "", nil); len(unset) != 0 {
		t.Fatalf("unset settings produced keys %v", unset)
	}
	h := versionHTTP(t, config)
	want := QUICSettings{HandshakeTimeout: 2 * time.Second, MaxIdleTimeout: time.Minute, KeepAlive: 15 * time.Second, AltSvcCache: "per_iteration", BrokenTTL: 0}
	if h.quicSettings != want {
		t.Fatalf("session settings = %+v, want %+v", h.quicSettings, want)
	}
	if defaults := versionHTTP(t, nil); defaults.quicSettings != DefaultQUICSettings() {
		t.Fatalf("unset session settings = %+v", defaults.quicSettings)
	}
	for _, bad := range []map[string]any{
		{quicHandshakeTimeoutKey: float64(10)},
		{quicKeepAliveKey: 1.5},
		{quicAltSvcCacheKey: "per_run"},
	} {
		h := &HTTP{}
		if err := h.Init(context.Background(), bad); err == nil {
			t.Fatalf("session accepted %v", bad)
		}
	}
}

func TestQUICOperatorLimits(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	limits, err := LoadQUICOperatorLimits(env(nil))
	if err != nil || limits != DefaultQUICOperatorLimits() {
		t.Fatalf("defaults = %+v, %v", limits, err)
	}
	if limits.MaxResponseHeaderBytes != maxHTTPResponseHeaderBytes || limits.InitialPacketSize != 1280 || limits.MaxConnectionReceiveWindow != 15<<20 {
		t.Fatalf("defaults diverge from quic-go and the TCP header limit: %+v", limits)
	}
	limits, err = LoadQUICOperatorLimits(env(map[string]string{
		EnvQUICInitialStreamReceiveWindow: "65536", EnvQUICMaxStreamReceiveWindow: "1048576",
		EnvQUICInitialConnectionReceiveWindow: "131072", EnvQUICMaxConnectionReceiveWindow: "4194304",
		EnvQUICMaxResponseHeaderBytes: "131072", EnvQUICInitialPacketSize: "1350", EnvQUICDisablePathMTUDiscovery: "true",
	}))
	want := QUICOperatorLimits{InitialStreamReceiveWindow: 65536, MaxStreamReceiveWindow: 1 << 20, InitialConnectionReceiveWindow: 128 << 10, MaxConnectionReceiveWindow: 4 << 20, MaxResponseHeaderBytes: 128 << 10, InitialPacketSize: 1350, DisablePathMTUDiscovery: true}
	if err != nil || limits != want {
		t.Fatalf("overrides = %+v, %v", limits, err)
	}
	for name, value := range map[string]string{
		EnvQUICMaxStreamReceiveWindow:  "1024",
		EnvQUICInitialPacketSize:       "1500",
		EnvQUICMaxResponseHeaderBytes:  "lots",
		EnvQUICDisablePathMTUDiscovery: "sometimes",
	} {
		if _, err := LoadQUICOperatorLimits(env(map[string]string{name: value})); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s=%s: err = %v, want one naming the variable", name, value, err)
		}
	}
	if _, err := LoadQUICOperatorLimits(env(map[string]string{EnvQUICInitialStreamReceiveWindow: "8388608"})); err == nil {
		t.Fatal("an initial stream window above the maximum was accepted")
	}
}
