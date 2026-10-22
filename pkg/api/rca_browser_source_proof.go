package api

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Expected owners are frozen by admission, never reconstructed from whichever
// generator bundles happened to arrive. Legacy archives have no such inventory.
func readBrowserSourceProof(ctx context.Context, record *storage.RunArchiveRecord, m *RunArchiveManifest) (*protocol.BrowserSourceReconciler, error) {
	if m == nil || record == nil || m.BrowserReceiptIncomplete || m.BrowserReceiptsFile == "" || len(m.BrowserProofExpectedOwners) == 0 {
		return nil, nil
	}
	if err := validateBrowserReceiptManifest(m, true); err != nil {
		return nil, err
	}
	r := protocol.NewBrowserSourceReconciler(m.TestID, m.BrowserProofExpectedOwners)
	err := forEachBrowserReceiptArtifact(ctx, record.ArchivePath, m.BrowserReceiptsFile, m.BrowserReceiptsSHA256, m.BrowserReceiptRecordCount, r.Observe)
	if err != nil {
		return nil, err
	}
	if !r.Complete() {
		return nil, errors.New("browser source expectations did not reconcile")
	}
	return r, nil
}
