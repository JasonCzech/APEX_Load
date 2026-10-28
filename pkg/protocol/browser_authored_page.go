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

const (
	BrowserAuthoredPageEvidenceKey = "browser_authored_page"
	BrowserAuthoredPageVersion     = 1
	BrowserAuthoredPageVersion2    = 2
	BrowserAuthoredPageVersion3    = 3

	BrowserAuthoredPageAssociationCDPLoaderJoin          = "cdp_loader_join"
	BrowserAuthoredPageAssociationNativeNavigationCommit = "native_navigation_commit"
	BrowserAuthoredPageAssociationHistoryTraversal       = "history_traversal"
	BrowserAuthoredPageAssociationSameDocumentRoute      = "same_document_route"
	BrowserAuthoredPageAssociationOpenedByAction         = "opened_by_action"
	BrowserAuthoredPageAssociationRedirectChain          = "redirect_chain"

	BrowserAuthoredPageUnavailableHistoryTraversal  = "history_traversal_unobserved"
	BrowserAuthoredPageUnavailableSameDocumentRoute = "same_document_route_unobserved"
	BrowserAuthoredPageUnavailablePopupCommit       = "popup_commit_unobserved"
	BrowserAuthoredPageUnavailableRedirectChain     = "redirect_chain_unobserved"
)

// BrowserAuthoredPageBinding is a separate immutable receipt. It joins a
// navigation command's native response to an observed document lifecycle;
// document metrics published before the command returned are never rewritten.
// PageID identifies the original authored navigation definition, not a URL,
// invocation, browser state equivalence or a baseline qualification.
type BrowserAuthoredPageBinding struct {
	NavigationSequence uint64               `json:"navigation_sequence,omitempty"`
	RouteSeq           uint64               `json:"route_seq,omitempty"`
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
	if b == nil || (b.Version != BrowserAuthoredPageVersion && b.Version != BrowserAuthoredPageVersion2 && b.Version != BrowserAuthoredPageVersion3) || b.ObservedAt.IsZero() || b.ObservedAt.Location() != time.UTC || b.ObservedAt.Year() < 1970 || b.ObservedAt.Year() > 9999 {
		return false
	}
	if b.AuthoredStep != nil && (!b.AuthoredStep.Valid() || b.AuthoredStep.Version != BrowserOriginalAuthoredStepVersion) {
		return false
	}
	if b.State == "bound" {
		if b.Reason != "" || b.AuthoredStep == nil || b.PageID != BrowserAuthoredPageID(b.AuthoredStep) || !BrowserEvidenceGeneration(b.SourceGeneration) || !browserEvidenceID(b.DocumentID, b.SourceGeneration, false) || !browserEvidenceID(b.LifecycleID, b.SourceGeneration, false) || b.NavigationSequence > 9007199254740991 || b.RouteSeq > 9007199254740991 {
			return false
		}
		switch b.Version {
		case BrowserAuthoredPageVersion:
			return b.RouteSeq == 0 && b.Association == BrowserAuthoredPageAssociationCDPLoaderJoin && b.NavigationSequence == 0 && browserEvidenceID(b.RequestID, b.SourceGeneration, false)
		case BrowserAuthoredPageVersion2:
			return b.RouteSeq == 0 && b.Association == BrowserAuthoredPageAssociationNativeNavigationCommit && b.NavigationSequence > 0 && browserEvidenceID(b.RequestID, b.SourceGeneration, false)
		case BrowserAuthoredPageVersion3:
			return b.validV3Association()
		default:
			return false
		}
	}
	if b.State != "unavailable" || b.NavigationSequence != 0 || b.RouteSeq != 0 || b.PageID != "" || b.SourceGeneration != "" || b.RequestID != "" || b.DocumentID != "" || b.LifecycleID != "" || b.Association != "" {
		return false
	}
	switch b.Reason {
	case "original_source_unavailable":
		return b.AuthoredStep == nil
	case "navigation_response_unavailable", "navigation_join_unavailable", "document_lifecycle_unavailable", "unsupported_navigation_binding":
		return b.AuthoredStep != nil
	case BrowserAuthoredPageUnavailableHistoryTraversal, BrowserAuthoredPageUnavailableSameDocumentRoute, BrowserAuthoredPageUnavailablePopupCommit, BrowserAuthoredPageUnavailableRedirectChain:
		return b.Version == BrowserAuthoredPageVersion3 && b.AuthoredStep != nil
	}
	return false
}

func (b *BrowserAuthoredPageBinding) validV3Association() bool {
	switch b.Association {
	case BrowserAuthoredPageAssociationRedirectChain:
		return b.NavigationSequence > 0 && b.RouteSeq == 0 && browserEvidenceID(b.RequestID, b.SourceGeneration, false)
	case BrowserAuthoredPageAssociationSameDocumentRoute:
		return b.NavigationSequence > 0 && b.RouteSeq > 0 && b.RequestID == ""
	case BrowserAuthoredPageAssociationHistoryTraversal:
		return b.NavigationSequence > 0 && browserEvidenceID(b.RequestID, b.SourceGeneration, true)
	case BrowserAuthoredPageAssociationOpenedByAction:
		return b.NavigationSequence > 0 && b.RouteSeq == 0 && browserEvidenceID(b.RequestID, b.SourceGeneration, true)
	default:
		return false
	}
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
