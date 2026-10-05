package duckdb

// NativeQuiescenceLease is the exact single-owner metadata hold acquired before
// cancellation/Release or durable physical proof publication. Its physical join
// channel excludes SQL/gate release, so a failed Confirm can retain admission
// while that same physical proof remains available to an owned metadata retry.
// No expiry, caller cancellation, or SQL absence may Resolve this hold.
type NativeQuiescenceLease struct {
	pipeline       *NativePipeline
	physicalDone   chan struct{}
	physicalJoined bool
	resolved       bool
}

func (p *NativePipeline) RetainWriterQuiescence() (*NativeQuiescenceLease, error) {
	if p == nil {
		return nil, errWorkerOwnership
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released || p.quiescenceStarted || p.pendingQuiescence != nil || p.publicationStarted || p.pendingPublication != nil || p.done == nil || p.releaseGate == nil {
		return nil, errWorkerOwnership
	}
	// The production reservation owns a stoppable monitor. Old synthetic or
	// unqualified monitor state cannot be reclassified as physical quiescence.
	if p.monitorDone != nil && !p.monitorStopped && p.monitorStop == nil {
		return nil, errWorkerOwnership
	}
	lease := &NativeQuiescenceLease{pipeline: p, physicalDone: make(chan struct{})}
	p.pendingQuiescence = lease
	p.quiescenceStarted = true
	p.writersSealed = true // Immediately refuse any new child/artifact writer.
	p.tryWriterQuiescenceLocked()
	return lease, nil
}
func (p *NativePipeline) tryWriterQuiescenceLocked() {
	lease := p.pendingQuiescence
	if lease == nil || lease.physicalJoined || len(p.attempts) != 0 || len(p.parentIO) != 0 {
		return
	}
	if p.monitorDone != nil && !p.monitorStopped {
		// Do not stop protection while an existing artifact/child owner is active.
		// Once those owners join, stop only this monitor, preserving the request's
		// logical authority for the following SQL publication on the success path.
		if p.monitorStop != nil {
			p.monitorStopOnce.Do(func() { close(p.monitorStop) })
		}
		return
	}
	lease.physicalJoined = true
	close(lease.physicalDone)
}
func (lease *NativeQuiescenceLease) PhysicalDone() <-chan struct{} {
	if lease == nil {
		return nil
	} // Missing authority never yields a closed proof.
	return lease.physicalDone
}

// Resolve must follow successful durable ConfirmAnalysisAttemptWritersQuiesced
// for the exact captured claim. The analytical package cannot mint that SQL
// authority; its trusted API adapter supplies it after this physical join.
// A premature/stale resolver cannot discard the hold. Successful repeats are
// idempotent and never reopen writer admission.
func (lease *NativeQuiescenceLease) Resolve() error {
	if lease == nil || lease.pipeline == nil {
		return errWorkerOwnership
	}
	p := lease.pipeline
	p.mu.Lock()
	defer p.mu.Unlock()
	if lease.resolved {
		return nil
	}
	if p.pendingQuiescence != lease || !lease.physicalJoined {
		return errWorkerOwnership
	}
	lease.resolved = true
	p.pendingQuiescence = nil
	p.notifyReleaseLocked()
	return nil
}
