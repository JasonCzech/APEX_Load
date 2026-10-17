package http

import "github.com/jasonczech/apexload/pkg/protocol/httpcontract"

type AltSvcEntry = httpcontract.AltSvcEntry
type AltSvc = httpcontract.AltSvc

const DefaultAltSvcMaxAge = httpcontract.DefaultAltSvcMaxAge

func AltSvcAdvertisesH3(values []string) bool {
	return httpcontract.AltSvcAdvertisesH3(values)
}
func ParseAltSvc(values []string) AltSvc {
	return httpcontract.ParseAltSvc(values)
}
