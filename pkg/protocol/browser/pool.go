package browser

import (
	"fmt"
	"strings"
	"sync"

	playwright "github.com/playwright-community/playwright-go"
)

var (
	globalPlaywright *playwright.Playwright
	globalChromium   playwright.Browser
	globalWebKit     playwright.Browser
	globalMutex      sync.Mutex
)

func getBrowserInstance(engine string, headless bool) (playwright.Browser, error) {
	globalMutex.Lock()
	defer globalMutex.Unlock()

	if globalPlaywright == nil {
		pw, err := playwright.Run()
		if err != nil {
			return nil, fmt.Errorf("failed to start playwright: %w", err)
		}
		globalPlaywright = pw
	}

	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" {
		engine = "chromium"
	}

	if engine == "webkit" {
		if globalWebKit == nil {
			b, err := globalPlaywright.WebKit.Launch(playwright.BrowserTypeLaunchOptions{
				Headless: playwright.Bool(headless),
				Timeout:  playwright.Float(30000),
			})
			if err != nil {
				return nil, err
			}
			globalWebKit = b
		}
		return globalWebKit, nil
	}

	if globalChromium == nil {
		b, err := globalPlaywright.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
			Headless: playwright.Bool(headless),
			Timeout:  playwright.Float(30000),
			Args: []string{
				"--disable-gpu",
				"--hide-scrollbars",
				"--no-sandbox",
			},
		})
		if err != nil {
			return nil, err
		}
		globalChromium = b
	}
	return globalChromium, nil
}
