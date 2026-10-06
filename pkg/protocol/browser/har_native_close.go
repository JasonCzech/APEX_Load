package browser

import playwright "github.com/mxschmitt/playwright-go"

// Keep the optional capability explicit: upstream ordinary Close may return nil
// for an existing/server close without executing or joining a HAR flush.
func closeOwnedHARBrowserContext(browserContext playwright.BrowserContext) error {
	owner, ok := browserContext.(interface {
		CloseHAROwned(...playwright.BrowserContextCloseOptions) error
	})
	if !ok {
		return errHAROwnershipUnproven
	}
	return owner.CloseHAROwned()
}
