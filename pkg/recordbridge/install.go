package recordbridge

import "sync"

// InstallSnapshot is the JSON-safe view of provisioning progress.
type InstallSnapshot struct {
	Status string `json:"status"` // idle | installing | installed | failed
	Error  string `json:"error,omitempty"`
}

// InstallState tracks a background Playwright/Chromium provisioning run
// for the headed browser recorder (one at a time per controller).
type InstallState struct {
	mu      sync.Mutex
	running bool
	status  string
	err     string
}

func NewInstallState() *InstallState {
	return &InstallState{status: "idle"}
}

// start kicks off install in the background if not already running, returning a
// snapshot of the (possibly just-updated) state.
func (b *InstallState) Start(install func() error) InstallSnapshot {
	b.mu.Lock()
	if b.running {
		snapshot := InstallSnapshot{Status: b.status, Error: b.err}
		b.mu.Unlock()
		return snapshot
	}
	b.running = true
	b.status = "installing"
	b.err = ""
	b.mu.Unlock()

	go func() {
		err := install()
		b.mu.Lock()
		b.running = false
		if err != nil {
			b.status = "failed"
			b.err = err.Error()
		} else {
			b.status = "installed"
			b.err = ""
		}
		b.mu.Unlock()
	}()
	return InstallSnapshot{Status: "installing"}
}

func (b *InstallState) Snapshot() InstallSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return InstallSnapshot{Status: b.status, Error: b.err}
}
