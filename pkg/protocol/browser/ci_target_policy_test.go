package browser

import "testing"

func TestCITargetPolicyBlocksNavigationAndSubresources(t *testing.T) {
	browser := &Browser{allowedTargetHosts: []string{"app.example.test", "*.assets.example.test"}}
	if browser.shouldBlockRequestInfo("https://app.example.test/", "document", true) {
		t.Fatal("allowed navigation was blocked")
	}
	if !browser.shouldBlockRequestInfo("https://attacker.example/", "document", true) {
		t.Fatal("disallowed navigation was not blocked")
	}
	if browser.shouldBlockRequestInfo("https://cdn.assets.example.test/app.js", "script", false) {
		t.Fatal("allowed subresource was blocked")
	}
	if !browser.shouldBlockRequestInfo("https://attacker.example/app.js", "script", false) {
		t.Fatal("disallowed subresource was not blocked")
	}
}
