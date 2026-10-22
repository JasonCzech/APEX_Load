package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const BrowserAuthoredPageEvidenceKey = "browser_authored_page"

// BrowserAuthoredPageBinding is a separate immutable receipt. It joins a
// navigation command's native response to an observed document lifecycle;
// document metrics published before the command returned are never rewritten.
// PageID identifies the original authored navigation definition, not a URL,
// invocation, browser state equivalence or a baseline qualification.
type BrowserAuthoredPageBinding struct {
	NavigationSequence uint64               `json:"navigation_sequence,omitempty"`
	Version            int                  `json:"version"`
	State              string               `json:"state"`
	Reason             string               `json:"reason,omitempty"`
	ObservedAt         time.Time            `json:"observed_at"`
	AuthoredStep       *BrowserAuthoredStep `json:"authored_step,omitempty"`
	PageID             string               `json:"page_id,omitempty"`
	SourceGeneration   string               `json:"source_generation,omitempty"`
	RequestID          string               `json:"request_id,omitempty"`
	DocumentID         string               `json:"document_id,omitempty"`
	LifecycleID        string               `json:"lifecycle_id,omitempty"`
	Association        string               `json:"association,omitempty"`
}

func BrowserAuthoredPageID(step *BrowserAuthoredStep) string {
	if !step.Valid() || step.Version != BrowserOriginalAuthoredStepVersion {
		return ""
	}
	sum := sha256.Sum256([]byte("browser_authored_page.v1\x00" + step.StepID))
	return hex.EncodeToString(sum[:])
}

func (b *BrowserAuthoredPageBinding) Valid() bool {
	if b == nil || (b.Version != 1 && b.Version != 2) || b.ObservedAt.IsZero() || b.ObservedAt.Location() != time.UTC || b.ObservedAt.Year() < 1970 || b.ObservedAt.Year() > 9999 {
		return false
	}
	if b.AuthoredStep != nil && (!b.AuthoredStep.Valid() || b.AuthoredStep.Version != BrowserOriginalAuthoredStepVersion) {
		return false
	}
	if b.State == "bound" {
		return b.Reason == "" && b.AuthoredStep != nil && b.PageID == BrowserAuthoredPageID(b.AuthoredStep) && BrowserEvidenceGeneration(b.SourceGeneration) && browserEvidenceID(b.RequestID, b.SourceGeneration, false) && browserEvidenceID(b.DocumentID, b.SourceGeneration, false) && browserEvidenceID(b.LifecycleID, b.SourceGeneration, false) && ((b.Version == 1 && b.Association == "cdp_loader_join" && b.NavigationSequence == 0) || (b.Version == 2 && b.Association == "native_navigation_commit" && b.NavigationSequence > 0 && b.NavigationSequence <= 9007199254740991))
	}
	if b.State != "unavailable" || b.NavigationSequence != 0 || b.PageID != "" || b.SourceGeneration != "" || b.RequestID != "" || b.DocumentID != "" || b.LifecycleID != "" || b.Association != "" {
		return false
	}
	switch b.Reason {
	case "original_source_unavailable":
		return b.AuthoredStep == nil
	case "navigation_response_unavailable", "navigation_join_unavailable", "document_lifecycle_unavailable", "unsupported_navigation_binding":
		return b.AuthoredStep != nil
	}
	return false
}

func (b *BrowserAuthoredPageBinding) Clone() *BrowserAuthoredPageBinding {
	if !b.Valid() {
		return nil
	}
	out := *b
	out.AuthoredStep = b.AuthoredStep.Clone()
	return &out
}

// DecodeBrowserAuthoredPageBinding admits only this bounded versioned receipt.
func DecodeBrowserAuthoredPageBinding(raw []byte) (*BrowserAuthoredPageBinding, error) {
	if len(raw) == 0 || len(raw) > 16<<10 {
		return nil, errors.New("authored page receipt exceeds its bounds")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var result BrowserAuthoredPageBinding
	if err := dec.Decode(&result); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(any)); err != io.EOF || !result.Valid() {
		return nil, errors.New("invalid authored page receipt")
	}
	return &result, nil
}
