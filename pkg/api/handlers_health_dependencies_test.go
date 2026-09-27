package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type unavailableHealthInventory struct {
	stubInventoryBackend
	calls   atomic.Int32
	release chan struct{}
}

func (b *unavailableHealthInventory) SystemInventory(ctx context.Context) []GeneratorInventoryEntry {
	b.calls.Add(1)
	select {
	case <-ctx.Done():
	case <-b.release:
	}
	return nil
}

type unavailableHealthSettings struct {
	settingsPersistence
	calls   atomic.Int32
	release chan struct{}
}

func (s *unavailableHealthSettings) GetSetting(string) (string, error) {
	s.calls.Add(1)
	<-s.release
	return "", nil
}

func TestHealthProbeDoesNotContactDependencies(t *testing.T) {
	for _, tc := range []struct {
		name     string
		age      time.Duration
		wantLive bool
	}{
		{"fresh inventory", 0, true},
		{"future inventory", -time.Hour, false},
		{"stale inventory", 2 * defaultGeneratorSnapshotTTL, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
			backend := &unavailableHealthInventory{release: make(chan struct{})}
			settings := &unavailableHealthSettings{release: make(chan struct{})}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(backend.release); close(settings.release) }) }
			defer release()
			server.runner.setExecutionBackend(backend)
			server.runner.settingsStore = settings
			server.runner.mu.Lock()
			server.runner.settings.CapacityPlanning.Availability.Source = capacityAvailabilityConfiguredPlusLive
			server.runner.mu.Unlock()
			server.runner.setCachedGeneratorInventory([]GeneratorInventoryEntry{{Target: "offline:9090", Status: "connected", AvailableCapacity: 10}}, time.Now().Add(-tc.age))
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				resp := httptest.NewRecorder()
				server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/system/health", nil))
				done <- resp
			}()
			var resp *httptest.ResponseRecorder
			select {
			case resp = <-done:
			case <-time.After(500 * time.Millisecond):
				release()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("handler did not exit after dependencies were released")
				}
				t.Fatal("liveness waited for an unavailable dependency")
			}
			if resp.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
			}
			var payload struct {
				Capacity struct {
					Live *int `json:"live_available_generators"`
				} `json:"capacity_availability"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if (payload.Capacity.Live != nil) != tc.wantLive {
				t.Errorf("live count=%v, want present=%v", payload.Capacity.Live, tc.wantLive)
			}
			if backend.calls.Load() != 0 || settings.calls.Load() != 0 {
				t.Fatalf("dependency calls: inventory=%d settings=%d", backend.calls.Load(), settings.calls.Load())
			}
		})
	}
}
