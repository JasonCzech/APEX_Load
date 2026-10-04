package collectoragent

import (
	"encoding/base64"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// The caller owns this mutable credential header and clears it after writing.
// The helper cannot acquire a network resource or use a process credential.
func solaceBasicAuthorization(credentials ManagementCredentials, identity analysis.ManagementIdentity) ([]byte, error) {
	if identity.Mode != "basic" || !credentials.MatchesIdentity(identity) || credentials.Basic == nil || strings.Contains(credentials.Basic.Username, ":") {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	raw := make([]byte, 0, len(credentials.Basic.Username)+1+len(credentials.Basic.Password))
	raw = append(raw, credentials.Basic.Username...)
	raw = append(raw, ':')
	raw = append(raw, credentials.Basic.Password...)
	defer clear(raw)
	header := make([]byte, len("Basic ")+base64.StdEncoding.EncodedLen(len(raw)))
	copy(header, "Basic ")
	base64.StdEncoding.Encode(header[len("Basic "):], raw)
	if len(header) > 16<<10 {
		clear(header)
		return nil, &managementObservationError{reason: "budget_exhausted"}
	}
	return header, nil
}
