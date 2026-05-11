package browser

import (
	"context"
	"fmt"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

const scaffoldError = "browser protocol scaffold is registered but real browser execution is not wired yet"

func init() {
	protocol.Register("browser", func() protocol.Protocol {
		return &Browser{}
	})
}

type Browser struct {
	baseURL    string
	currentURL string
	headless   bool
}

func (b *Browser) Name() string { return "browser" }

func (b *Browser) Init(_ context.Context, config map[string]any) error {
	if baseURL, ok := config["base_url"].(string); ok {
		b.baseURL = baseURL
	}
	if headless, ok := config["browser_headless"].(bool); ok {
		b.headless = headless
	}
	return nil
}

func (b *Browser) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	result := &protocol.Result{
		ActionName:  action.Name,
		Extractions: map[string]string{},
	}

	switch action.Type {
	case "browser_navigate":
		if action.Target == "" {
			return result, fmt.Errorf("browser_navigate requires target")
		}
		b.currentURL = b.resolveURL(action.Target)
		result.PageURL = b.currentURL
	case "browser_wait", "browser_screenshot", "browser_click", "browser_fill":
		result.PageURL = b.currentURL
	default:
		return result, fmt.Errorf("unsupported browser action type: %s", action.Type)
	}

	result.Success = false
	result.ErrorMessage = scaffoldError
	return result, nil
}

func (b *Browser) Teardown(_ context.Context) error {
	b.currentURL = ""
	return nil
}

func (b *Browser) HealthCheck(_ context.Context) error {
	if b.baseURL == "" && b.currentURL == "" {
		return fmt.Errorf("browser protocol requires base_url or absolute navigation target")
	}
	return nil
}

func (b *Browser) resolveURL(target string) string {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	if b.baseURL == "" {
		return target
	}
	return strings.TrimRight(b.baseURL, "/") + "/" + strings.TrimLeft(target, "/")
}