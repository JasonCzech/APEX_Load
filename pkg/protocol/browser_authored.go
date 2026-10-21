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
const BrowserOriginalAuthoredStepVersion = 2

// BrowserAuthoredStep binds a structural DSL location to script provenance.
// Version 1 identifies the exact engine input before VUser interpolation;
// version 2 identifies original source bound by a trusted host before runtime
// overrides. This is not a page, runtime equivalence, invocation, policy
// revision or qualification claim.
type BrowserAuthoredStep struct {
	SourceStructureSHA256 string `json:"source_structure_sha256,omitempty"`
	Version               int    `json:"version"`
	ScriptSHA256          string `json:"script_sha256"`
	StepPath              string `json:"step_path"`
	StepID                string `json:"step_id"`
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

// BrowserAuthoredDefinition is trusted source provenance, separate from the
// executable runtime script. Callers bind it before runtime overrides. A valid
// descriptor identifies authored content; it does not prove runtime equivalence.
type BrowserAuthoredDefinition struct {
	Version         int    `json:"version"`
	ScriptSHA256    string `json:"script_sha256"`
	StructureSHA256 string `json:"structure_sha256"`
}

func browserAuthoredDigestValid(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func (d BrowserAuthoredDefinition) Valid() bool {
	return d.Version == 1 && browserAuthoredDigestValid(d.ScriptSHA256) && browserAuthoredDigestValid(d.StructureSHA256)
}

// NewBrowserOriginalAuthoredStep binds a location to the original source
// descriptor. Version 1 stays the legacy exact-engine-input identity.
func NewBrowserOriginalAuthoredStep(definition BrowserAuthoredDefinition, path string) *BrowserAuthoredStep {
	if !definition.Valid() || NewBrowserAuthoredStep(definition.ScriptSHA256, path) == nil {
		return nil
	}
	sum := sha256.Sum256([]byte("browser_authored_step.v2\x00" + definition.ScriptSHA256 + "\x00" + definition.StructureSHA256 + "\x00" + path))
	return &BrowserAuthoredStep{Version: BrowserOriginalAuthoredStepVersion, ScriptSHA256: definition.ScriptSHA256, SourceStructureSHA256: definition.StructureSHA256, StepPath: path, StepID: hex.EncodeToString(sum[:])}
}

func (s *BrowserAuthoredStep) Valid() bool {
	if s == nil {
		return false
	}
	var expected *BrowserAuthoredStep
	switch s.Version {
	case BrowserAuthoredStepVersion:
		if s.SourceStructureSHA256 != "" {
			return false
		}
		expected = NewBrowserAuthoredStep(s.ScriptSHA256, s.StepPath)
	case BrowserOriginalAuthoredStepVersion:
		expected = NewBrowserOriginalAuthoredStep(BrowserAuthoredDefinition{Version: 1, ScriptSHA256: s.ScriptSHA256, StructureSHA256: s.SourceStructureSHA256}, s.StepPath)
	default:
		return false
	}
	return expected != nil && s.StepID == expected.StepID
}

func (s *BrowserAuthoredStep) Clone() *BrowserAuthoredStep {
	if !s.Valid() {
		return nil
	}
	out := *s
	return &out
}
