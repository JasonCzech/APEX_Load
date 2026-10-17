package browsercontract

import "time"

const (
	MaxConfiguredViewportDimension = 8192
	MaxConfiguredViewportPixels    = 16 << 20
	MaxConfiguredBrowserDelay      = time.Hour
	MaxConfiguredNetworkKbps       = 100_000_000
	DefaultDialogWaitMS            = 1000
	DefaultEngine                  = "chromium"
	DefaultHeadless                = true
	DefaultCacheEnabled            = true
	DefaultCacheContentURLs        = true
	DefaultDownloadResources       = "all"
	DefaultOrientation             = "landscape"
	DefaultViewportWidth           = 1440
	DefaultViewportHeight          = 900
	DefaultDeviceScaleFactor       = 1
	DefaultCPUThrottleRate         = 1
)
