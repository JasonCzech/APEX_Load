package engine

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

const runtimeMockNamespaceConfigKey = "runtime_mock_namespace"

var runtimeMockNamespaceCounter atomic.Uint64

func newRuntimeMockNamespace() string {
	return fmt.Sprintf("run-%d-%d-%d", os.Getpid(), time.Now().UnixNano(), runtimeMockNamespaceCounter.Add(1))
}

func applyRuntimeMockNamespace(config map[string]any, namespace string) {
	if config != nil && namespace != "" {
		config[runtimeMockNamespaceConfigKey] = namespace
	}
}
