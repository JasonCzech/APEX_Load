package collectoragent

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSignedManagementJobProviderCadence(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, provider := range []struct {
		pack    string
		minimum int
	}{{"kafka-offsets", 10}, {"servicebus-runtime", 30}, {"solace-semp", 30}} {
		for _, interval := range []int{9, 10, 29, 30, 3600, 3601} {
			t.Run(fmt.Sprintf("%s/%d", provider.pack, interval), func(t *testing.T) {
				job := RunJob{ID: "job", ProjectID: "project", RunID: "run", CollectorID: "collector", SourceID: "source", SourceRevision: 1, Pack: provider.pack, Destination: "management://approved", IntervalSeconds: interval, StartedAt: now, ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute), Management: &ManagementJob{Version: 1, ScopeHash: strings.Repeat("a", 64), IdentityHash: strings.Repeat("b", 64)}}
				// A cryptographically valid old/malformed issuer cannot bypass the new
				// provider cadence at the collector's first job-admission boundary.
				signed, err := SignRunJob(job, private)
				if err != nil {
					t.Fatal(err)
				}
				_, err = VerifyRunJob(signed, public, "collector", now)
				want := interval >= provider.minimum && interval <= 3600
				if (err == nil) != want {
					t.Fatalf("interval %d admitted=%v want=%v", interval, err == nil, want)
				}
			})
		}
	}
}
