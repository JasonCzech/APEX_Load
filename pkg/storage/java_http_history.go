package storage

import "github.com/jasonczech/apexload/pkg/protocol"

func nullableJavaHTTPJSON(s *protocol.JavaHTTPActions) any {
	if s == nil {
		return nil
	}
	return protocol.JavaHTTPActionsJSON(s)
}
