package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/contentcontract"
)

func StripProtectedImportSourceCommitFence(script *dsl.Script) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return
	}
	delete(script.Metadata.Import.Preserved, contentcontract.ImportSourceCommitFenceMetadataKey)
}
