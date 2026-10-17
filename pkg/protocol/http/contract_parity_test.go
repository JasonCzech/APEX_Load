package http

import (
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

func TestRuntimeContractDefaultsMatchAuthoring(t *testing.T) {
	runtime := parseTransportConfig(nil)
	authored := dsl.DefaultScriptConfig().HTTP
	if runtime.maxConnsPerHost != authored.MaxConnsPerHost || runtime.maxIdleConns != authored.MaxIdleConns || runtime.connectTimeout != time.Duration(authored.ConnectTimeoutMs)*time.Millisecond || runtime.requestTimeout != time.Duration(authored.RequestTimeoutMs)*time.Millisecond || runtime.tlsTimeout != time.Duration(authored.TLSTimeoutMs)*time.Millisecond || runtime.idleTimeout != time.Duration(authored.IdleTimeoutMs)*time.Millisecond {
		t.Fatalf("runtime %+v does not match authoring %+v", runtime, authored)
	}
	if runtime.disableKeepAlive == *authored.KeepAlive || parseMaxRedirects(nil) != *authored.MaxRedirects {
		t.Fatal("runtime defaults differ from authored defaults")
	}
	tlsConfig, err := buildTLSConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	version, err := parseTLSVersion(authored.TLS.MinVersion)
	if err != nil || tlsConfig.MinVersion != version {
		t.Fatal("TLS default mismatch")
	}
	if jar := buildCookieJar(nil); (jar != nil) != *authored.CookiesEnabled {
		t.Fatalf("cookie default: %v", jar)
	}
	if _, err := protocol.Get("http"); err != nil {
		t.Fatalf("runtime registration lost: %v", err)
	}
}

func TestRuntimeCapabilitiesRetainAvailabilityGates(t *testing.T) {
	for _, enabled := range []string{"0", "1"} {
		t.Setenv("APEXLOAD_HTTP3", enabled)
		want := httpcontract.Capabilities()
		want.Versions = []string{VersionHTTP11, VersionHTTP2}
		if HTTP3ExecutionError() == nil {
			want.Versions = append(want.Versions, VersionHTTP3)
		}
		if HTTP3UpgradeExecutionError() == nil {
			want.Versions = append(want.Versions, VersionAutoH3)
		}
		if got := Capabilities(); !reflect.DeepEqual(got, want) {
			t.Fatalf("runtime capabilities = %#v, want %#v", got, want)
		}
	}
}

func TestAltSvcRuntimeFacadeParity(t *testing.T) {
	for _, values := range [][]string{nil, {`h3=":443"; ma=60`}, {"clear"}, {`h3-29=":443"`}, {"invalid"}} {
		if got, want := ParseAltSvc(values), httpcontract.ParseAltSvc(values); !reflect.DeepEqual(got, want) {
			t.Fatalf("Alt-Svc %q: %+v, want %+v", values, got, want)
		}
		if AltSvcAdvertisesH3(values) != httpcontract.AltSvcAdvertisesH3(values) {
			t.Fatal("Alt-Svc capability mismatch")
		}
	}
}
