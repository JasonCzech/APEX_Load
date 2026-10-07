package browser

import (
	"context"
	"errors"
	"github.com/mxschmitt/playwright-go"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func (b *Browser) close() error { return b.closeWithContext(context.Background()) }

func (b *Browser) closeWithContext(ctx context.Context) error {
	var closeErr error
	b.censorDocumentEvidence(nil, "context_closed")
	b.mu.Lock()
	b.closing = true
	b.retireNetworkWorkersLocked()
	requestPolicyStop := b.requestPolicyStop
	b.requestPolicyStop = nil
	cdp := b.pwCDP
	b.pwCDP = nil
	b.pwCDPPage = nil
	b.cdpInstallGeneration++
	owner := b.harOwner
	page := b.pwPage
	b.pwPage = nil
	b.mu.Unlock()
	if requestPolicyStop != nil {
		close(requestPolicyStop)
	}
	if cdp != nil && owner == nil {
		_ = cdp.Detach()
	}
	if owner != nil {
		b.mu.Lock()
		monitorStop := b.harMonitorStop
		b.harMonitorStop = nil
		workerStop := b.networkWorkerStop
		b.networkWorkerStop = nil
		b.networkTasks = nil
		b.mu.Unlock()
		if workerStop != nil {
			close(workerStop)
		}
		owner.startRuntimeJoin(func() {
			if cdp != nil {
				owner.recordRuntimeError(cdp.Detach())
			}
			<-owner.closeDone
			if monitorStop != nil {
				close(monitorStop)
			}
			b.harMonitorWG.Wait()
			b.networkWorkerWG.Wait()
			b.mu.Lock()
			if b.harOwner == owner {
				b.harLimitExceeded = nil
			}
			b.mu.Unlock()
		})
		owner.startClose()
	} else {
		if page != nil {
			closeErr = errors.Join(closeErr, page.Close())
		}
		if browserContext := b.takeBrowserContext(); browserContext != nil {
			closeErr = errors.Join(closeErr, browserContext.Close())
		}
	}
	if owner == nil {
		// Keep the limiter active through Playwright's final HAR flush.
		b.mu.Lock()
		monitorStop := b.harMonitorStop
		b.harMonitorStop = nil
		b.mu.Unlock()
		if monitorStop != nil {
			close(monitorStop)
			b.harMonitorWG.Wait()
			b.mu.Lock()
			b.harLimitExceeded = nil
			b.mu.Unlock()
		}
		b.mu.Lock()
		workerStop := b.networkWorkerStop
		b.networkWorkerStop = nil
		b.networkTasks = nil
		b.mu.Unlock()
		if workerStop != nil {
			close(workerStop)
			b.networkWorkerWG.Wait()
		}
	}
	artifactPath, harErr := b.finalizeHARCaptureContext(ctx)
	closeErr = errors.Join(closeErr, harErr)
	browserEvidenceBudget.retireHARWriter(b, owner)
	browserEvidenceBudget.releaseBrowserContext(b)
	if owner != nil && !owner.retiredSuccessfully() {
		return errors.Join(closeErr, errHAROwnershipUnproven)
	}
	if artifactPath != "" {
		// Retained artifacts are independently swept by the configured retention
		// policy. In particular, do not delete an earlier failing iteration when
		// new_user_per_iteration tears down a later browser session.
		b.lastHARArtifactPath = artifactPath
		b.mu.Lock()
		if len(b.publishedHARArtifacts) < protocol.MaxBrowserHARArtifactsPerRun {
			b.publishedHARArtifacts = append(b.publishedHARArtifacts, protocol.BrowserHARArtifact{Path: artifactPath, Generation: owner.generation})
		}
		b.mu.Unlock()
	}
	b.mu.Lock()
	b.runtimeTextRedactor = nil
	b.runtimeSecretsActive = nil
	b.mu.Unlock()
	b.storageState = nil
	b.currentURL = ""
	b.slowMo = 0
	b.userAgent = ""
	b.captureScreenshots = ""
	b.consoleEntries = nil
	b.consoleDropped = 0
	b.networkEntries = nil
	b.networkEntriesDropped = 0
	b.networkBodyBytes = 0
	b.networkMetadataBytes = 0
	b.pages = nil
	b.resetWSEvents()
	if owner != nil {
		b.mu.Lock()
		if b.harOwner == nil {
			b.pwContext = nil
		}
		b.mu.Unlock()
	}
	return closeErr
}

func (b *Browser) stopRequestPolicyWaiters() {
	b.mu.Lock()
	stop := b.requestPolicyStop
	b.requestPolicyStop = nil
	b.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (b *Browser) takeBrowserContext() playwright.BrowserContext {
	b.mu.Lock()
	defer b.mu.Unlock()
	context := b.pwContext
	b.pwContext = nil
	return context
}
