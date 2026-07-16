package browser

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

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

func TestCITargetPolicyFailsClosedBeforeBrowserLaunch(t *testing.T) {
	browser := &Browser{}
	err := browser.Init(context.Background(), map[string]any{
		protocol.TargetPolicyConfigKey: []string{"app.example.test"},
		"base_url":                     "https://app.example.test",
	})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("browser CI target policy error = %v", err)
	}
	if browser.pwContext != nil {
		t.Fatal("browser context was launched before rejecting bypassable CI policy")
	}
}
