package collectoragent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/filelock"
)

type collectorSpool struct {
	Version      int                             `json:"version"`
	Pending      map[string]*RunSampleBatch      `json:"pending"`
	OTLPAccepted map[string]map[string]time.Time `json:"otlp_accepted,omitempty"`
}

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
		if fields["pending"] == nil || bytes.Equal(bytes.TrimSpace(fields["pending"]), []byte("null")) {
			return errors.New("versioned collector spool pending map is required")
		}
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
	if len(batches) > 32 {
		return errors.New("collector spool job limit exceeded")
	}
	for id, b := range batches {
		if id == "" || len(id) > 256 || b == nil || b.Sequence < 0 || len(b.Points) > 512 || len(b.Points) == 0 || b.MessagingManagement != nil || b.ManagementGrant != nil {
			return errors.New("invalid collector spool batch")
		}
		for _, p := range b.Points {
			if err := p.Validate(); err != nil {
				return err
			}
		}
	}
	if len(document.OTLPAccepted) > 32 {
		return errors.New("OTLP spool job limit exceeded")
	}
	for id, seen := range document.OTLPAccepted {
		if id == "" || len(id) > 256 || len(seen) > 1024 {
			return errors.New("invalid OTLP acknowledgement spool")
		}
		for key, at := range seen {
			if len(key) != 64 || at.IsZero() {
				return errors.New("invalid OTLP acknowledgement")
			}
		}
	}
	a.otlpAccepted = document.OTLPAccepted
	if batches == nil {
		batches = map[string]*RunSampleBatch{}
	}
	a.pendingBatches = batches
	ids := make([]string, 0, len(batches))
	for id := range batches {
		ids = append(ids, id)
	}
	return a.spoolBudget.restore("legacy", data, ids)
}

func (a *Agent) saveSpool() error {
	ids := make([]string, 0, len(a.pendingBatches))
	for id := range a.pendingBatches {
		ids = append(ids, id)
	}
	data, err := json.Marshal(collectorSpool{Version: 3, Pending: a.pendingBatches, OTLPAccepted: a.otlpAccepted})
	if err != nil {
		return err
	}
	path := ""
	if a.cfg.SpoolDirectory != "" {
		path = a.spoolPath()
	}
	if err := a.spoolBudget.persist("legacy", path, data, ids, func() []string {
		names := []string{}
		for id := range a.pendingBatches {
			names = append(names, "legacy-active:"+id)
		}
		return names
	}()...); err != nil {
		return err
	}
	a.spoolBudget.clearLegacyReservations()
	return nil
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
