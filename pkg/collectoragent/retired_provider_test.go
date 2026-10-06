package collectoragent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRetiredProviderCannotConfigureOrReceiveSignedGrants(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = New(Config{ControllerURL: "https://controller.example", CollectorID: "collector", Token: "token", Packs: []string{"appdynamics"}, ControllerPublicKey: public}); err == nil || !strings.Contains(err.Error(), "unsupported metric pack") {
		t.Fatalf("retired configuration accepted: %v", err)
	}
	if err = validateAdapterTargets(Config{AdapterTargets: []AdapterTarget{{Pack: "appdynamics", Destination: "https://upstream.example"}}}); err == nil {
		t.Fatal("retired destination accepted")
	}
	now := time.Now().UTC()
	job := RunJob{Pack: "appdynamics", ID: "job", ProjectID: "p", RunID: "r", SourceID: "s", SourceRevision: 1, CollectorID: "collector", IntervalSeconds: 60, StartedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute)}
	if _, err = SignRunJob(job, private); err == nil {
		t.Fatal("retired grant signed")
	}
	body, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	signed := SignedRunJob{Payload: string(body), Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(private, body))}
	if _, err = VerifyRunJob(signed, public, "collector", now); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("legacy signed grant accepted: %v", err)
	}
}

func TestRetiredProviderInspectionCannotBeAssigned(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = SignInspectionJob(InspectionJob{Provider: "appdynamics"}, private); err == nil {
		t.Fatal("retired inspection signed")
	}
	body := []byte(`{"provider":"appdynamics","id":"old","project_id":"p","connection_id":"c","connection_revision":1,"collector_id":"collector"}`)
	envelope := SignedInspectionJob{Payload: string(body), Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(private, body))}
	if _, err = VerifyInspectionJob(envelope, public, "collector", time.Now()); err == nil {
		t.Fatal("old signed inspection accepted")
	}
}
