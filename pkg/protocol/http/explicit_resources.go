package http

import "github.com/jasonczech/apexload/pkg/protocol/httpcontract"

type ExplicitResource = httpcontract.ExplicitResource

func ValidateExplicitResourceTemplates(params map[string]any) error {
	return httpcontract.ValidateExplicitResourceTemplates(params)
}
func ParseExplicitResources(params map[string]any) ([]ExplicitResource, error) {
	return httpcontract.ParseExplicitResources(params)
}
