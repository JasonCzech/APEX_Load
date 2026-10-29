package engine

import (
	"sync"
	"sync/atomic"
)

// Each publication has a fresh identity, even when the owning VUser returns
// to a previously used configuration pointer. Readers can therefore reject
// evidence projected across an A→B→A policy transition.
type runtimeRedactionPublication struct {
	config *RedactionConfig
}

type runtimeRedactionPublisher struct {
	current atomic.Pointer[runtimeRedactionPublication]
	fence   sync.RWMutex
}

func (p *runtimeRedactionPublisher) Load() *RedactionConfig {
	if publication := p.current.Load(); publication != nil {
		return publication.config
	}
	return nil
}

func (p *runtimeRedactionPublisher) Store(config *RedactionConfig) {
	p.fence.Lock()
	defer p.fence.Unlock()
	p.current.Store(&runtimeRedactionPublication{config: config})
}

func (p *runtimeRedactionPublisher) snapshot() (*RedactionConfig, func(func() bool) bool) {
	publication := p.current.Load()
	var config *RedactionConfig
	if publication != nil {
		config = publication.config
	}
	return config, func(commit func() bool) bool {
		if commit == nil {
			return false
		}
		p.fence.RLock()
		defer p.fence.RUnlock()
		return p.current.Load() == publication && commit()
	}
}
