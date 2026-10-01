package collectoragent

import (
	"crypto/ecdh"
	"crypto/rand"
	"testing"
	"time"
)

func TestSealedMonitorCredentialsIdentityAndExpiry(t *testing.T) {
	key, _ := ecdh.X25519().GenerateKey(rand.Reader)
	now := time.Now().UTC()
	job := RunJob{ID: "j", ProjectID: "p", CollectorID: "c", SourceID: "s", SourceRevision: 1, Pack: "ssh-linux", Destination: "ssh://server", LeaseExpiresAt: now.Add(time.Minute)}
	sealed, err := SealCredentials(job, key.PublicKey().Bytes(), Credentials{Username: "u", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	job.Credentials = sealed
	got, err := OpenCredentials(job, key.Bytes(), now)
	if err != nil || got.Password != "secret" {
		t.Fatalf("open: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*RunJob)
	}{
		{"destination", func(j *RunJob) { j.Destination = "ssh://other" }}, {"project", func(j *RunJob) { j.ProjectID = "other" }}, {"lease", func(j *RunJob) { j.LeaseExpiresAt = j.LeaseExpiresAt.Add(time.Second) }}, {"revision", func(j *RunJob) { j.SourceRevision++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modified := job
			tc.change(&modified)
			if _, err := OpenCredentials(modified, key.Bytes(), now); err == nil {
				t.Fatal("identity substitution accepted")
			}
		})
	}
	if _, err := OpenCredentials(job, key.Bytes(), now.Add(2*time.Minute)); err == nil {
		t.Fatal("expired credentials accepted")
	}
	other, _ := ecdh.X25519().GenerateKey(rand.Reader)
	if _, err := OpenCredentials(job, other.Bytes(), now); err == nil {
		t.Fatal("wrong collector accepted")
	}
}
