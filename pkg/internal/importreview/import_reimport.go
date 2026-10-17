package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

func StampFileImportSourceIdentity(fileName string, scripts []*dsl.Script) {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return
	}
	for _, script := range scripts {
		if script == nil {
			continue
		}
		var metadata *dsl.ImportMetadata
		if script.Metadata != nil {
			metadata = script.Metadata.Import
		}
		if _, _, _, ok := dsl.ImportSourceIdentityOf(metadata); ok {
			continue
		}
		item := ""
		if len(scripts) > 1 {
			item = script.Name
		}
		dsl.SetImportSourceIdentity(script, fileName, false, item)
	}
}

func WithoutImportedContentDigest(metadata *dsl.ScriptMetadata) *dsl.ScriptMetadata {
	if metadata == nil || metadata.Import == nil || metadata.Import.Preserved == nil {
		return metadata
	}
	if _, stamped := metadata.Import.Preserved[dsl.ImportedActionDigestKey]; !stamped {
		return metadata
	}
	copied := *metadata
	importCopy := *metadata.Import
	importCopy.Preserved = make(map[string]any, len(metadata.Import.Preserved))
	for key, value := range metadata.Import.Preserved {
		if key != dsl.ImportedActionDigestKey {
			importCopy.Preserved[key] = value
		}
	}
	copied.Import = &importCopy
	return &copied
}
