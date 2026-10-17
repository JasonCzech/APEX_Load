package httpcontract

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func ValidateVerification(v protocol.Verification) error {
	if v.ID == "" || len(v.ID) > 256 {
		return fmt.Errorf("verification ID must contain 1..256 bytes")
	}
	switch v.Operation {
	case "pause", "resume":
		return nil
	case "register":
	default:
		return fmt.Errorf("invalid verification operation")
	}
	if v.Text == "" || len(v.Text) > 65536 {
		return fmt.Errorf("verification text must contain 1..65536 bytes")
	}
	switch strings.ToLower(v.Scope) {
	case "all", "body", "headers", "noresource", "resource":
	default:
		return fmt.Errorf("invalid verification scope")
	}
	return nil
}
