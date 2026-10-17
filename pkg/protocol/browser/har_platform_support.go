package browser

import (
	"errors"
	"runtime"
	"sync"
)

// harUnavailablePlatform is the HAR-unavailable reason a session reports
// when this platform cannot own native HAR capture.
const harUnavailablePlatform = "har_platform_unsupported"

// errHARPlatformUnsupported refuses native HAR ownership on a platform whose
// private creation, identity and publication are not qualified.
var errHARPlatformUnsupported = errors.New("native HAR capture is not supported on " + runtime.GOOS)

// nativeHARSupported is this platform's answer; tests replace it to run an
// unsupported platform's path.
var nativeHARSupported = nativeHARPlatformSupported

var harPlatformUnsupportedLogged sync.Once

// NativeHARSupported reports whether this platform can own native HAR
// capture: private staging, file identity, the namespace lease and durable
// publication. Where it cannot, browser sessions run without a native HAR
// (reporting har_platform_unsupported) and nothing creates or cleans a HAR
// directory's staging.
func NativeHARSupported() bool { return nativeHARSupported() }
