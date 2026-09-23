package browser

import (
	"context"
	"testing"
)

func TestBrowserAvailableScreenAndSystemLanguage(t *testing.T) {
	b := &Browser{}
	err := b.Init(context.Background(), map[string]any{"browser_headless": true, "browser_system_language": "en-US", "browser_available_screen_width": 1920, "browser_available_screen_height": 1008, "browser_viewport_width": 800, "browser_viewport_height": 600})
	requireBrowserRuntime(t, "", err)
	defer b.Teardown(context.Background())
	value, err := b.currentPage().Evaluate(`() => [navigator.systemLanguage,screen.availWidth,screen.availHeight,innerWidth,innerHeight]`)
	if err != nil {
		t.Fatal(err)
	}
	got := value.([]interface{})
	if got[0] != "en-US" || got[1] != 1920 || got[2] != 1008 || got[3] != 800 || got[4] != 600 {
		t.Fatalf("properties or viewport incorrect: %#v", value)
	}
}
