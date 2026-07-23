package dsl

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/language"
)

const maxBrowserEmulationCustomUABytes = 1024

// validateBrowserEmulationIssues validates config.browser_emulation. The
// section is protocol-agnostic config: it is validated whenever present, and
// simply ignored by protocols that do not consume it.
func validateBrowserEmulationIssues(script *Script) []string {
	cfg := script.Config.BrowserEmulation
	if cfg == nil {
		return nil
	}
	var issues []string

	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case "", "specific", "custom":
	default:
		issues = append(issues, "config.browser_emulation.mode must be specific or custom")
	}

	if len(cfg.CustomUserAgent) > maxBrowserEmulationCustomUABytes {
		issues = append(issues, fmt.Sprintf("config.browser_emulation.custom_user_agent must not exceed %d bytes", maxBrowserEmulationCustomUABytes))
	}
	if containsBrowserEmulationControlCharacter(cfg.CustomUserAgent) {
		issues = append(issues, "config.browser_emulation.custom_user_agent must not contain control characters")
	}

	switch mode {
	case "custom":
		if strings.TrimSpace(cfg.CustomUserAgent) == "" {
			issues = append(issues, "config.browser_emulation.custom_user_agent is required when mode is custom")
		}
		if cfg.BrowserType != "" || cfg.BrowserVersion != "" || cfg.Platform != "" {
			issues = append(issues, "config.browser_emulation browser_type/browser_version/platform must be empty when mode is custom")
		}
	case "specific":
		// An empty browser_type is tolerated: at replay it simply produces no
		// User-Agent override (ResolveBrowserEmulationUserAgent returns "no
		// override" for an empty type). Blocking it would make the editors'
		// natural transient states — e.g. selecting "specific" before picking a
		// browser, or clearing the browser back to Engine default — hard-fail on
		// save for no runtime benefit. The type/platform/version checks below
		// still apply once a browser is actually chosen.
		if strings.TrimSpace(cfg.BrowserType) != "" {
			if !BrowserEmulationTypeInCatalog(cfg.BrowserType) {
				issues = append(issues, fmt.Sprintf("config.browser_emulation.browser_type %q is not in the browser catalog", cfg.BrowserType))
			} else {
				if !BrowserEmulationPlatformValidFor(cfg.BrowserType, cfg.Platform) {
					issues = append(issues, fmt.Sprintf("config.browser_emulation.platform %q is not available for browser_type %q", cfg.Platform, cfg.BrowserType))
				}
				if strings.TrimSpace(cfg.BrowserVersion) != "" && normalizeBrowserMajorVersion(cfg.BrowserVersion) == "" {
					issues = append(issues, fmt.Sprintf("config.browser_emulation.browser_version %q must start with a numeric major version", cfg.BrowserVersion))
				}
			}
		}
		if strings.TrimSpace(cfg.CustomUserAgent) != "" {
			issues = append(issues, "config.browser_emulation.custom_user_agent must be empty when mode is specific")
		}
	}

	if lang := strings.TrimSpace(cfg.Language); lang != "" {
		if _, err := language.Parse(lang); err != nil {
			issues = append(issues, fmt.Sprintf("config.browser_emulation.language %q must be a valid BCP-47 language tag", cfg.Language))
		}
	}

	if cache := cfg.Cache; cache != nil {
		simulateOff := cache.SimulateCache != nil && !*cache.SimulateCache
		if simulateOff {
			if cache.CacheContentURLs != nil && *cache.CacheContentURLs {
				issues = append(issues, "config.browser_emulation.cache.cache_content_urls has no effect while simulate_cache is false")
			}
			if cache.CheckNewerVersions != nil && *cache.CheckNewerVersions {
				issues = append(issues, "config.browser_emulation.cache.check_newer_versions has no effect while simulate_cache is false")
			}
		}
	}
	return issues
}

func containsBrowserEmulationControlCharacter(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
