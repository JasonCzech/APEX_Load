package browser

import "sort"

type DeviceProfile struct {
	Name               string
	Platform           string
	BrowserFamily      string
	Width              int
	Height             int
	DeviceScaleFactor  float64
	Mobile             bool
	HasTouch           bool
	DefaultOrientation string
	UserAgent          string
	CPUThrottleRate    float64
	NetworkPreset      string
	DeviceTier         string
}

var deviceProfiles = map[string]DeviceProfile{
	"desktop_1080p": {
		Name:               "desktop_1080p",
		Platform:           "desktop",
		BrowserFamily:      "chromium",
		Width:              1920,
		Height:             1080,
		DeviceScaleFactor:  1,
		DefaultOrientation: "landscape",
		CPUThrottleRate:    1,
		NetworkPreset:      "10mbps",
		DeviceTier:         "high",
	},
	"galaxy_s24": {
		Name:               "galaxy_s24",
		Platform:           "android",
		BrowserFamily:      "chromium",
		Width:              360,
		Height:             780,
		DeviceScaleFactor:  3,
		Mobile:             true,
		HasTouch:           true,
		DefaultOrientation: "portrait",
		UserAgent:          "Mozilla/5.0 (Linux; Android 14; SM-S921B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Mobile Safari/537.36",
		CPUThrottleRate:    1.5,
		NetworkPreset:      "10mbps",
		DeviceTier:         "high",
	},
	"ipad_air": {
		Name:               "ipad_air",
		Platform:           "ipados",
		BrowserFamily:      "webkit",
		Width:              820,
		Height:             1180,
		DeviceScaleFactor:  2,
		Mobile:             true,
		HasTouch:           true,
		DefaultOrientation: "portrait",
		UserAgent:          "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
		CPUThrottleRate:    2,
		NetworkPreset:      "dsl",
		DeviceTier:         "mid",
	},
	"iphone_15_pro": {
		Name:               "iphone_15_pro",
		Platform:           "ios",
		BrowserFamily:      "webkit",
		Width:              393,
		Height:             852,
		DeviceScaleFactor:  3,
		Mobile:             true,
		HasTouch:           true,
		DefaultOrientation: "portrait",
		UserAgent:          "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
		CPUThrottleRate:    1.25,
		NetworkPreset:      "10mbps",
		DeviceTier:         "high",
	},
	"iphone_se": {
		Name:               "iphone_se",
		Platform:           "ios",
		BrowserFamily:      "webkit",
		Width:              375,
		Height:             667,
		DeviceScaleFactor:  2,
		Mobile:             true,
		HasTouch:           true,
		DefaultOrientation: "portrait",
		UserAgent:          "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Mobile/15E148 Safari/604.1",
		CPUThrottleRate:    4,
		NetworkPreset:      "56k",
		DeviceTier:         "low",
	},
	"pixel_8": {
		Name:               "pixel_8",
		Platform:           "android",
		BrowserFamily:      "chromium",
		Width:              412,
		Height:             915,
		DeviceScaleFactor:  2.625,
		Mobile:             true,
		HasTouch:           true,
		DefaultOrientation: "portrait",
		UserAgent:          "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Mobile Safari/537.36",
		CPUThrottleRate:    1.75,
		NetworkPreset:      "10mbps",
		DeviceTier:         "high",
	},
}

func LookupDevice(name string) (DeviceProfile, bool) {
	profile, ok := deviceProfiles[name]
	return profile, ok
}

func ListDevices() []DeviceProfile {
	names := make([]string, 0, len(deviceProfiles))
	for name := range deviceProfiles {
		names = append(names, name)
	}
	sort.Strings(names)
	profiles := make([]DeviceProfile, 0, len(names))
	for _, name := range names {
		profiles = append(profiles, deviceProfiles[name])
	}
	return profiles
}
