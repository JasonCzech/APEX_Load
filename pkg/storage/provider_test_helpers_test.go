package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func providerReceipt(t *testing.T, job string, sequence int64, payload ProviderMonitorBatch, at time.Time) MonitorBatchReceipt {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	return MonitorBatchReceipt{JobID: job, Sequence: sequence, Digest: hex.EncodeToString(hash[:]), ArtifactKey: "fixture", SampleCount: len(payload.Points), ReceivedAt: at}
}
