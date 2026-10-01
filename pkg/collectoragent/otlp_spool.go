package collectoragent

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// The owning agent holds its process spool lock before opening this journal.
// HTTP acknowledgement follows atomic replacement and fsync, not memory enqueue.
func (o *OTLPReceiver) openJournal(path string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	recovered := map[string][]analysis.MetricPoint{}
	file, err := os.Open(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		defer file.Close()
		body, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
		if err != nil || len(body) > 32<<20 {
			return errors.New("OTLP journal exceeds limit")
		}
		var points map[string][]analysis.MetricPoint
		if json.Unmarshal(body, &points) != nil || len(points) > 16 {
			return errors.New("invalid OTLP journal")
		}
		for stream, samples := range points {
			if len(samples) > 512 {
				return errors.New("invalid OTLP journal sample count")
			}
			for _, point := range samples {
				if point.Validate() != nil {
					return errors.New("invalid OTLP journal point")
				}
			}
			if o.tokens[stream] != "" {
				for _, point := range samples {
					if point.TimestampUTC.After(time.Now().Add(-5 * time.Minute)) {
						recovered[stream] = append(recovered[stream], point)
					}
				}
			}
		}
	}
	o.points = recovered
	o.journalPath = path
	o.journalReady = true
	return nil
}

func (o *OTLPReceiver) closeJournal() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.journalReady = false
}

func (o *OTLPReceiver) persistJournal() error {
	if !o.requireJournal {
		return nil
	}
	if !o.journalReady {
		return errors.New("OTLP journal not ready")
	}
	data, err := json.Marshal(o.points)
	if err != nil || len(data) > 32<<20 {
		return errors.New("OTLP journal exceeds limit")
	}
	if err := os.MkdirAll(filepath.Dir(o.journalPath), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(o.journalPath), "otlp-*.tmp")
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
	if err = os.Rename(file.Name(), o.journalPath); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(o.journalPath))
	if err != nil {
		return err
	}
	defer directory.Close()
	err = directory.Sync()
	if runtime.GOOS == "windows" && err != nil && strings.Contains(strings.ToLower(err.Error()), "incorrect function") {
		return nil
	}
	return err
}
