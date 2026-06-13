package api

import "sync"

// browserInstallSnapshot is the JSON-safe view of provisioning progress.
type browserInstallSnapshot struct {
	Status string `json:"status"` // idle | installing | installed | failed
	Error  string `json:"error,omitempty"`
}

// browserInstallState tracks a background Playwright/Chromium provisioning run
// for the headed browser recorder (one at a time per controller).
type browserInstallState struct {
	mu      sync.Mutex
	running bool
	status  string
	err     string
}

func newBrowserInstallState() *browserInstallState {
	return &browserInstallState{status: "idle"}
}

// start kicks off install in the background if not already running, returning a
// snapshot of the (possibly just-updated) state.
func (b *browserInstallState) start(install func() error) browserInstallSnapshot {
	b.mu.Lock()
	if b.running {
		snapshot := browserInstallSnapshot{Status: b.status, Error: b.err}
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
	return browserInstallSnapshot{Status: "installing"}
}

func (b *browserInstallState) snapshot() browserInstallSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return browserInstallSnapshot{Status: b.status, Error: b.err}
}
