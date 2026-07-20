package dsl

import (
	"fmt"
	"strings"
)

func validateGRPCConfigIssues(script *Script) []string {
	if script == nil || strings.TrimSpace(script.Protocol) != "grpc" {
		return nil
	}
	cfg := script.Config.GRPC
	var issues []string
	if cfg.MaxRecvMsgSize < 0 {
		issues = append(issues, "config.grpc.max_recv_msg_size must be zero or greater")
	} else if cfg.MaxRecvMsgSize > MaxGRPCStreamBytes {
		issues = append(issues, fmt.Sprintf("config.grpc.max_recv_msg_size must not exceed %d", MaxGRPCStreamBytes))
	}
	if cfg.TimeoutMs < 0 {
		issues = append(issues, "config.grpc.timeout_ms must be zero or greater")
	} else if cfg.TimeoutMs > MaxConfiguredProtocolTimeoutMS {
		issues = append(issues, fmt.Sprintf("config.grpc.timeout_ms must not exceed %d", MaxConfiguredProtocolTimeoutMS))
	}
	for index, protoFile := range cfg.ProtoFiles {
		if strings.TrimSpace(protoFile) == "" {
			issues = append(issues, fmt.Sprintf("config.grpc.proto_files[%d] must be non-empty", index))
		}
	}
	return issues
}

// legacyActionTypePrefix is the pre-rename prefix for enterprise-source engine
// action types. Scripts persisted before the enterprise-archive rename use it
