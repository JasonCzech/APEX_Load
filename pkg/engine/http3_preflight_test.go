package engine

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func TestProbeHTTP3GroupChecksStaticOriginsWithoutRequests(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	t.Setenv(httpprotocol.EnvHTTP3, "true")
	dead, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadPort := dead.LocalAddr().(*net.UDPAddr).Port
	_ = dead.Close()
	script := &dsl.Script{Name: "probe", Protocol: "http", Config: dsl.ScriptConfig{
		BaseURL: fmt.Sprintf("https://127.0.0.1:%d", fixture.Port),
		HTTP:    dsl.HTTPConfig{Version: "3", TLS: dsl.TLSConfig{InsecureSkipVerify: true}, QUIC: &dsl.HTTPQUICConfig{HandshakeTimeoutMs: 1000}},
	}, Actions: []dsl.Action{
		{Name: "home", Type: "GET", Target: "/"},
		{Name: "dead", Type: "GET", Target: fmt.Sprintf("https://127.0.0.1:%d/", deadPort)},
		{Name: "dynamic", Type: "GET", Target: "https://{{param.host}}/"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := ProbeHTTP3Group(ctx, GroupConfig{Name: "buyers", Script: script})
	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	if !results[0].OK || results[0].Group != "buyers" || results[0].HandshakeMs <= 0 || results[0].Blocking() {
		t.Fatalf("reachable origin = %+v", results[0])
	}
	if results[1].OK || results[1].ErrorClass == "" || !results[1].Blocking() {
		t.Fatalf("dead origin = %+v", results[1])
	}
	if !strings.HasPrefix(results[2].Skipped, "dynamic target") || results[2].Blocking() {
		t.Fatalf("dynamic origin = %+v", results[2])
	}
	if n := fixture.Ledger.Count(func(h3fixture.Attempt) bool { return true }); n != 0 {
		t.Fatalf("the preflight sent %d requests", n)
	}

	proxied := *script
	proxied.Config.HTTP.Proxy.URL = "http://proxy.example:3128"
	for _, result := range ProbeHTTP3Group(ctx, GroupConfig{Name: "proxied", Script: &proxied}) {
		if result.Skipped == "" || result.Blocking() {
			t.Fatalf("a proxied route is not checked: %+v", result)
		}
	}
	if ProbeHTTP3Group(ctx, GroupConfig{Name: "tcp", Script: &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://shop.example"}, Actions: []dsl.Action{{Name: "a", Type: "GET", Target: "/"}}}}) != nil {
		t.Fatal("a TCP-only group has nothing to check")
	}
}
