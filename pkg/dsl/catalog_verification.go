package dsl

import (
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func validateVerificationAction(index int, a Action) []string {
	path := fmt.Sprintf("action[%d]", index)
	if hasDurableExecutionBlocker(a.Metadata) {
		return nil
	}
	b, err := json.Marshal(a.Params)
	if err != nil {
		return []string{path + ": invalid verification"}
	}
	var v protocol.Verification
	if err = json.Unmarshal(b, &v); err == nil {
		err = httpprotocol.ValidateVerification(v)
	}
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", path, err)}
	}
	return nil
}
