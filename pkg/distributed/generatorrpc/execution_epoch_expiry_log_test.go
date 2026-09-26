package generatorrpc

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type lockedLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A controller loss stops load through epoch expiry, not run supervision, so
// the expiry itself must leave a record naming the runs it stopped.
func TestExecutionEpochExpiryLogsStoppedAssignments(t *testing.T) {
	logs := &lockedLogBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	service.now = func() time.Time { return time.Unix(0, clock.Load()) }
	if ack := claimEpoch(t, service, 7, time.Minute); !ack.GetAccepted() {
		t.Fatalf("claim rejected: %s", ack.GetErrorMessage())
	}
	hash := distributeEpochScript(t, service, 7, "script", []byte(`{"name":"logged"}`))
	if ack := assignEpochTest(t, service, 7, "logged-run", "script", hash); !ack.GetAccepted() {
		t.Fatalf("assignment rejected: %s", ack.GetErrorMessage())
	}
	service.mu.RLock()
	expiry := service.executionExpiresAt
	service.mu.RUnlock()
	clock.Store(expiry.UnixNano())
	service.expireExecutionEpoch(7, expiry)
	deadline := time.Now().Add(2 * time.Second)
	for {
		output := logs.String()
		if strings.Contains(output, `"event":"generator_execution_epoch_expired"`) {
			if !strings.Contains(output, `"logged-run"`) || !strings.Contains(output, `"execution_generation":7`) {
				t.Fatalf("expiry log does not name the stopped run and generation:\n%s", output)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no generator_execution_epoch_expired log after expiry:\n%s", output)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
