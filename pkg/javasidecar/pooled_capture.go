package javasidecar

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"unicode/utf8"
)

var errPooledEventCapture = errors.New("pooled_event_capture_invalid")

// Keep the originally created descriptor. Script-side path replacement must
// never make the runner truncate or disclose a different file.
type pooledSlotCapture struct {
	mu                 sync.Mutex
	file               *os.File
	path               string
	sequence           uint64
	initialized, ended bool
}

func (c *pooledSlotCapture) validFile() bool {
	if c.file == nil {
		return false
	}
	pinned, err := c.file.Stat()
	if err != nil || !pinned.Mode().IsRegular() {
		return false
	}
	current, err := os.Lstat(c.path)
	return err == nil && current.Mode().IsRegular() && os.SameFile(pinned, current)
}

func (c *pooledSlotCapture) reset() error {
	if !c.validFile() {
		return errPooledEventCapture
	}
	if err := c.file.Truncate(0); err != nil {
		return errPooledEventCapture
	}
	return nil
}

func (c *pooledSlotCapture) read(request string) (string, bool, error) {
	if !c.validFile() {
		return "", false, errPooledEventCapture
	}
	data, err := io.ReadAll(io.NewSectionReader(c.file, 0, int64(MaxCaptureBytes)+1))
	if err != nil || !c.validFile() {
		return "", false, errPooledEventCapture
	}
	truncated := len(data) > MaxCaptureBytes
	if truncated {
		data = data[:MaxCaptureBytes]
		// Never return half a JSON record or UTF-8 character at the cap.
		end := bytes.LastIndexByte(data, '\n')
		data = data[:end+1]
	}
	if !validPooledEvents(string(data), request) {
		return "", false, errPooledEventCapture
	}
	return string(data), truncated, nil
}

func validPooledEvents(events, request string) bool {
	if len(events) > MaxCaptureBytes || !utf8.ValidString(events) || (events != "" && events[len(events)-1] != '\n') {
		return false
	}
	var last uint64
	for _, line := range bytes.Split([]byte(events), []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event struct {
			Contract  string `json:"contract"`
			RequestID string `json:"request_id"`
			Sequence  uint64 `json:"sequence"`
			Type      string `json:"type"`
		}
		if json.Unmarshal(line, &event) != nil || event.Contract != harnessEventContract || event.RequestID != request || event.Sequence == 0 || event.Sequence <= last || event.Type == "" {
			return false
		}
		last = event.Sequence
	}
	return true
}

func (p *pooledPreparation) closeCaptures() {
	for _, c := range p.captures {
		c.mu.Lock()
		if c.file != nil {
			_ = c.file.Close()
			c.file = nil
		}
		c.mu.Unlock()
	}
}
