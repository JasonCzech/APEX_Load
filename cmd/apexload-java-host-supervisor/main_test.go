package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProfileBindingAndBounds(t *testing.T) {
	p := imageProfile{Contract: broker.SupervisorContract, BrokerClientURI: "spiffe://apexload/java-broker", LeaseTTLMS: 5000, PreparationTimeoutMS: 120000, InvocationTimeoutMS: 30000}
	data, _ := json.Marshal(p)
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	path := filepath.Join(t.TempDir(), "profile.json")
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	got, e := loadProfile(path, digest)
	if e != nil || got.LeaseTTLMS != 5000 {
		t.Fatal(got, e)
	}
	if _, e = loadProfile(path, "bad"); e == nil {
		t.Fatal("unbound profile")
	}
	for _, bad := range [][]byte{append(data, []byte(" {}")...), []byte(`{"contract":"wrong"}`), make([]byte, (512<<10)+1)} {
		if e := os.WriteFile(path, bad, 0600); e != nil {
			t.Fatal(e)
		}
		sum = sha256.Sum256(bad)
		if _, e := loadProfile(path, hex.EncodeToString(sum[:])); e == nil {
			t.Fatal("bad profile accepted")
		}
	}
	if _, e := readBounded(t.TempDir(), 100); e == nil {
		t.Fatal("directory accepted")
	}
}
func TestSupervisorRefusesOrdinaryProcess(t *testing.T) {
	if os.Getpid() == 1 {
		t.Skip("test requires ordinary process")
	}
	if e := run(nil); !errors.Is(e, javarunner.ErrPID1Isolation) {
		t.Fatal(e)
	}
	s := newHTTPServer(nil, time.Minute)
	if s.ReadTimeout <= 0 || s.ReadHeaderTimeout <= 0 || s.IdleTimeout <= 0 || s.WriteTimeout <= time.Minute || s.MaxHeaderBytes != 8<<10 {
		t.Fatal("unbounded server")
	}
}
