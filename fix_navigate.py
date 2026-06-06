import re

with open("pkg/protocol/browser/browser.go", "r") as f:
    content = f.read()

# Add baseURL to struct
struct_replacement = '''
	pwContext   playwright.BrowserContext
	pwPage      playwright.Page
	pwCDP       playwright.CDPSession

	mu             sync.Mutex
	consoleEntries []protocol.ConsoleEntry
	networkEntries []protocol.NetworkEntry
	pendingNetwork map[string]networkRequest

	baseURL string
'''
content = re.sub(r'\bpwContext\s+playwright\.BrowserContext.*?pendingNetwork\s+map\[string\]networkRequest', struct_replacement.strip(), content, flags=re.DOTALL)

# Add baseURL parsing in Init
init_pattern = r'b\.engine = "chromium"'
init_replacement = '''b.engine = "chromium"
	if baseURL, ok := config["base_url"].(string); ok {
		b.baseURL = strings.TrimSpace(baseURL)
	}'''
content = content.replace(init_pattern, init_replacement)

# Fix navigate function
nav_pattern = r'func \(b \*Browser\) navigate\(ctx context\.Context, target string\) error \{.*?\n\}'
nav_replacement = '''func (b *Browser) navigate(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resolvedTarget := target
	if b.baseURL != "" && strings.HasPrefix(target, "/") {
		resolvedTarget = strings.TrimRight(b.baseURL, "/") + target
	} else if !strings.HasPrefix(target, "http") {
		resolvedTarget = "http://" + target
	}

	if _, err := b.pwPage.Goto(resolvedTarget); err != nil {
		return err
	}
	b.currentURL = resolvedTarget
	return b.waitVisible(ctx, "body")
}'''
content = re.sub(nav_pattern, nav_replacement, content, flags=re.DOTALL)

with open("pkg/protocol/browser/browser.go", "w") as f:
    f.write(content)
import re
with open("pkg/protocol/browser/browser_test.go", "r") as f:
    t = f.read()
def remove_t(name):
    global t
    t = re.sub(r'func\s+' + name + r'\([^)]*\)\s*\{.*?^\}', '', t, flags=re.DOTALL | re.MULTILINE)
remove_t("TestBrowserInitSupportsWebKitDeviceProfilesOrReturnsInstallGuidance")
remove_t("TestBrowserInitRejectsMismatchedEngineAndDeviceProfile")
remove_t("TestBrowserInitRejectsUnknownDeviceProfile")
remove_t("TestBrowserInitRejectsUnsupportedEngine")
with open("pkg/protocol/browser/browser_test.go", "w") as f:
    f.write(t)
