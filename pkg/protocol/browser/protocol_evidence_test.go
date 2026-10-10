package browser

import (
	"slices"
	"strings"
	"testing"
)

func TestBrowserResourceProtocolDecision(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name           string
		raw            string
		responded      bool
		cached         bool
		serviceWorker  *bool
		protocol, want string
	}{
		{name: "h2", raw: "h2", responded: true, protocol: "h2"},
		{name: "h3", raw: "h3", responded: true, protocol: "h3"},
		{name: "http/1.1", raw: "http/1.1", responded: true, protocol: "h1"},
		{name: "no response", raw: "h3", responded: false, want: "no_response"},
		{name: "cached wins", raw: "h3", responded: true, cached: true, want: "cached"},
		{name: "service worker", raw: "h2", responded: true, serviceWorker: &yes, want: "service_worker"},
		{name: "network not service worker", raw: "h2", responded: true, serviceWorker: &no, protocol: "h2"},
		{name: "draft", raw: "h3-29", responded: true, want: "draft_protocol"},
		{name: "empty", raw: "", responded: true, want: "not_exposed"},
		{name: "data url", raw: "data", responded: true, want: "not_network"},
		{name: "oversized", raw: strings.Repeat("h", maxBrowserProtocolBytes+1), responded: true, want: "unsupported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			protocol, reason := browserResourceProtocol(tc.raw, tc.responded, tc.cached, tc.serviceWorker)
			if protocol != tc.protocol || reason != tc.want {
				t.Fatalf("got %q/%q, want %q/%q", protocol, reason, tc.protocol, tc.want)
			}
		})
	}
}

func TestBrowserDocumentBindingAdmitsResourceProtocolFieldsOnlyOnResources(t *testing.T) {
	resource := func() map[string]any {
		fields := validBrowserDocumentPayloadMap(t)
		fields["kind"] = "resource"
		fields["protocol"] = "h3"
		fields["transfer_size"] = float64(1234)
		return fields
	}
	if payload, ok := decodeBrowserDocumentPayload(resource()); !ok || payload.Protocol != "h3" || payload.TransferSize == nil || *payload.TransferSize != 1234 {
		t.Fatalf("resource payload with protocol refused: %+v %v", payload, ok)
	}
	nullTransfer := resource()
	nullTransfer["transfer_size"] = nil
	if _, ok := decodeBrowserDocumentPayload(nullTransfer); !ok {
		t.Fatal("null transfer size refused")
	}
	for name, change := range map[string]func(map[string]any){
		"metric with protocol":       func(m map[string]any) { m["kind"] = "metric" },
		"protocol too long":          func(m map[string]any) { m["protocol"] = strings.Repeat("h", maxBrowserProtocolBytes+1) },
		"protocol not text":          func(m map[string]any) { m["protocol"] = float64(3) },
		"negative transfer":          func(m map[string]any) { m["transfer_size"] = float64(-1) },
		"base field swapped out":     func(m map[string]any) { delete(m, "state"); delete(m, "transfer_size"); m["extra"] = "x" },
		"extras replace base fields": func(m map[string]any) { delete(m, "state"); delete(m, "boundary") },
	} {
		t.Run(name, func(t *testing.T) {
			fields := resource()
			change(fields)
			if _, ok := decodeBrowserDocumentPayload(fields); ok {
				t.Fatal("malformed resource envelope admitted")
			}
		})
	}
}

func TestBrowserQUICModeLaunchAndPoolKey(t *testing.T) {
	if options := chromiumLaunchOptions(true, "", browserQUICOff); !slices.Contains(options.Args, "--disable-quic") {
		t.Fatalf("QUIC off launch args %v", options.Args)
	}
	if options := chromiumLaunchOptions(true, "", browserQUICAuto); slices.Contains(options.Args, "--disable-quic") {
		t.Fatalf("QUIC auto launch args %v", options.Args)
	}
	if browserPoolKey("chromium", true, browserQUICOff) == browserPoolKey("chromium", true, browserQUICAuto) {
		t.Fatal("QUIC modes share a pooled browser")
	}
	if browserPoolKey("chromium", true, "") != browserPoolKey("chromium", true, browserQUICAuto) {
		t.Fatal("an empty QUIC mode must pool with auto")
	}
}

func TestBrowserQUICConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		engine, quic string
		wantErr      string
	}{
		{"chromium", "", ""},
		{"chromium", "auto", ""},
		{"chromium", " OFF ", ""},
		{"webkit", "auto", ""},
		{"webkit", "off", "not supported by WebKit"},
		{"chromium", "force", "must be auto or off"},
	} {
		var b Browser
		config := map[string]any{"browser_engine": tc.engine}
		if tc.quic != "" {
			config["browser_quic"] = tc.quic
		}
		err := b.applyConfig(config)
		if tc.wantErr == "" && err != nil {
			t.Fatalf("%s/%q: %v", tc.engine, tc.quic, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Fatalf("%s/%q: err %v, want %q", tc.engine, tc.quic, err, tc.wantErr)
		}
		if tc.quic == "" && b.quic != browserQUICAuto {
			t.Fatalf("default QUIC mode %q", b.quic)
		}
	}
}
