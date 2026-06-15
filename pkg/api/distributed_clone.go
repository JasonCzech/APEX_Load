package api

// distributed_clone.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import "github.com/jasonczech/apexload/pkg/distributed/generatorrpc"

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneClientMap(values map[string]generatorClient) map[string]generatorClient {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]generatorClient, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneMetricsStreamMap(values map[string]*generatorrpc.MetricsStream) map[string]*generatorrpc.MetricsStream {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]*generatorrpc.MetricsStream, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
