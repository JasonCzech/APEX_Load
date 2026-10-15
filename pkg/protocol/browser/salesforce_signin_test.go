package browser

import "testing"

func TestSalesforceSignInURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://login.salesforce.com/?locale=us":                                         true,
		"https://test.salesforce.com/":                                                    true,
		"https://acme.my.salesforce.com/":                                                 true,
		"https://acme.my.salesforce.com/?ec=302&startURL=%2Flightning%2Fpage%2Fhome":      true,
		"https://acme.my.salesforce.com/_ui/identity/verification/method/SmsVerification": true,
		"https://acme.my.salesforce.com/lightning/page/home":                              false,
		"https://acme.lightning.force.com/lightning/page/home":                            false,
		"https://example.com/_ui/identity/verification/x":                                 false,
		"": false,
	} {
		if got := SalesforceSignInURL(raw); got != want {
			t.Errorf("SalesforceSignInURL(%q) = %v, want %v", raw, got, want)
		}
	}
}
