package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// BrowserAuthoredStepVersion identifies the immutable script/location identity
// contract. It is independent of runtime action IDs and imported source labels.
const BrowserAuthoredStepVersion = 1

// BrowserAuthoredStep binds a structural DSL location to the exact serialized
// script supplied to the engine, before per-VUser interpolation. A changed
// script intentionally has a different identity. This is not a page, document,
// invocation, policy revision or qualification claim.
type BrowserAuthoredStep struct {
	Version      int    `json:"version"`
	ScriptSHA256 string `json:"script_sha256"`
	StepPath     string `json:"step_path"`
	StepID       string `json:"step_id"`
}

const maxBrowserAuthoredPathBytes = 4096

var browserAuthoredPath = regexp.MustCompile(`^(init|actions|teardown)\[(0|[1-9][0-9]{0,5})\](\.control\.((steps|else|default)\[(0|[1-9][0-9]{0,5})\]|(cases|branches)\[(0|[1-9][0-9]{0,5})\]\.steps\[(0|[1-9][0-9]{0,5})\])){0,64}$`)

// NewBrowserAuthoredStep returns no identity for unbound or noncanonical input.
// Only structural tokens are accepted: names, selectors, URLs and source labels
// never enter StepPath. No key is manufactured for historical evidence.
func NewBrowserAuthoredStep(scriptSHA256, path string) *BrowserAuthoredStep {
	if len(scriptSHA256) != 64 || strings.ToLower(scriptSHA256) != scriptSHA256 || len(path) > maxBrowserAuthoredPathBytes || !browserAuthoredPath.MatchString(path) {
		return nil
	}
	if _, err := hex.DecodeString(scriptSHA256); err != nil {
		return nil
	}
	sum := sha256.Sum256([]byte("browser_authored_step.v1\x00" + scriptSHA256 + "\x00" + path))
	return &BrowserAuthoredStep{Version: BrowserAuthoredStepVersion, ScriptSHA256: scriptSHA256, StepPath: path, StepID: hex.EncodeToString(sum[:])}
}

func (s *BrowserAuthoredStep) Valid() bool {
	if s == nil || s.Version != BrowserAuthoredStepVersion {
		return false
	}
	expected := NewBrowserAuthoredStep(s.ScriptSHA256, s.StepPath)
	return expected != nil && s.StepID == expected.StepID
}

func (s *BrowserAuthoredStep) Clone() *BrowserAuthoredStep {
	if !s.Valid() {
		return nil
	}
	out := *s
	return &out
}
