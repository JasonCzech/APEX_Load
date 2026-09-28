package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

func TestJavaHostGeneratorConfigurationOptInAndValidation(t *testing.T) {
	option, close, err := configureJavaHostRuntime("")
	if err != nil || option != nil || close == nil {
		t.Fatal(err)
	}
	if err := close(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`{}`, `{"contract":"future"}`, `{"contract":"apexload.java-placement/v1","unexpected":true}`, `{} {}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := configureJavaHostRuntime(path); err == nil {
			t.Fatal("accepted invalid config", value)
		}
	}
	if _, _, err := configureJavaHostRuntime("relative.json"); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := readJavaHostConfigFile(t.TempDir(), 100); err == nil {
		t.Fatal("directory accepted")
	}
	path := filepath.Join(t.TempDir(), "large")
	_ = os.WriteFile(path, []byte("12345"), 0600)
	if _, err := readJavaHostConfigFile(path, 4); err == nil {
		t.Fatal("oversized file accepted")
	}
}
func TestJavaHostGeneratorConfigurationStartsAndClosesOneOwner(t *testing.T) {
	var claims, releases atomic.Int32
	profile := broker.BrokerProfileCapability{ID: "approved", ConfigurationSHA256: strings.Repeat("a", 64), PolicySHA256: strings.Repeat("b", 64), MemoryBytes: 100, HostLeaseMS: 3000, PreparationMS: 5000, InvocationMS: 1000, MaxRunMS: 60000}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
			t.Error("wrong local token")
		}
		var q javasidecar.BrokerFacadeRequest
		_ = json.NewDecoder(r.Body).Decode(&q)
		out := javasidecar.BrokerFacadeResponse{Contract: javasidecar.BrokerFacadeContract, RequestID: q.RequestID}
		switch q.Operation {
		case "capabilities":
			out.Capabilities = &broker.BrokerCapabilities{Mode: "dedicated", Containment: "execution_pod", MaxHosts: 2, MaxVUsers: 2, MaxMemoryBytes: 1000, Profiles: []broker.BrokerProfileCapability{profile}}
		case "claim":
			claims.Add(1)
			out.OwnerToken = "owner"
			out.LeaseMS = 6000
		case "release":
			releases.Add(1)
			out.State = "retiring"
		default:
			t.Error("unexpected", q.Operation)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(token, []byte(strings.Repeat("x", 32)+"\n"), 0600)
	cfg := javaHostGeneratorConfig{Contract: generatorrpc.JavaHostSelectionContract, Endpoint: server.URL, TokenFile: token, Approvals: []javaHostGeneratorApproval{{ScriptSHA256: strings.Repeat("c", 64), TargetClass: "Actions", Profile: profile, FacadeResources: generatorrpc.JavaHostFacadeResources{CPU: 100, MemoryBytes: 8 << 20, NetworkKbps: 100}}}}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(path, data, 0600)
	option, close, err := configureJavaHostRuntime(path)
	if err != nil || option == nil {
		t.Fatal(err)
	}
	if err = close(); err != nil {
		t.Fatal(err)
	}
	if err = close(); err != nil {
		t.Fatal(err)
	}
	if claims.Load() != 1 || releases.Load() != 1 {
		t.Fatal(claims.Load(), releases.Load())
	}
	cfg.Approvals[0].FacadeResources.CPU = 0
	data, _ = json.Marshal(cfg)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := configureJavaHostRuntime(path); err == nil {
		t.Fatal("partial facade budget accepted")
	}
	if claims.Load() != 1 {
		t.Fatal("invalid budget claimed an owner")
	}
}
