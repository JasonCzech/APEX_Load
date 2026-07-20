package jms

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// resolveJMSProvider keeps the registered protocol name and configured provider
// on one immutable adapter lane.
func resolveJMSProvider(protocolName, runtimeProvider, configuredProvider string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(protocolName))
	if name == "jms" {
		return "", fmt.Errorf("generic JMS protocol %q is not executable; use jms-solace, jms-ibmmq, or jms-servicebus", protocolName)
	}

	expected := ""
	switch name {
	case "jms-solace":
		expected = "solace"
	case "jms-ibmmq":
		expected = "ibmmq"
	case "jms-servicebus":
		expected = "servicebus"
	default:
		return "", fmt.Errorf("JMS protocol %q is not supported; use jms-solace, jms-ibmmq, or jms-servicebus", protocolName)
	}

	validate := func(source, provider string) error {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if provider == "" {
			return nil
		}
		switch provider {
		case "solace", "ibmmq", "servicebus":
		default:
			return fmt.Errorf("%s %q is not supported; use solace, ibmmq, or servicebus", source, provider)
		}
		if provider != expected {
			return fmt.Errorf("%s %q does not match protocol %q; expected %q", source, provider, name, expected)
		}
		return nil
	}
	if err := validate("JMS runtime provider", runtimeProvider); err != nil {
		return "", err
	}
	if err := validate("jms_provider", configuredProvider); err != nil {
		return "", err
	}
	return expected, nil
}

// selectAdapter chooses a non-mock provider adapter. The sidecar command comes
// only from operator configuration; user-authored scripts cannot supply it.
func (j *JMS) selectAdapter() (brokerAdapter, error) {
	if scriptCommand := strings.TrimSpace(j.cfg.Properties["sidecar_command"]); scriptCommand != "" {
		return nil, fmt.Errorf("script-supplied sidecar_command is not allowed; configure %s on the runner", sidecarCommandEnv)
	}

	switch strings.ToLower(strings.TrimSpace(j.provider)) {
	case "ibmmq":
		return nil, fmt.Errorf("real IBM MQ adapter is not available in this build; enable mock mode for local replay")
	case "servicebus":
		return newServiceBusAdapter(j.cfg), nil
	case "solace":
	default:
		return nil, fmt.Errorf("JMS provider %q is not supported; use solace, ibmmq, or servicebus", j.provider)
	}

	if strings.EqualFold(strings.TrimSpace(j.cfg.Properties["adapter"]), "sidecar") {
		if len(j.allowedTargetHosts) > 0 {
			return nil, fmt.Errorf("JMS sidecar adapter cannot enforce the CI runtime target policy")
		}
		operatorCommand := strings.TrimSpace(os.Getenv(sidecarCommandEnv))
		if operatorCommand == "" {
			return nil, fmt.Errorf("jms adapter=sidecar requires the operator-owned %s to be set on the runner", sidecarCommandEnv)
		}
		return sidecarAdapter{command: operatorCommand, cfg: j.cfg}, nil
	}

	return newSolaceAdapter(j.cfg), nil
}

type unavailableSolaceAdapter struct {
	cfg config
}

func (a unavailableSolaceAdapter) Publish(context.Context, string, message) (message, error) {
	return message{}, a.err()
}

func (a unavailableSolaceAdapter) Consume(context.Context, string, time.Duration) (message, error) {
	return message{}, a.err()
}

func (a unavailableSolaceAdapter) RequestReply(context.Context, string, string, message, time.Duration) (message, message, error) {
	return message{}, message{}, a.err()
}

func (a unavailableSolaceAdapter) HealthCheck(context.Context) error {
	if a.cfg.BrokerURL == "" {
		return fmt.Errorf("jms broker_url is not configured")
	}
	return a.err()
}

func (a unavailableSolaceAdapter) Teardown(context.Context) error { return nil }

func (a unavailableSolaceAdapter) err() error {
	return fmt.Errorf("real Solace JMS adapter is not available in this build; enable mock mode for local replay")
}

type unavailableServiceBusAdapter struct {
	cfg config
}

func (a unavailableServiceBusAdapter) Publish(context.Context, string, message) (message, error) {
	return message{}, a.err()
}

func (a unavailableServiceBusAdapter) Consume(context.Context, string, time.Duration) (message, error) {
	return message{}, a.err()
}

func (a unavailableServiceBusAdapter) RequestReply(context.Context, string, string, message, time.Duration) (message, message, error) {
	return message{}, message{}, a.err()
}

func (a unavailableServiceBusAdapter) HealthCheck(context.Context) error {
	if a.cfg.BrokerURL == "" {
		return fmt.Errorf("jms broker_url is not configured")
	}
	return a.err()
}

func (a unavailableServiceBusAdapter) Teardown(context.Context) error { return nil }

func (a unavailableServiceBusAdapter) err() error {
	return fmt.Errorf("real Azure Service Bus adapter is not available in this build; enable mock mode or build with -tags jms_servicebus_real")
}
