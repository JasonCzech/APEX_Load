package collectoragent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxCollectorSpoolBytes = 33 << 20

// Both durable documents and in-flight polls share one capacity authority.
// Maps in the legacy heartbeat path never need to be read by async workers.
type collectorSpoolBudget struct {
	mu          sync.Mutex
	fileBytes   map[string]int
	fileJobs    map[string][]string
	activeJobs  map[string][]string
	activeBytes map[string]int
	write       func(string, []byte) (bool, error)
}

func newCollectorSpoolBudget() *collectorSpoolBudget {
	return &collectorSpoolBudget{fileBytes: map[string]int{}, fileJobs: map[string][]string{}, activeJobs: map[string][]string{}, activeBytes: map[string]int{}, write: writeCollectorSpool}
}
func (b *collectorSpoolBudget) jobsFit(name string, jobs []string, active bool) bool {
	seen := map[string]bool{}
	for key, list := range b.fileJobs {
		if !active && key == name {
			continue
		}
		for _, id := range list {
			seen[id] = true
		}
	}
	for key, list := range b.activeJobs {
		if active && key == name {
			continue
		}
		for _, id := range list {
			seen[id] = true
		}
	}
	for _, id := range jobs {
		seen[id] = true
	}
	return len(seen) <= 32
}
func (b *collectorSpoolBudget) reserve(name string, jobs []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.jobsFit(name, jobs, true) {
		return errors.New("combined collector pending job limit exceeded")
	}
	b.activeJobs[name] = append([]string(nil), jobs...)
	return nil
}
func (b *collectorSpoolBudget) release(name string) {
	b.mu.Lock()
	delete(b.activeJobs, name)
	delete(b.activeBytes, name)
	b.mu.Unlock()
}
func (b *collectorSpoolBudget) restore(name string, data []byte, jobs []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.canWrite(name, len(data), jobs) {
		return errors.New("combined collector spool limits exceeded")
	}
	b.fileBytes[name] = len(data)
	b.fileJobs[name] = append([]string(nil), jobs...)
	return nil
}
func (b *collectorSpoolBudget) canWrite(name string, size int, jobs []string, absorb ...string) bool {
	total := size
	for key, n := range b.fileBytes {
		if key != name {
			total += n
		}
	}
	for key, n := range b.activeBytes {
		skip := false
		for _, a := range absorb {
			skip = skip || a == key
		}
		if !skip {
			total += n
		}
	}
	return total <= maxCollectorSpoolBytes && b.jobsFit(name, jobs, false)
}
func (b *collectorSpoolBudget) persist(name, path string, data []byte, jobs []string, absorb ...string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.canWrite(name, len(data), jobs, absorb...) {
		return errors.New("combined collector spool limits exceeded")
	}
	installed := true
	var err error
	if path != "" {
		installed, err = b.write(path, data)
	}
	// Rename may have installed a new document even if directory fsync failed.
	// Physical accounting must follow that installed document on every outcome.
	if installed {
		b.fileBytes[name] = len(data)
		b.fileJobs[name] = append([]string(nil), jobs...)
		for _, key := range absorb {
			delete(b.activeBytes, key)
		}
	}
	return err
}
func (b *collectorSpoolBudget) reserveFresh(name string, jobs []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	const bound = (1 << 20) + (4 << 10)
	total := bound
	for _, n := range b.fileBytes {
		total += n
	}
	for key, n := range b.activeBytes {
		if key != name {
			total += n
		}
	}
	if !b.jobsFit(name, jobs, true) || total > maxCollectorSpoolBytes {
		return errors.New("combined collector fresh evidence capacity exceeded")
	}
	b.activeJobs[name] = append([]string(nil), jobs...)
	b.activeBytes[name] = bound
	return nil
}

// A completed in-memory poll retains its reserved write capacity even if
// persistence fails and its actual observer task can safely finish.
func (b *collectorSpoolBudget) transferFresh(from, to string, jobs []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	bytes, ok := b.activeBytes[from]
	if !ok || !b.jobsFit(to, jobs, true) {
		return errors.New("collector completed evidence reservation unavailable")
	}
	b.activeJobs[to] = append([]string(nil), jobs...)
	b.activeBytes[to] = bytes
	delete(b.activeBytes, from)
	return nil
}
func (b *collectorSpoolBudget) clearLegacyReservations() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for key := range b.activeJobs {
		if strings.HasPrefix(key, "legacy-active:") {
			delete(b.activeJobs, key)
			delete(b.activeBytes, key)
		}
	}
}
func writeCollectorSpool(path string, data []byte) (bool, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return false, err
	}
	file, err := os.CreateTemp(directory, "pending-*.tmp")
	if err != nil {
		return false, err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return false, err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return false, err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return true, err
	}
	defer dir.Close()
	err = dir.Sync()
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "incorrect function") {
		return true, nil
	}
	return true, err
}
