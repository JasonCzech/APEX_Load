package storage

import (
	"fmt"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
)

// disclosureSafeImportBatchReceiptScripts builds the immutable evidence copy
// stored with an import receipt. Live script rows remain the exact executable
// import. Ordinary governed imports retain their historical exact receipt
// snapshots; credential-preserving imports use the shared field-aware
// disclosure projection so retries retain every nonsecret script semantic.
func disclosureSafeImportBatchReceiptScripts(scripts []*dsl.Script) ([]*dsl.Script, error) {
	snapshots := make([]*dsl.Script, len(scripts))
	for index, script := range scripts {
		if script == nil {
			return nil, fmt.Errorf("import batch receipt script %d is nil", index+1)
		}
		if !importBatchPreservesExecutableCredentials(script) {
			snapshot := cloneBrunoScript(script)
			if snapshot == nil {
				return nil, fmt.Errorf("clone import batch receipt script %d", index+1)
			}
			snapshots[index] = snapshot
			continue
		}
		snapshot, err := scriptdisclosure.Project(script)
		if err != nil {
			return nil, fmt.Errorf("project import batch receipt script %d: %w", index+1, err)
		}
		if snapshot == nil {
			return nil, fmt.Errorf("project import batch receipt script %d", index+1)
		}
		snapshots[index] = snapshot
	}
	return snapshots, nil
}
