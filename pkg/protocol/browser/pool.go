package browser

import (
	"fmt"
	"os"
	"strings"
	"sync"

	playwright "github.com/playwright-community/playwright-go"
)

var (
	globalPlaywright *playwright.Playwright
	globalChromium   playwright.Browser
	globalWebKit     playwright.Browser
	globalFirefox    playwright.Browser
	globalChrome     playwright.Browser
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

	if engine == "firefox" {
		if globalFirefox == nil {
			b, err := globalPlaywright.Firefox.Launch(playwright.BrowserTypeLaunchOptions{
				Headless: playwright.Bool(headless),
				Timeout:  playwright.Float(30000),
			})
			if err != nil {
				return nil, err
			}
			globalFirefox = b
		}
		return globalFirefox, nil
	}

	if engine == "chrome" {
		if globalChrome == nil {
			b, err := globalPlaywright.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
				Headless: playwright.Bool(headless),
				Timeout:  playwright.Float(30000),
				Channel:  playwright.String("chrome"),
				Args: []string{
					"--disable-gpu",
					"--hide-scrollbars",
					"--no-sandbox",
				},
			})
			if err != nil {
				return nil, err
			}
			globalChrome = b
		}
		return globalChrome, nil
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

func RuntimeInstallWarning(engine string) string {
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" || engine == "chromium" || engine == "chrome" {
		return ""
	}
	if err := browserExecutableAvailable(engine); err != nil {
		return fmt.Sprintf("Playwright %s runtime is unavailable: %v. Run `npx playwright install %s` before replaying this browser script.", engine, err, engine)
	}
	return ""
}

func browserExecutableAvailable(engine string) error {
	globalMutex.Lock()
	defer globalMutex.Unlock()

	if globalPlaywright == nil {
		pw, err := playwright.Run()
		if err != nil {
			return fmt.Errorf("failed to start playwright: %w", err)
		}
		globalPlaywright = pw
	}

	var executablePath string
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "webkit":
		executablePath = globalPlaywright.WebKit.ExecutablePath()
	case "firefox":
		executablePath = globalPlaywright.Firefox.ExecutablePath()
	default:
		return nil
	}
	if strings.TrimSpace(executablePath) == "" {
		return fmt.Errorf("browser executable path is empty")
	}
	if _, err := os.Stat(executablePath); err != nil {
		return err
	}
	return nil
}
