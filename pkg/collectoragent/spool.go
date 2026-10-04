package collectoragent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/filelock"
)

type collectorSpool struct {
	ProviderBacklog map[string][]*RunSampleBatch    `json:"provider_backlog,omitempty"`
	Version         int                             `json:"version"`
	Pending         map[string]*RunSampleBatch      `json:"pending"`
	OTLPAccepted    map[string]map[string]time.Time `json:"otlp_accepted,omitempty"`
}

// The job count is a local retry-storage bound across runs. The existing
// decompressed byte budget remains unchanged; 32 sources is a per-run limit.
const maxCollectorSpoolJobs = 256
const maxRunMonitorJobIDBytes = 513

func (a *Agent) spoolPath() string {
	hash := sha256.Sum256([]byte(a.cfg.ControllerURL + "\x00" + a.cfg.CollectorID))
	return filepath.Join(a.cfg.SpoolDirectory, hex.EncodeToString(hash[:])+".json")
}

func (a *Agent) loadSpool() error {
	if a.cfg.SpoolDirectory == "" {
		return nil
	}
	file, err := os.Open(a.spoolPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 34<<20))
	if err != nil {
		return err
	}
	if len(data) > 33<<20 {
		return errors.New("collector spool exceeds limit")
	}
	var batches map[string]*RunSampleBatch
	var document collectorSpool
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err == nil && fields["version"] != nil {
		err = json.Unmarshal(data, &document)
		if err == nil && document.Version != 2 && document.Version != 3 {
			err = errors.New("unsupported spool version")
		}
		batches = document.Pending
	} else if err == nil {
		err = json.Unmarshal(data, &batches)
	}
	if err != nil {
		return errors.New("invalid collector spool; refusing to discard unacknowledged evidence")
	}
	if len(batches) > maxCollectorSpoolJobs {
		return errors.New("collector spool job limit exceeded")
	}
	for id, b := range batches {
		if id == "" || len(id) > maxRunMonitorJobIDBytes || b == nil || b.Sequence < 0 || len(b.Points) > 512 || len(b.Evidence) > 100 || len(b.Points)+len(b.Evidence) == 0 && len(b.Coverage) == 0 {
			return errors.New("invalid collector spool batch")
		}
		for _, p := range b.Points {
			if err := p.Validate(); err != nil {
				return err
			}
		}
	}
	if len(document.OTLPAccepted) > maxCollectorSpoolJobs {
		return errors.New("OTLP spool job limit exceeded")
	}
	for id, seen := range document.OTLPAccepted {
		if id == "" || len(id) > maxRunMonitorJobIDBytes || len(seen) > 1024 {
			return errors.New("invalid OTLP acknowledgement spool")
		}
		for key, at := range seen {
			if len(key) != 64 || at.IsZero() {
				return errors.New("invalid OTLP acknowledgement")
			}
		}
	}
	if len(document.ProviderBacklog) > maxCollectorSpoolJobs {
		return errors.New("provider spool job limit exceeded")
	}
	for id, queue := range document.ProviderBacklog {
		if id == "" || len(id) > maxRunMonitorJobIDBytes || len(queue) > 32 {
			return errors.New("provider spool backlog exceeds bounds")
		}
		for _, batch := range queue {
			if batch == nil || len(batch.Points) > 512 || len(batch.Evidence) > 100 {
				return errors.New("invalid provider backlog")
			}
			for _, point := range batch.Points {
				if err := point.Validate(); err != nil {
					return err
				}
			}
		}
	}
	a.providerBacklog = document.ProviderBacklog
	a.otlpAccepted = document.OTLPAccepted
	if batches == nil {
		batches = map[string]*RunSampleBatch{}
	}
	a.pendingBatches = batches
	return nil
}

func (a *Agent) saveSpool() error {
	for id, queue := range a.providerBacklog {
		if len(queue) == 0 {
			delete(a.providerBacklog, id)
		}
	}
	if len(a.pendingBatches) > maxCollectorSpoolJobs || len(a.providerBacklog) > maxCollectorSpoolJobs || len(a.otlpAccepted) > maxCollectorSpoolJobs {
		return errors.New("collector spool job limit exceeded")
	}
	data, err := json.Marshal(collectorSpool{Version: 3, ProviderBacklog: a.providerBacklog, Pending: a.pendingBatches, OTLPAccepted: a.otlpAccepted})
	if err != nil {
		return err
	}
	if len(data) > 33<<20 {
		return errors.New("collector spool exceeds limit")
	}
	if a.cfg.SpoolDirectory == "" {
		return nil
	}
	if err = os.MkdirAll(a.cfg.SpoolDirectory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(a.cfg.SpoolDirectory, "pending-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), a.spoolPath()); err != nil {
		return err
	}
	dir, err := os.Open(a.cfg.SpoolDirectory)
	if err != nil {
		return err
	}
	defer dir.Close()
	err = dir.Sync()
	// Windows directory handles do not implement FlushFileBuffers.
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "incorrect function") {
		return nil
	}
	return err
}

// Caller holds stateMu. Admission is bounded even without disk persistence;
// a failed write or quota check restores every already queued retry.
func (a *Agent) admitMonitorRetry(id string, batch *RunSampleBatch, queue []*RunSampleBatch) error {
	previous, hadPrevious := a.pendingBatches[id]
	previousQueue, hadQueue := a.providerBacklog[id]
	a.pendingBatches[id] = batch
	if a.providerBacklog == nil {
		a.providerBacklog = map[string][]*RunSampleBatch{}
	}
	if len(queue) > 0 {
		a.providerBacklog[id] = queue
	} else {
		delete(a.providerBacklog, id)
	}
	if err := a.saveSpool(); err != nil {
		if hadPrevious {
			a.pendingBatches[id] = previous
		} else {
			delete(a.pendingBatches, id)
		}
		if hadQueue {
			a.providerBacklog[id] = previousQueue
		} else {
			delete(a.providerBacklog, id)
		}
		return err
	}
	return nil
}

// Expired job evidence must not reappear merely because a later paginated
// scan reaches that job ID. Collection leases may be renewed, but the signed
// job lifetime is immutable and every upload still needs a current lease.
func (a *Agent) discardExpiredMonitorSpool(now time.Time) {
	for id, batch := range a.pendingBatches {
		if !a.spoolJobAlive(id, batch, now) {
			delete(a.pendingBatches, id)
			delete(a.providerBacklog, id)
			delete(a.otlpAccepted, id)
			delete(a.nextCollection, id)
			delete(a.jobScanUnseen, id)
		}
	}
	for id, queue := range a.providerBacklog {
		if len(queue) == 0 {
			delete(a.providerBacklog, id)
			continue
		}
		if !a.spoolJobAlive(id, queue[0], now) {
			delete(a.providerBacklog, id)
			delete(a.otlpAccepted, id)
			delete(a.nextCollection, id)
			delete(a.jobScanUnseen, id)
		}
	}
}

func (a *Agent) spoolJobAlive(id string, batch *RunSampleBatch, now time.Time) bool {
	if batch == nil {
		return false
	}
	var stored RunJob
	if json.Unmarshal([]byte(batch.Job.Payload), &stored) != nil || stored.ID != id || !stored.ExpiresAt.After(now) || stored.LeaseExpiresAt.IsZero() {
		return false
	}
	// Validate the immutable signature and identity at the lease's original
	// endpoint; an expired collection lease alone does not erase observations.
	_, err := VerifyRunJob(batch.Job, a.cfg.ControllerPublicKey, a.cfg.CollectorID, stored.LeaseExpiresAt.Add(-time.Nanosecond))
	return err == nil
}

func (a *Agent) lockSpool(ctx context.Context) (func(), error) {
	if a.cfg.SpoolDirectory == "" {
		return func() {}, nil
	}
	if err := os.MkdirAll(a.cfg.SpoolDirectory, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(a.spoolPath()+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	release, err := filelock.LockContext(ctx, file)
	if err != nil {
		file.Close()
		return nil, err
	}
	return func() { _ = release(); _ = file.Close() }, nil
}
