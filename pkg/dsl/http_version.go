package dsl

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// OptionalHTTPVersion is the shared presence-aware leaf policy scalar.
type OptionalHTTPVersion = protocol.OptionalHTTPVersion

func NewHTTPVersionOverride(value string) (OptionalHTTPVersion, error) {
	return protocol.NewHTTPVersionOverride(value)
}

func EffectiveHTTPVersion(action Action, inherited string) (string, error) {
	return action.HTTPVersion.Resolve(httpprotocol.NormalizeVersion(inherited))
}

// InheritedHTTPVersion applies a nonempty explicit config before a registered
// protocol alias. The legacy config string represents both empty and omitted.
func InheritedHTTPVersion(script *Script) string {
	if script == nil {
		return ""
	}
	if script.Config.HTTP.Version != "" {
		return httpprotocol.NormalizeVersion(script.Config.HTTP.Version)
	}
	switch script.Protocol {
	case "http/1.1":
		return "1.1"
	case "http/2":
		return "2"
	}
	return ""
}
