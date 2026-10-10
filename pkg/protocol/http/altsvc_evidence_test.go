package http

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
)

func TestAltSvcH3AdvertisementIsObservedButNeverRoutes(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort, AltSvcMaxAge: 60})
	h := versionHTTP(t, nil)
	for _, tc := range []struct{ policy, negotiated string }{{"auto", "2"}, {"1.1", "1.1"}, {"2", "2"}} {
		r := executeVersion(t, h, fixture.URL("/?policy="+tc.policy), tc.policy)
		if r.ProtocolEvidence["alt_svc_h3_advertised"] != true {
			t.Fatalf("policy %s: evidence = %+v", tc.policy, r.ProtocolEvidence)
		}
		if r.ProtocolEvidence["http_version_negotiated"] != tc.negotiated {
			t.Fatalf("policy %s negotiated %v, want %s", tc.policy, r.ProtocolEvidence["http_version_negotiated"], tc.negotiated)
		}
	}
	if n := fixture.Ledger.Count(func(a h3fixture.Attempt) bool { return a.Listener != "tcp" }); n != 0 {
		t.Fatalf("an advertisement changed routing: %d QUIC attempts", n)
	}
}

func TestAltSvcH3AdvertisementAbsentForDraftsAndPlainResponses(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := versionHTTP(t, nil)
	if r := executeVersion(t, h, fixture.URL("/"), "auto"); r.ProtocolEvidence["alt_svc_h3_advertised"] != nil {
		t.Fatalf("no Alt-Svc but evidence = %+v", r.ProtocolEvidence)
	}
	fixture.SetAltSvc(`h3-29=":443"; ma=60, h2=":443"`)
	if r := executeVersion(t, h, fixture.URL("/draft"), "auto"); r.ProtocolEvidence["alt_svc_h3_advertised"] != nil {
		t.Fatalf("draft-only advertisement reported as final HTTP/3: %+v", r.ProtocolEvidence)
	}
}

func TestAltSvcH3AdvertisementNotClaimedFromCache(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort})
	h := versionHTTP(t, map[string]any{"http_cache_enabled": true})
	first := executeVersion(t, h, fixture.URL("/cacheable"), "auto")
	if first.ProtocolEvidence["alt_svc_h3_advertised"] != true || first.ProtocolEvidence["http_version_source"] != "wire" {
		t.Fatalf("wire response evidence = %+v", first.ProtocolEvidence)
	}
	second := executeVersion(t, h, fixture.URL("/cacheable"), "auto")
	if second.ProtocolEvidence["http_version_source"] != "cache" {
		t.Fatalf("second request was not a cache hit: %+v", second.ProtocolEvidence)
	}
	if second.ProtocolEvidence["alt_svc_h3_advertised"] != nil {
		t.Fatalf("cache hit claimed an Alt-Svc observation: %+v", second.ProtocolEvidence)
	}
}
