package protocol

import (
	"fmt"
	"sync"
)

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Factory)

	runtimeNamespaceMu       sync.RWMutex
	runtimeNamespaceManagers = make(map[string]runtimeNamespaceManager)
)

type Factory func() Protocol

type runtimeNamespaceManager struct {
	acquire func(string)
	release func(string)
}

func Register(name string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("protocol already registered: %s", name))
	}
	registry[name] = factory
}

func Get(name string) (Protocol, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown protocol: %s", name)
	}
	return factory(), nil
}

func List() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// RegisterRuntimeNamespaceManager registers a protocol package's lifecycle
// hooks for engine-generated run namespaces. The engine holds one namespace
// lease for the entire run so short-lived protocol instances (for example an
// init-once block) cannot clean up shared mock state before VUsers start.
func RegisterRuntimeNamespaceManager(name string, acquire, release func(string)) {
	if name == "" || acquire == nil || release == nil {
		panic("protocol runtime namespace manager requires a name and lifecycle hooks")
	}
	runtimeNamespaceMu.Lock()
	defer runtimeNamespaceMu.Unlock()
	if _, exists := runtimeNamespaceManagers[name]; exists {
		panic(fmt.Sprintf("protocol runtime namespace manager already registered: %s", name))
	}
	runtimeNamespaceManagers[name] = runtimeNamespaceManager{acquire: acquire, release: release}
}

// AcquireRuntimeNamespace holds all registered protocol namespace managers for
// the duration of one engine run. The returned release function is idempotent.
func AcquireRuntimeNamespace(namespace string) func() {
	if namespace == "" {
		return func() {}
	}
	runtimeNamespaceMu.RLock()
	managers := make([]runtimeNamespaceManager, 0, len(runtimeNamespaceManagers))
	for _, manager := range runtimeNamespaceManagers {
		managers = append(managers, manager)
	}
	runtimeNamespaceMu.RUnlock()
	for _, manager := range managers {
		manager.acquire(namespace)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for i := len(managers) - 1; i >= 0; i-- {
				managers[i].release(namespace)
			}
		})
	}
}
