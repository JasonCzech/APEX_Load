package browser

import "github.com/jasonczech/apexload/pkg/protocol"

func browserErrorSnapshot(result *protocol.Result) *protocol.ErrorSnapshot {
	return &protocol.ErrorSnapshot{
		PageURL:          result.PageURL,
		PageTitle:        result.PageTitle,
		HTML:             result.HTML,
		ScreenshotBase64: result.ScreenshotBase64,
		ConsoleEntries:   cloneConsoleEntries(result.ConsoleEntries),
		NetworkEntries:   cloneNetworkEntries(result.NetworkEntries),
		DOMSummary:       cloneDOMSummary(result.DOMSummary),
	}
}
