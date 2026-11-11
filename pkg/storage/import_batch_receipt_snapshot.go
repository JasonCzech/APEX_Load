package storage

import (
	"fmt"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// disclosureSafeImportBatchReceiptScripts builds the immutable snapshot copy
// stored with an import receipt, retaining exact unredacted script semantics.
func disclosureSafeImportBatchReceiptScripts(scripts []*dsl.Script) ([]*dsl.Script, error) {
	snapshots := make([]*dsl.Script, len(scripts))
	for index, script := range scripts {
		if script == nil {
			return nil, fmt.Errorf("import batch receipt script %d is nil", index+1)
		}
		snapshot := cloneBrunoScript(script)
		if snapshot == nil {
			return nil, fmt.Errorf("clone import batch receipt script %d", index+1)
		}
		snapshots[index] = snapshot
	}
	return snapshots, nil
}

// ImportBatchReceiptSnapshot returns an independent snapshot clone of script as
// stored in an import batch receipt.
func ImportBatchReceiptSnapshot(script *dsl.Script) (*dsl.Script, error) {
	snapshots, err := disclosureSafeImportBatchReceiptScripts([]*dsl.Script{script})
	if err != nil {
		return nil, err
	}
	return snapshots[0], nil
}
