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

	"github.com/jasonczech/apexload/pkg/internal/filelock"
)

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
	if err = json.Unmarshal(data, &batches); err != nil {
		return errors.New("invalid collector spool; refusing to discard unacknowledged evidence")
	}
	if len(batches) > 32 {
		return errors.New("collector spool job limit exceeded")
	}
	for id, b := range batches {
		if id == "" || len(id) > 256 || b == nil || b.Sequence < 0 || len(b.Points) > 512 || len(b.Points) == 0 {
			return errors.New("invalid collector spool batch")
		}
		for _, p := range b.Points {
			if err := p.Validate(); err != nil {
				return err
			}
		}
	}
	a.pendingBatches = batches
	return nil
}

func (a *Agent) saveSpool() error {
	if a.cfg.SpoolDirectory == "" {
		return nil
	}
	if len(a.pendingBatches) > 32 {
		return errors.New("collector spool job limit exceeded")
	}
	data, err := json.Marshal(a.pendingBatches)
	if err != nil {
		return err
	}
	if len(data) > 33<<20 {
		return errors.New("collector spool exceeds limit")
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
