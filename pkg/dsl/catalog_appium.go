package dsl

import (
	"fmt"
	"net/url"
	"strings"

	appiumprotocol "github.com/jasonczech/apexload/pkg/protocol/appium"
)

// maxAppiumTimeoutMs bounds an Appium session or element timeout.
const maxAppiumTimeoutMs = 10 * 60 * 1000

// appiumActionSpecs declares the steps of a device observer script (protocol
// appium): it drives one device through an Appium server while the run's
// HTTP groups supply the load, and its transactions time what a person on
// the device sees.
func appiumActionSpecs() []ActionSpec {
	element := func(doc string) []FieldSpec {
		return []FieldSpec{
			{Name: "target", Required: true, Kind: FieldKindString, Doc: doc + " Its accessibility id unless params.using names another locator."},
			{Name: "params.using", Kind: FieldKindEnum, Enum: appiumprotocol.Locators, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "Locator strategy; defaults to accessibility id."},
			{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "How long to wait for the element, in milliseconds; defaults to config.appium.element_timeout_ms (15 seconds)."},
		}
	}
	app := []FieldSpec{
		{Name: "params.bundle_id", Required: true, Kind: FieldKindString, Doc: "The app's bundle id (iOS) or package (Android)."},
	}
	return []ActionSpec{
		{
			Type:      "appium_tap",
			Protocols: []string{appiumprotocol.ProtocolName},
			Summary:   "Waits for an element on the device and taps it.",
			Fields:    element("The element to tap."),
			Validate:  validateAppiumElementAction,
		},
		{
			Type:      "appium_type",
			Protocols: []string{appiumprotocol.ProtocolName},
			Summary:   "Waits for a text field on the device and types into it.",
			Fields: append(element("The field to type into."),
				FieldSpec{Name: "value", Kind: FieldKindString, Doc: "The text to type; template variables are expanded."},
				FieldSpec{Name: "params.clear", Kind: FieldKindBool, Doc: "Clear the field first."},
			),
			Validate: validateAppiumElementAction,
		},
		{
			Type:      "appium_wait",
			Protocols: []string{appiumprotocol.ProtocolName},
			Summary:   "Waits until an element shows on the device, optionally with some text; the element's text is the step's response.",
			Fields: append(element("The element to wait for."),
				FieldSpec{Name: "params.text", Kind: FieldKindString, Doc: "Text the element must contain."},
			),
			Validate: validateAppiumElementAction,
		},
		{
			Type:      "appium_activate_app",
			Protocols: []string{appiumprotocol.ProtocolName},
			Summary:   "Brings an app to the foreground, launching it if needed.",
			Fields:    app,
			Validate:  validateAppiumAppAction,
		},
		{
			Type:      "appium_terminate_app",
			Protocols: []string{appiumprotocol.ProtocolName},
			Summary:   "Closes an app, so the next iteration starts it fresh.",
			Fields:    app,
			Validate:  validateAppiumAppAction,
		},
		{
			Type:      "appium_execute",
			Protocols: []string{appiumprotocol.ProtocolName},
			Summary:   "Runs an Appium mobile command, such as mobile: swipe or mobile: scroll.",
			Fields: []FieldSpec{
				{Name: "params.script", Required: true, Kind: FieldKindString, Doc: "The command, such as mobile: swipe."},
				{Name: "params.args", Kind: FieldKindObject, Doc: "The command's arguments, such as {\"direction\": \"up\"}."},
			},
			Validate: func(index int, action Action) []string {
				if script, _ := action.Params["script"].(string); strings.TrimSpace(script) == "" {
					return []string{fmt.Sprintf("actions[%d] %q: appium_execute requires params.script", index, action.Name)}
				}
				if args, ok := action.Params["args"]; ok && args != nil {
					if _, isObject := args.(map[string]any); !isObject {
						return []string{fmt.Sprintf("actions[%d] %q: appium_execute params.args must be an object", index, action.Name)}
					}
				}
				return nil
			},
		},
		{
			Type:      "appium_screenshot",
			Protocols: []string{appiumprotocol.ProtocolName},
			Summary:   "Takes a screenshot of the device for the step's evidence.",
		},
	}
}

func validateAppiumElementAction(index int, action Action) []string {
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	if strings.TrimSpace(action.Target) == "" {
		issues = append(issues, fmt.Sprintf("%s: %s requires a target element", label, action.Type))
	}
	if using, ok := actionParamStringValue(action.Params, "using"); ok && strings.TrimSpace(using) != "" &&
		!isAllowedValue(strings.ToLower(strings.TrimSpace(using)), appiumprotocol.Locators...) {
		issues = append(issues, fmt.Sprintf("%s: params.using must be one of %s", label, strings.Join(appiumprotocol.Locators, ", ")))
	}
	issues = append(issues, appiumTimeoutIssue(label, "params.timeout_ms", action.Params["timeout_ms"])...)
	if want, ok := action.Params["text"]; ok {
		if _, isString := want.(string); !isString {
			issues = append(issues, fmt.Sprintf("%s: params.text must be a string", label))
		}
	}
	return issues
}

func validateAppiumAppAction(index int, action Action) []string {
	if bundle, _ := action.Params["bundle_id"].(string); strings.TrimSpace(bundle) == "" {
		return []string{fmt.Sprintf("actions[%d] %q: %s requires params.bundle_id", index, action.Name, action.Type)}
	}
	return nil
}

func appiumTimeoutIssue(label, field string, raw any) []string {
	if raw == nil {
		return nil
	}
	value, ok := raw.(float64)
	if integer, isInt := raw.(int); isInt {
		value, ok = float64(integer), true
	}
	if !ok || value < 0 || value > maxAppiumTimeoutMs || value != float64(int64(value)) {
		return []string{fmt.Sprintf("%s: %s must be a whole number of milliseconds from 0 to %d", label, field, maxAppiumTimeoutMs)}
	}
	return nil
}

// validateAppiumScriptIssues checks a device observer script's connection.
func validateAppiumScriptIssues(script *Script) []string {
	var issues []string
	cfg := script.Config.Appium
	server := strings.TrimSpace(cfg.ServerURL)
	if server == "" {
		issues = append(issues, "config.appium.server_url is required")
	} else if !strings.Contains(server, "{{") {
		if parsed, err := url.Parse(server); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			issues = append(issues, "config.appium.server_url must be an http or https URL")
		}
	}
	if platform, _ := cfg.Capabilities["platformName"].(string); strings.TrimSpace(platform) == "" {
		issues = append(issues, "config.appium.capabilities.platformName is required (iOS or Android)")
	}
	issues = append(issues, appiumTimeoutIssue("config.appium", "session_timeout_ms", appiumIntValue(cfg.SessionTimeoutMs))...)
	issues = append(issues, appiumTimeoutIssue("config.appium", "element_timeout_ms", appiumIntValue(cfg.ElementTimeoutMs))...)
	return issues
}

func appiumIntValue(value int) any {
	if value == 0 {
		return nil
	}
	return value
}
