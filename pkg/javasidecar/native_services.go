package javasidecar

import (
	"context"
	"encoding/json"
	"strings"
)

func harnessHealthFeature(stdout, feature string) bool {
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), harnessSessionPrefix) {
			continue
		}
		var envelope harnessHealthEnvelope
		if json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), harnessSessionPrefix)), &envelope) != nil || envelope.Contract != harnessIPCContract || envelope.Kind != "capabilities" || envelope.EventContract != harnessEventContract {
			continue
		}
		for _, f := range envelope.Features {
			if f == feature {
				return true
			}
		}
	}
	return false
}
func (s *Server) verifyNativeHarness(ctx context.Context, runtime RuntimeSettings) *safeFailure {
	prepared, failure := s.prepareRuntime(ctx, runtime)
	if failure != nil {
		return failure
	}
	if failure = s.verifyHarness(ctx, prepared); failure != nil {
		return failure
	}
	s.harnessHealthMu.Lock()
	available := s.harnessNative[harnessHealthCacheKey(prepared)]
	s.harnessHealthMu.Unlock()
	if !available {
		return &safeFailure{code: "native_data_sidecar_unavailable"}
	}
	return nil
}
