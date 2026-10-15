// Package appium drives one real device (or simulator) through an Appium
// server's W3C WebDriver API, so a run can measure what a person on a real
// iPad sees while the HTTP script supplies the load: the device observer of
// decision D12. Each virtual user opens one Appium session at init and closes
// it at teardown; a script's steps tap, type and wait on the app's elements,
// and its transactions time them like any other script's.
package appium

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// ProtocolName is the script protocol of a device observer.
const ProtocolName = "appium"

const (
	defaultSessionTimeout = 3 * time.Minute
	defaultElementTimeout = 15 * time.Second
	defaultCommandTimeout = time.Minute
	elementPollInterval   = 250 * time.Millisecond
	// maxElementTimeout bounds a wait a script asks for (the DSL's bound).
	maxElementTimeout = 10 * time.Minute
	defaultLocator    = "accessibility id"
)

// Locators lists the element locator strategies a step may use.
var Locators = []string{
	"accessibility id", "id", "name", "xpath", "class name",
	"-ios predicate string", "-ios class chain", "-android uiautomator",
}

func init() {
	protocol.Register(ProtocolName, func() protocol.Protocol { return &Appium{} })
}

// Appium is one virtual user's device session.
type Appium struct {
	driver         *webdriver
	capabilities   map[string]any
	sessionTimeout time.Duration
	elementTimeout time.Duration
	session        string
}

func (a *Appium) Name() string { return ProtocolName }

// Init opens the Appium session the script's capabilities describe; a device
// cloud may take minutes to allocate a device and install the app.
func (a *Appium) Init(ctx context.Context, config map[string]any) error {
	serverURL := protocol.ConfigString(config, "appium_server_url")
	driver, err := newWebDriver(serverURL)
	if err != nil {
		return err
	}
	// A governed run reaches only the hosts its runtime policy allows.
	if err := protocol.ValidateTargetURL(serverURL, protocol.TargetPolicyHosts(config)); err != nil {
		return err
	}
	a.driver = driver
	a.capabilities, _ = config["appium_capabilities"].(map[string]any)
	if len(a.capabilities) == 0 {
		return fmt.Errorf("appium capabilities are required (platformName and the app at least)")
	}
	a.sessionTimeout = configDuration(config, "appium_session_timeout_ms", defaultSessionTimeout)
	a.elementTimeout = configDuration(config, "appium_element_timeout_ms", defaultElementTimeout)
	sessionCtx, cancel := context.WithTimeout(ctx, a.sessionTimeout)
	defer cancel()
	session, err := a.driver.newSession(sessionCtx, a.capabilities)
	if err != nil {
		return fmt.Errorf("start the appium session: %w", err)
	}
	a.session = session
	return nil
}

func configDuration(config map[string]any, key string, fallback time.Duration) time.Duration {
	if ms := protocol.ConfigInt(config, key); ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return fallback
}

// Execute runs one step on the device.
func (a *Appium) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
	result := &protocol.Result{
		ActionName:       protocol.ActionName(action),
		StartTime:        start,
		DependencyType:   ProtocolName,
		DependencyTarget: a.dependencyTarget(),
		Extractions:      make(map[string]string),
	}
	err := a.execute(ctx, action, result)
	result.Duration = time.Since(start)
	if err != nil {
		result.Success = false
		result.ErrorMessage = err.Error()
		if result.StatusCode == 0 {
			result.StatusCode = 500
		}
		return result, nil
	}
	result.Success = true
	result.StatusCode = 200
	return result, nil
}

func (a *Appium) dependencyTarget() string {
	if a.driver == nil {
		return ""
	}
	return a.driver.base.Host
}

func (a *Appium) execute(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	if action == nil {
		return fmt.Errorf("appium action is required")
	}
	if a.session == "" {
		return fmt.Errorf("the appium session is not open")
	}
	commandCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout+a.waitTimeout(action))
	defer cancel()
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "appium_tap":
		return a.onElement(commandCtx, action, func(element string) error {
			return a.driver.elementCommand(commandCtx, a.session, element, "click", map[string]any{}, nil)
		})
	case "appium_type":
		return a.onElement(commandCtx, action, func(element string) error {
			if clear, _ := action.Params["clear"].(bool); clear {
				if err := a.driver.elementCommand(commandCtx, a.session, element, "clear", map[string]any{}, nil); err != nil {
					return err
				}
			}
			return a.driver.elementCommand(commandCtx, a.session, element, "value", map[string]any{"text": action.Value}, nil)
		})
	case "appium_wait":
		var text string
		if err := a.onElement(commandCtx, action, func(element string) error {
			return a.driver.elementCommand(commandCtx, a.session, element, "text", nil, &text)
		}); err != nil {
			return err
		}
		result.ResponseBody = text
		if want, _ := action.Params["text"].(string); want != "" && !strings.Contains(text, want) {
			return fmt.Errorf("element %q shows %q, not %q", action.Target, firstLine(text), want)
		}
		return nil
	case "appium_activate_app", "appium_terminate_app":
		bundle, _ := action.Params["bundle_id"].(string)
		if strings.TrimSpace(bundle) == "" {
			return fmt.Errorf("%s needs params.bundle_id", action.Type)
		}
		script := "mobile: activateApp"
		if strings.EqualFold(action.Type, "appium_terminate_app") {
			script = "mobile: terminateApp"
		}
		// iOS names the app bundleId, Android appId; each ignores the other.
		return a.driver.execute(commandCtx, a.session, script, []any{map[string]any{"bundleId": bundle, "appId": bundle}}, nil)
	case "appium_execute":
		script, _ := action.Params["script"].(string)
		if strings.TrimSpace(script) == "" {
			return fmt.Errorf("appium_execute needs params.script")
		}
		var args []any
		if raw, ok := action.Params["args"]; ok && raw != nil {
			args = []any{raw}
		}
		var out any
		if err := a.driver.execute(commandCtx, a.session, script, args, &out); err != nil {
			return err
		}
		if text, ok := out.(string); ok {
			result.ResponseBody = text
		}
		return nil
	case "appium_screenshot":
		encoded, err := a.driver.screenshot(commandCtx, a.session)
		if err != nil {
			return err
		}
		result.ScreenshotBase64 = encoded
		return nil
	default:
		return fmt.Errorf("unsupported appium action type %q", action.Type)
	}
}

// onElement finds the step's element and acts on it, finding it again once
// when the screen re-rendered in between (a stale element reference).
func (a *Appium) onElement(ctx context.Context, action *protocol.Action, act func(element string) error) error {
	for attempt := 0; ; attempt++ {
		element, err := a.find(ctx, action)
		if err != nil {
			return err
		}
		err = act(element)
		if attempt == 0 && errors.Is(err, errStaleElement) {
			continue
		}
		return err
	}
}

// waitTimeout is how long a step may wait for its element.
func (a *Appium) waitTimeout(action *protocol.Action) time.Duration {
	if ms := protocol.ConfigInt(action.Params, "timeout_ms"); ms > 0 {
		return min(time.Duration(ms)*time.Millisecond, maxElementTimeout)
	}
	return a.elementTimeout
}

// find waits for the step's element: its target, located by params.using
// (accessibility id unless the step names another strategy).
func (a *Appium) find(ctx context.Context, action *protocol.Action) (string, error) {
	if strings.TrimSpace(action.Target) == "" {
		return "", fmt.Errorf("%s needs a target element", action.Type)
	}
	using := defaultLocator
	if named, _ := action.Params["using"].(string); strings.TrimSpace(named) != "" {
		using = strings.ToLower(strings.TrimSpace(named))
	}
	return a.driver.waitForElement(ctx, a.session, using, action.Target, a.waitTimeout(action), elementPollInterval)
}

// Teardown closes the session, so a device cloud frees the device.
func (a *Appium) Teardown(ctx context.Context) error {
	if a.driver == nil || a.session == "" {
		return nil
	}
	session := a.session
	a.session = ""
	teardownCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	if err := a.driver.deleteSession(teardownCtx, session); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("close the appium session: %w", err)
	}
	return nil
}

// HealthCheck asks the Appium server for its status.
func (a *Appium) HealthCheck(ctx context.Context) error {
	if a.driver == nil {
		return fmt.Errorf("appium is not initialized")
	}
	return a.driver.status(ctx)
}
