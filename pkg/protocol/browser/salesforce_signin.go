package browser

import "github.com/jasonczech/apexload/pkg/protocol/browsercontract"

func SalesforceSignInURL(raw string) bool {
	return browsercontract.SalesforceSignInURL(raw)
}
