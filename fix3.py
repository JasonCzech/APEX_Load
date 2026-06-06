import re

with open("pkg/protocol/browser/browser.go", "r") as f:
    code = f.read()

# 1. cdp.Send assignment mismatch
code = re.sub(r'_ = cdp.Send\("Emulation.setCPUThrottlingRate"', '_, _ = cdp.Send("Emulation.setCPUThrottlingRate"', code)

# 2. remove resolveNetworkProfile
code = re.sub(r'func resolveNetworkProfile\([^)]*\)\s+\(int,\s+int,\s+network\.ConnectionType\)\s+\{.*?\n\}\n', '', code, flags=re.DOTALL)

# 3. remove remoteObjectText
code = re.sub(r'func remoteObjectText\([^)]*\)\s+string\s+\{.*?\n\}\n', '', code, flags=re.DOTALL)

# 4. replace `if !b.initialized() {`
code = code.replace('if !b.initialized() {', 'if b.pwPage == nil {')

# 5. replace `actionCtx, cancel := b.executionContext(ctx)`
code = code.replace('actionCtx, cancel := b.executionContext(ctx)', 'actionCtx, cancel := context.WithCancel(ctx)')

# 6. Add selectOption
select_option = '''
func (b *Browser) selectOption(ctx context.Context, selector, value string) error {
	if err := b.waitVisible(ctx, selector); err != nil {
		return err
	}
	_, err := b.pwPage.SelectOption(selector, playwright.SelectOptionValues{
		Values: playwright.StringSlice(value),
	})
	return err
}
'''
code += "\n" + select_option

with open("pkg/protocol/browser/browser.go", "w") as f:
    f.write(code)
