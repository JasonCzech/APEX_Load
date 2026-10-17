package api

import (
	"fmt"
	"net/http"
)

// These messages are adapter contracts. Keep their existing status, wording
// and item numbering independent of transaction/recovery decisions.
type importBatchMessages struct {
	Prepare, Receipt, Draft, Script, Commit string
	IndexedScript                           bool
}

var (
	asyncAPIImportBatchMessages = importBatchMessages{
		Prepare: "failed to prepare AsyncAPI batch: ", Receipt: "AsyncAPI batch receipt ownership was lost; retry the save",
		Draft: "AsyncAPI import draft lease was lost; retry the save", Script: "failed to save AsyncAPI script %d: %v",
		Commit: "failed to commit AsyncAPI batch: ", IndexedScript: true,
	}
	genericImportBatchMessages = importBatchMessages{
		Prepare: "failed to prepare import batch: ", Receipt: "import batch receipt ownership was lost; retry the save",
		Draft: "import draft lease was lost; retry the save", Script: "failed to save imported script %d: %v",
		Commit: "failed to commit import batch: ", IndexedScript: true,
	}
	enterpriseImportBatchMessages = importBatchMessages{
		Prepare: "failed to prepare enterprise archive batch: ", Receipt: "enterprise archive batch receipt ownership was lost; retry the save",
		Draft: "enterprise archive import draft lease was lost; retry the save", Script: "failed to save enterprise archive script %d: %v",
		Commit: "failed to commit enterprise archive batch: ", IndexedScript: true,
	}
	jmeterImportBatchMessages = importBatchMessages{
		Prepare: "failed to prepare imported assets: ", Receipt: "JMeter import receipt ownership was lost; retry the save",
		Draft: "JMeter import draft lease was lost; retry the save", Script: "failed to save imported script: ",
		Commit: "failed to commit imported assets: ",
	}
)

func writeImportBatchFailure(w http.ResponseWriter, failure *importBatchFailure, messages importBatchMessages) {
	status := http.StatusInternalServerError
	var message string
	switch failure.Stage {
	case importBatchReadUpdate:
		message = "failed to read re-import update target: " + failure.Err.Error()
	case importBatchStaleUpdate:
		writeImportUpdateTargetStale(w, failure.Index, failure.Err)
		return
	case importBatchPrepare:
		message = messages.Prepare + failure.Err.Error()
	case importBatchReceipt:
		status, message = http.StatusConflict, messages.Receipt
	case importBatchDraft:
		status, message = http.StatusConflict, messages.Draft
	case importBatchScript:
		if messages.IndexedScript {
			message = fmt.Sprintf(messages.Script, failure.Index+1, failure.Err)
		} else {
			message = messages.Script + failure.Err.Error()
		}
	case importBatchScenario:
		status, message = http.StatusBadRequest, "failed to save scenario draft: "+failure.Err.Error()
	case importBatchCommitStage:
		message = messages.Commit + failure.Err.Error()
	default:
		message = "failed to commit imported assets"
	}
	writeError(w, status, message)
}
