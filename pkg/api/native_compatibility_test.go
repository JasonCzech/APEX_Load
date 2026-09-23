package api

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"testing"
)

func TestNativeCompatibilityOverrideRoundTrip(t *testing.T) {
	source := &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{ReceiveBufferBytes: 12288, MaxMetaRefresh: 2, ContentCheckErrorLimit: 10, ProxyMode: "pac", Proxy: dsl.ProxyConfig{PACURL: "https://example.invalid/proxy.pac"}}, Log: &dsl.LogConfig{OnErrorOnly: true, ErrorBufferBytes: 102400}, BrowserConfig: &dsl.BrowserConfig{SystemLanguage: "en-US", AvailableScreenWidth: 1920, AvailableScreenHeight: 1008}, WebSocket: &dsl.WebSocketConfig{CallbackIntervalMs: 500}}
	clone := cloneRuntimeSettings(source)
	clone.WebSocket.CallbackIntervalMs = 250
	clone.HTTP.Proxy.PACURL = "https://changed.invalid/pac"
	if source.WebSocket.CallbackIntervalMs != 500 || source.HTTP.Proxy.PACURL != "https://example.invalid/proxy.pac" {
		t.Fatal("clone aliases source")
	}
	script := &dsl.Script{}
	if err := applyRuntimeOverrides(script, source); err != nil {
		t.Fatal(err)
	}
	if script.Config.HTTP.ReceiveBufferBytes != 12288 || script.Config.HTTP.Proxy.PACURL == "" || script.Config.Log.ErrorBufferBytes != 102400 || script.Config.Browser.AvailableScreenWidth != 1920 || script.Config.WebSocket.CallbackIntervalMs != 500 {
		t.Fatal("override fields lost")
	}
	merged := mergeRuntimeSettings(source, &RuntimeSettingsOverride{WebSocket: &dsl.WebSocketConfig{CallbackIntervalMs: 100}})
	if merged.WebSocket.CallbackIntervalMs != 100 || source.WebSocket.CallbackIntervalMs != 500 {
		t.Fatal("merge or isolation broken")
	}
	if !fieldApplicableForProtocol("websocket", "websocket") || fieldApplicableForProtocol("websocket", "http") {
		t.Fatal("callback override applicability incorrect")
	}
}
