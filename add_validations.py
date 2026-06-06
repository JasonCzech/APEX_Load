import re

with open("pkg/protocol/browser/browser.go", "r") as f:
    content = f.read()

validation_code = """
	if b.engine != "chromium" && b.engine != "webkit" {
		return fmt.Errorf("unsupported browser engine: %s", b.engine)
	}

	if b.deviceProfile != "" {
		profile, ok := LookupDevice(b.deviceProfile)
		if !ok {
			return fmt.Errorf("unknown browser device profile: %s", b.deviceProfile)
		}
		if profile.Engine != "" && profile.Engine != b.engine {
			return fmt.Errorf("incompatible device profile %s for engine %s", b.deviceProfile, b.engine)
		}
		b.devicePlatform = profile.Platform
		b.deviceTier = profile.DeviceTier
		if b.userAgent == "" && profile.UserAgent != "" {
			b.userAgent = profile.UserAgent
		}
		if b.viewportW == 1440 && profile.Width > 0 {
			b.viewportW = profile.Width
		}
		if b.viewportH == 900 && profile.Height > 0 {
			b.viewportH = profile.Height
		}
		if b.deviceScaleFactor == 1 && profile.ScaleFactor > 0 {
			b.deviceScaleFactor = profile.ScaleFactor
		}
		if !b.touchMode && profile.Touch {
			b.touchMode = true
		}
		if !b.mobile && profile.Mobile {
			b.mobile = true
		}
	}
	
	if b.downloadResources == "recorded" && !b.recordedResourcesAvailable {
		return fmt.Errorf("recorded resource metadata not available")
	}

	pwBrowser, err := getBrowserInstance(b.engine, b.headless)
"""

content = content.replace("pwBrowser, err := getBrowserInstance(b.engine, b.headless)", validation_code.strip())

with open("pkg/protocol/browser/browser.go", "w") as f:
    f.write(content)
