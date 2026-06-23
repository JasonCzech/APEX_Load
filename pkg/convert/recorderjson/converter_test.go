package recorderjson

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
)

const chromeRecordingJSON = `{
  "title": "Checkout flow",
  "steps": [
    {"type": "setViewport", "width": 1280, "height": 720},
    {"type": "navigate", "url": "https://shop.example/login", "assertedEvents": [{"type": "navigation", "url": "https://shop.example/login"}]},
    {"type": "change", "value": "jason", "selectors": [["aria/Username"], ["#username"], ["xpath///*[@id=\"username\"]"]]},
    {"type": "click", "selectors": [["aria/Sign in[role=\"button\"]"], ["[data-testid='login-btn']"], ["pierce/#login-widget button"]], "offsetX": 7, "offsetY": 9, "button": "secondary", "duration": 125, "assertedEvents": [{"type": "navigation", "url": "https://shop.example/account"}]},
    {"type": "keyDown", "key": "Tab"},
    {"type": "keyUp", "key": "Tab"},
    {"type": "waitForElement", "selectors": [["#status"]], "operator": ">=", "count": 2, "attributes": {"aria-busy": "false"}, "properties": {"disabled": "false"}},
    {"type": "doubleClick", "selectors": [["#row-1"]]},
    {"type": "hover", "selectors": [["text/Order details"]]},
    {"type": "scroll", "distancePercentage": 50},
    {"type": "close"}
  ]
}`

func TestConvertChromeRecording(t *testing.T) {
	result, err := Convert([]byte(chromeRecordingJSON), ConvertOptions{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	script := result.Script
	if script.Protocol != "browser" {
		t.Errorf("protocol = %q", script.Protocol)
	}
	if script.Name != "Checkout flow" {
		t.Errorf("name = %q", script.Name)
	}
	if script.Config.BaseURL != "https://shop.example" {
		t.Errorf("base url = %q", script.Config.BaseURL)
	}
	if script.Config.Browser.Viewport.Width != 1280 || script.Config.Browser.Viewport.Height != 720 {
		t.Errorf("viewport = %+v", script.Config.Browser.Viewport)
	}
	if result.Stats.Format != "chrome_devtools_recorder" {
		t.Errorf("format = %q", result.Stats.Format)
	}

	types := make([]string, 0, len(script.Actions))
	for _, action := range script.Actions {
		types = append(types, action.Type)
	}
	want := []string{"browser_navigate", "browser_fill", "browser_click", "browser_press_key", "browser_wait", "browser_dblclick", "browser_hover", "browser_evaluate_js"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("action types = %v, want %v", types, want)
	}

	nav := script.Actions[0]
	if nav.Target != "/login" {
		t.Errorf("navigate target = %q", nav.Target)
	}
	fill := script.Actions[1]
	if fill.Selector != "#username" || fill.Value != "jason" {
		t.Errorf("fill = %+v", fill)
	}
	click := script.Actions[2]
	if click.Selector != `[data-testid='login-btn']` {
		t.Errorf("click selector = %q", click.Selector)
	}
	if click.Sync == nil || click.Sync.Type != "load" {
		t.Errorf("click sync = %+v, want load from assertedEvents", click.Sync)
	}
	if click.Metadata["source_click_button"] != "secondary" {
		t.Errorf("click metadata button = %+v", click.Metadata)
	}
	if offset, ok := click.Metadata["source_click_offset"].(map[string]int); !ok || offset["x"] != 7 || offset["y"] != 9 {
		t.Errorf("click offset = %+v", click.Metadata["source_click_offset"])
	}
	if click.Metadata["source_duration_ms"] != 125 {
		t.Errorf("click duration = %+v", click.Metadata)
	}
	candidates, ok := click.Metadata["source_selector_candidates"].([]selector.Candidate)
	if !ok || len(candidates) != 3 {
		t.Fatalf("click candidates = %v", click.Metadata["source_selector_candidates"])
	}
	// role candidate from aria/, css from data-testid, shadow-piercing css
	kinds := map[string]bool{}
	for _, candidate := range candidates {
		kinds[candidate.Kind] = true
	}
	if !kinds["role"] || !kinds["css"] {
		t.Errorf("candidate kinds = %v", candidates)
	}
	press := script.Actions[3]
	if press.Value != "Tab" {
		t.Errorf("press = %+v", press)
	}
	wait := script.Actions[4]
	if wait.WaitFor == nil || wait.WaitFor.Type != "visible" || wait.WaitFor.Value != "#status" {
		t.Errorf("wait = %+v", wait.WaitFor)
	}
	if wait.Params["operator"] != ">=" || wait.Params["count"] != 2 {
		t.Errorf("wait params = %+v", wait.Params)
	}

	scroll := script.Actions[7]
	if scroll.Metadata["review_required"] != true {
		t.Errorf("scroll metadata = %+v, want review_required", scroll.Metadata)
	}
	if scroll.Metadata["source_scroll_distance_percentage"] != 50.0 || !strings.Contains(scroll.Target, "scrollBy") {
		t.Errorf("scroll action = %+v", scroll)
	}

	// close is unsupported; scroll is preserved as review-gated JS.
	if result.Stats.SkippedSteps != 1 {
		t.Errorf("skipped = %d, want 1 (%v)", result.Stats.SkippedSteps, result.Unsupported)
	}
	if len(result.Unsupported) != 1 {
		t.Errorf("unsupported = %v", result.Unsupported)
	}
	if len(result.StepMapping) != 11 {
		t.Fatalf("step mapping = %+v, want all 11 source steps", result.StepMapping)
	}
	if result.StepMapping[0].Disposition != "mapped" || result.StepMapping[0].Name != "" {
		t.Errorf("viewport mapping = %+v", result.StepMapping[0])
	}
	if result.StepMapping[9].Disposition != "degraded" || !strings.Contains(result.StepMapping[9].Reason, "distancePercentage") {
		t.Errorf("scroll mapping = %+v", result.StepMapping[9])
	}
	if result.StepMapping[10].Disposition != "unsupported" || result.StepMapping[10].SourceStepType != "close" {
		t.Errorf("close mapping = %+v", result.StepMapping[10])
	}
}

func TestConvertChromeAriaSelectorMapping(t *testing.T) {
	tests := []struct {
		name      string
		chains    [][]string
		wantKind  string
		wantValue string
	}{
		{
			name:      "aria with role",
			chains:    [][]string{{`aria/Submit[role="button"]`}},
			wantKind:  "role",
			wantValue: `role=button[name="Submit"]`,
		},
		{
			name:      "bare aria becomes text",
			chains:    [][]string{{"aria/Search"}},
			wantKind:  "text",
			wantValue: `text="Search"`,
		},
		{
			name:      "pierce chain joins with descendant combinator",
			chains:    [][]string{{"#host", "#inner"}},
			wantKind:  "css",
			wantValue: "#host #inner",
		},
		{
			name:      "xpath chain in shadow dropped",
			chains:    [][]string{{"xpath/#a", "xpath/#b"}},
			wantKind:  "",
			wantValue: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := candidatesFromChromeSelectors(tt.chains)
			if tt.wantKind == "" {
				if len(candidates) != 0 {
					t.Fatalf("candidates = %v, want none", candidates)
				}
				return
			}
			if len(candidates) != 1 || candidates[0].Kind != tt.wantKind || candidates[0].Value != tt.wantValue {
				t.Errorf("candidates = %v, want %s:%s", candidates, tt.wantKind, tt.wantValue)
			}
		})
	}
}

func TestConvertChromeNoSelectors(t *testing.T) {
	payload := `{"steps":[{"type":"navigate","url":"https://x.example/"},{"type":"click","selectors":[]}]}`
	result, err := Convert([]byte(payload), ConvertOptions{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Stats.SkippedSteps != 1 {
		t.Errorf("skipped = %d, want 1", result.Stats.SkippedSteps)
	}
	if len(result.Script.Actions) != 1 {
		t.Errorf("actions = %d", len(result.Script.Actions))
	}
	if len(result.StepMapping) != 2 || result.StepMapping[1].Disposition != "unsupported" {
		t.Errorf("step mapping = %+v", result.StepMapping)
	}
}

func TestConvertErrors(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr string
	}{
		{name: "empty", payload: "  ", wantErr: "recording content is required"},
		{name: "bad json", payload: "{broken", wantErr: "parse Chrome DevTools Recorder JSON"},
		{name: "no steps", payload: `{"title":"x","steps":[]}`, wantErr: "no steps"},
		{name: "unrecognized text", payload: "SELECT * FROM users;", wantErr: "unrecognized recording format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Convert([]byte(tt.payload), ConvertOptions{})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Convert() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

const playwrightScript = `
const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  await page.goto('https://shop.example/login');
  await page.locator('#username').fill('jason');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.getByTestId('nav-cart').click();
  await page.getByText('Order details').hover();
  await page.getByLabel('Coupon code').fill('SAVE10');
  await page.getByPlaceholder('Search products').fill('laptop');
  await expect(page).toHaveURL('https://shop.example/cart');
  await expect(page).toHaveTitle(/Cart/);
  await page.locator('#qty').selectOption('2');
  await page.locator('#search').press('Enter');
  await page.locator('xpath=//button[3]').dblclick();
  await page.getByRole('dialog').click();
  await page.locator('#x').screenshot({ path: 'x.png' });
})();
`

func TestConvertPlaywrightScript(t *testing.T) {
	result, err := Convert([]byte(playwrightScript), ConvertOptions{ScriptName: "PW Import"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Stats.Format != "playwright_codegen" {
		t.Errorf("format = %q", result.Stats.Format)
	}
	script := result.Script
	if script.Config.BaseURL != "https://shop.example" {
		t.Errorf("base url = %q", script.Config.BaseURL)
	}

	wantSelectors := map[string]string{
		"browser_navigate": "/login",
	}
	types := make([]string, 0, len(script.Actions))
	for _, action := range script.Actions {
		types = append(types, action.Type)
	}
	want := []string{"browser_navigate", "browser_fill", "browser_click", "browser_click", "browser_hover",
		"browser_fill", "browser_fill", "browser_wait", "browser_wait", "browser_select", "browser_press_key", "browser_dblclick"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("action types = %v, want %v", types, want)
	}
	if script.Actions[0].Target != wantSelectors["browser_navigate"] {
		t.Errorf("navigate target = %q", script.Actions[0].Target)
	}
	if script.Actions[2].Selector != `role=button[name="Sign in"]` {
		t.Errorf("getByRole selector = %q", script.Actions[2].Selector)
	}
	if script.Actions[3].Selector != `[data-testid="nav-cart"]` {
		t.Errorf("getByTestId selector = %q", script.Actions[3].Selector)
	}
	if script.Actions[4].Selector != `text="Order details"` {
		t.Errorf("getByText selector = %q", script.Actions[4].Selector)
	}
	if script.Actions[5].Selector != `[aria-label="Coupon code"]` || script.Actions[5].Value != "SAVE10" {
		t.Errorf("getByLabel fill = %+v", script.Actions[5])
	}
	labelCandidates, ok := script.Actions[5].Metadata["source_selector_candidates"].([]selector.Candidate)
	if !ok || len(labelCandidates) < 2 || labelCandidates[1].Kind != "text" {
		t.Errorf("getByLabel candidates = %+v", script.Actions[5].Metadata["source_selector_candidates"])
	}
	if script.Actions[5].Metadata["import_disposition"] != "degraded" {
		t.Errorf("getByLabel disposition = %+v", script.Actions[5].Metadata)
	}
	if script.Actions[7].Assertions[0].Type != "url_contains" || script.Actions[7].Assertions[0].Value != "/cart" {
		t.Errorf("toHaveURL action = %+v", script.Actions[7])
	}
	if script.Actions[8].Assertions[0].Type != "page_title" || script.Actions[8].Assertions[0].Value != "Cart" {
		t.Errorf("toHaveTitle action = %+v", script.Actions[8])
	}
	if script.Actions[9].Value != "2" {
		t.Errorf("selectOption value = %q", script.Actions[9].Value)
	}
	if script.Actions[10].Value != "Enter" {
		t.Errorf("press value = %q", script.Actions[10].Value)
	}
	if script.Actions[11].Selector != "xpath=//button[3]" {
		t.Errorf("xpath dblclick selector = %q", script.Actions[11].Selector)
	}

	// getByRole without name + screenshot statement are skipped with diagnostics
	if result.Stats.SkippedSteps != 2 {
		t.Errorf("skipped = %d (%v)", result.Stats.SkippedSteps, result.Unsupported)
	}
	if len(result.StepMapping) < 14 {
		t.Fatalf("step mapping = %+v", result.StepMapping)
	}
	degraded := 0
	unsupported := 0
	for _, mapping := range result.StepMapping {
		if mapping.Disposition == "degraded" {
			degraded++
		}
		if mapping.Disposition == "unsupported" {
			unsupported++
		}
	}
	if degraded == 0 || unsupported != 2 {
		t.Errorf("step mapping dispositions = %+v", result.StepMapping)
	}
}

func TestConvertPlaywrightStateAndLocatorSuffixes(t *testing.T) {
	result, err := Convert([]byte(`
await page.goto('https://shop.example/settings');
await page.frameLocator('#pay').getByLabel('Save card').check();
await page.getByTestId('delete').first().click();
await page.getByRole('button', { name: 'Remove' }).nth(2).click();
await page.getByText('Total').filter({ hasText: '$10' }).hover();
`), ConvertOptions{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got := actionTypes(result.Script.Actions); strings.Join(got, ",") != "browser_navigate,browser_click,browser_click,browser_click,browser_hover" {
		t.Fatalf("actions = %v", got)
	}
	check := result.Script.Actions[1]
	if check.Metadata["frame_selector"] != "#pay" || check.Metadata["source_checkbox_state"] != "check" {
		t.Errorf("check metadata = %+v", check.Metadata)
	}
	for _, idx := range []int{1, 2, 3, 4} {
		if result.Script.Actions[idx].Metadata["import_disposition"] != "degraded" {
			t.Errorf("action %d metadata = %+v, want degraded", idx, result.Script.Actions[idx].Metadata)
		}
	}
}

func TestConvertPlaywrightNoActions(t *testing.T) {
	_, err := Convert([]byte("await page.goto2('nope'); locator getBy"), ConvertOptions{})
	if err == nil {
		t.Error("expected error for script with no convertible statements")
	}
}

func TestChromeRecorderPromotesAssertedNavigationToAssertions(t *testing.T) {
	result, err := Convert([]byte(`{
  "title": "Login",
  "steps": [
    {"type": "navigate", "url": "https://shop.example/login"},
    {"type": "click", "selectors": [["#submit"]],
     "assertedEvents": [{"type": "navigation", "url": "https://shop.example/account/home", "title": "My Account"}]}
  ]
}`), ConvertOptions{ScriptName: "Asserted Nav"})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	click := result.Script.Actions[1]
	if click.Sync == nil || click.Sync.Type != "load" {
		t.Fatalf("click sync = %+v, want load", click.Sync)
	}
	wantAssertions := map[string]string{
		"url_contains": "/account/home",
		"page_title":   "My Account",
	}
	if len(click.Assertions) != len(wantAssertions) {
		t.Fatalf("assertions = %+v, want url_contains + page_title", click.Assertions)
	}
	for _, assertion := range click.Assertions {
		want, ok := wantAssertions[assertion.Type]
		if !ok {
			t.Fatalf("unexpected assertion type %q", assertion.Type)
		}
		if assertion.Value != want {
			t.Fatalf("%s value = %q, want %q", assertion.Type, assertion.Value, want)
		}
	}
}

func TestNavigationAssertionValue(t *testing.T) {
	tests := []struct {
		rawURL string
		want   string
	}{
		{"https://shop.example/account/home", "/account/home"},
		{"https://shop.example/", "https://shop.example/"},
		{"https://shop.example", "https://shop.example"},
		{"  ", ""},
	}
	for _, tt := range tests {
		if got := navigationAssertionValue(tt.rawURL); got != tt.want {
			t.Errorf("navigationAssertionValue(%q) = %q, want %q", tt.rawURL, got, tt.want)
		}
	}
}

func actionTypes(actions []dsl.Action) []string {
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		out = append(out, action.Type)
	}
	return out
}
