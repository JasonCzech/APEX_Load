package browser

import (
	"encoding/json"
	"math"

	"github.com/jasonczech/apexload/pkg/dsl"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/mxschmitt/playwright-go"
)

const (
	RecordingEnvironmentVersion = 1
	RecordingEnvironmentKey     = "recording_environment"
	RecordingObservationLocal   = "local_recorder"
	RecordingObservationAgent   = "agent_reported"
	RecordingPhysicalUnverified = "unverified"
)

// RecordingEnvironment is a historical recorder receipt. It describes a local
// browser launch and applied emulation, never physical-device qualification or
// the engine that an edited script will use for future replay.
type RecordingEnvironment struct {
	Version                     int                      `json:"version"`
	ObservationSource           string                   `json:"observation_source"`
	Engine                      string                   `json:"engine"`
	DeviceProfile               string                   `json:"device_profile,omitempty"`
	ProfileBrowserFamily        string                   `json:"profile_browser_family,omitempty"`
	Emulated                    bool                     `json:"emulated"`
	AppliedContext              *RecordingAppliedContext `json:"applied_context,omitempty"`
	PhysicalDeviceQualification string                   `json:"physical_device_qualification"`
	// BrowserVersion is the launched browser's version, which replay
	// compares with its own.
	BrowserVersion string `json:"browser_version,omitempty"`
}

// RecordedBrowserVersion is the browser version the recording ran in.
func (environment *RecordingEnvironment) RecordedBrowserVersion() string {
	if environment == nil {
		return ""
	}
	return environment.BrowserVersion
}

// RecordingAppliedContext records explicit context options only. Missing
// pointers mean unapplied/unknown; an explicit false remains distinguishable.
type RecordingAppliedContext struct {
	ViewportWidth     *int     `json:"viewport_width,omitempty"`
	ViewportHeight    *int     `json:"viewport_height,omitempty"`
	DeviceScaleFactor *float64 `json:"device_scale_factor,omitempty"`
	Mobile            *bool    `json:"mobile,omitempty"`
	HasTouch          *bool    `json:"has_touch,omitempty"`
}

func (environment *RecordingEnvironment) Clone() *RecordingEnvironment {
	if environment == nil {
		return nil
	}
	clone := *environment
	if environment.AppliedContext != nil {
		context := *environment.AppliedContext
		context.ViewportWidth = copyRecordingPointer(context.ViewportWidth)
		context.ViewportHeight = copyRecordingPointer(context.ViewportHeight)
		context.DeviceScaleFactor = copyRecordingPointer(context.DeviceScaleFactor)
		context.Mobile = copyRecordingPointer(context.Mobile)
		context.HasTouch = copyRecordingPointer(context.HasTouch)
		clone.AppliedContext = &context
	}
	return &clone
}

func copyRecordingPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// NormalizeRecordingEnvironment reads only this fixed schema, with no JSON
// marshaling/traversal of arbitrary claims. A bridge may override provenance,
// but it cannot turn a claim into a local observation or physical proof.
func NormalizeRecordingEnvironment(value any, sourceOverride string) *RecordingEnvironment {
	var environment *RecordingEnvironment
	switch input := value.(type) {
	case *RecordingEnvironment:
		environment = input.Clone()
	case RecordingEnvironment:
		environment = input.Clone()
	case map[string]any:
		version, ok := recordingInteger(input["version"])
		if !ok {
			return nil
		}
		engine, ok := input["engine"].(string)
		if !ok {
			return nil
		}
		emulated, ok := input["emulated"].(bool)
		if !ok {
			return nil
		}
		source, _ := input["observation_source"].(string)
		environment = &RecordingEnvironment{Version: version, Engine: engine, Emulated: emulated, ObservationSource: source}
		for key, target := range map[string]*string{"device_profile": &environment.DeviceProfile, "profile_browser_family": &environment.ProfileBrowserFamily, "browser_version": &environment.BrowserVersion} {
			if field, exists := input[key]; exists {
				text, ok := field.(string)
				if !ok {
					return nil
				}
				*target = text
			}
		}
		if field, exists := input["applied_context"]; exists && field != nil {
			context, ok := field.(map[string]any)
			if !ok {
				return nil
			}
			environment.AppliedContext = &RecordingAppliedContext{}
			for key, target := range map[string]**int{"viewport_width": &environment.AppliedContext.ViewportWidth, "viewport_height": &environment.AppliedContext.ViewportHeight} {
				if field, exists := context[key]; exists {
					integer, ok := recordingInteger(field)
					if !ok {
						return nil
					}
					*target = &integer
				}
			}
			if field, exists := context["device_scale_factor"]; exists {
				number, ok := recordingNumber(field)
				if !ok {
					return nil
				}
				environment.AppliedContext.DeviceScaleFactor = &number
			}
			for key, target := range map[string]**bool{"mobile": &environment.AppliedContext.Mobile, "has_touch": &environment.AppliedContext.HasTouch} {
				if field, exists := context[key]; exists {
					boolean, ok := field.(bool)
					if !ok {
						return nil
					}
					*target = &boolean
				}
			}
		}
	default:
		return nil
	}
	if environment == nil || environment.Version != RecordingEnvironmentVersion || !knownRecordingEngine(environment.Engine) {
		return nil
	}
	if sourceOverride != "" {
		if sourceOverride != RecordingObservationAgent {
			return nil
		}
		environment.ObservationSource = sourceOverride
	}
	if environment.ObservationSource != RecordingObservationLocal && environment.ObservationSource != RecordingObservationAgent {
		return nil
	}
	if environment.BrowserVersion != "" && !browserprotocol.ValidBrowserVersion(environment.BrowserVersion) {
		return nil
	}
	if len(environment.DeviceProfile) > 64 || len(environment.ProfileBrowserFamily) > 32 || (!environment.Emulated && (environment.DeviceProfile != "" || environment.ProfileBrowserFamily != "")) {
		return nil
	}
	if environment.DeviceProfile != "" {
		profile, known := browserprotocol.LookupDevice(environment.DeviceProfile)
		if !known || (environment.ProfileBrowserFamily != "" && environment.ProfileBrowserFamily != profile.BrowserFamily) {
			return nil
		}
	}
	if environment.ProfileBrowserFamily != "" && !knownRecordingEngine(environment.ProfileBrowserFamily) {
		return nil
	}
	if context := environment.AppliedContext; context != nil {
		if (context.ViewportWidth == nil) != (context.ViewportHeight == nil) {
			return nil
		}
		if context.ViewportWidth != nil && (*context.ViewportWidth <= 0 || *context.ViewportHeight <= 0 || *context.ViewportWidth > maxBrowserViewportDimension || *context.ViewportHeight > maxBrowserViewportDimension || int64(*context.ViewportWidth)*int64(*context.ViewportHeight) > maxBrowserViewportPixels) {
			return nil
		}
		if context.DeviceScaleFactor != nil && (math.IsNaN(*context.DeviceScaleFactor) || math.IsInf(*context.DeviceScaleFactor, 0) || *context.DeviceScaleFactor <= 0 || *context.DeviceScaleFactor > maxBrowserDeviceScale) {
			return nil
		}
		if context.ViewportWidth != nil && context.DeviceScaleFactor != nil && float64(*context.ViewportWidth)*float64(*context.ViewportHeight)*(*context.DeviceScaleFactor)*(*context.DeviceScaleFactor) > maxBrowserDevicePixels {
			return nil
		}
		if context.ViewportWidth == nil && context.DeviceScaleFactor == nil && context.Mobile == nil && context.HasTouch == nil {
			environment.AppliedContext = nil
		}
	}
	environment.PhysicalDeviceQualification = RecordingPhysicalUnverified
	return environment
}

func knownRecordingEngine(engine string) bool {
	switch engine {
	case "chromium", "webkit", "firefox", "chrome":
		return true
	}
	return false
}

func recordingInteger(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case float64:
		if !math.IsNaN(number) && !math.IsInf(number, 0) && number >= -1<<30 && number <= 1<<30 && math.Trunc(number) == number {
			return int(number), true
		}
	case json.Number:
		if len(number) > 32 {
			return 0, false
		}
		integer, err := number.Int64()
		if err == nil && integer >= -1<<30 && integer <= 1<<30 {
			return int(integer), true
		}
	}
	return 0, false
}

func recordingNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int:
		return float64(number), true
	case json.Number:
		if len(number) > 32 {
			return 0, false
		}
		floating, err := number.Float64()
		return floating, err == nil
	}
	return 0, false
}

func recordingEnvironmentForContext(device *DeviceEmulation, options playwright.BrowserNewContextOptions, browserVersion string) *RecordingEnvironment {
	environment := &RecordingEnvironment{Version: RecordingEnvironmentVersion, ObservationSource: RecordingObservationLocal, Engine: "chromium", Emulated: device != nil, PhysicalDeviceQualification: RecordingPhysicalUnverified}
	if browserprotocol.ValidBrowserVersion(browserVersion) {
		environment.BrowserVersion = browserVersion
	}
	if device != nil && len(device.Name) <= 64 {
		if profile, known := browserprotocol.LookupDevice(device.Name); known {
			environment.DeviceProfile, environment.ProfileBrowserFamily = profile.Name, profile.BrowserFamily
		}
	}
	context := &RecordingAppliedContext{DeviceScaleFactor: copyRecordingPointer(options.DeviceScaleFactor), Mobile: copyRecordingPointer(options.IsMobile), HasTouch: copyRecordingPointer(options.HasTouch)}
	if options.Viewport != nil {
		width, height := options.Viewport.Width, options.Viewport.Height
		context.ViewportWidth, context.ViewportHeight = &width, &height
	}
	environment.AppliedContext = context
	return NormalizeRecordingEnvironment(environment, "")
}

// AttachRecordingEnvironment changes only this receipt. HTTP/Salesforce import
// provenance and review remain intact; legacy artifacts create no observation.
func AttachRecordingEnvironment(script *dsl.Script, environment *RecordingEnvironment) {
	if script == nil {
		return
	}
	canonical := NormalizeRecordingEnvironment(environment, "")
	if canonical == nil {
		if script.Metadata != nil && script.Metadata.Import != nil {
			delete(script.Metadata.Import.Preserved, RecordingEnvironmentKey)
		}
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	script.Metadata.Import.Preserved[RecordingEnvironmentKey] = canonical
}
