package engine

import (
	"context"
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"strconv"
	"time"
)

func (vu *VUser) dispatchWebSocketCallbacks(ctx context.Context, wait bool) error {
	if vu.inWebSocketCallback {
		return nil
	}
	source, ok := vu.Proto.(protocol.MessageCallbackSource)
	if !ok {
		return nil
	}
	messages, err := source.DrainMessageCallbacks(ctx, wait)
	if err != nil {
		// This failure occurs before the next authored action executes. It must
		// still reach metrics/evidence rather than only stopping the VUser.
		cfg := &vu.Script.Config
		vu.recordResult(&protocol.Result{ActionName: "WebSocket callback dispatch", StartTime: time.Now(), Success: false, ErrorMessage: err.Error(), DependencyType: "websocket"}, cfg.ErrorHandling, cfg.Log)
		return err
	}
	vu.inWebSocketCallback = true
	defer func() { vu.inWebSocketCallback = false }()
	for _, message := range messages {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if source.CallbackGeneration() != message.Generation {
			break
		}
		vu.Variables["ws_message"] = message.Body
		vu.Variables["ws_message_type"] = strconv.Itoa(message.MessageType)
		// Callback ingress is measured once, separately from authored child actions.
		vu.recordResult(&protocol.Result{ActionName: "WebSocket callback message", StartTime: callbackReceivedAt(message.ReceivedAt), Success: true, BytesReceived: message.BytesReceived, DependencyType: "websocket", ServerTelemetry: &protocol.ServerTelemetry{MessagesReceived: 1, WebSocket: message.Diagnostics.Clone()}}, vu.Script.Config.ErrorHandling, vu.Script.Config.Log)
		cfg := &vu.Script.Config
		if !vu.runLifecycleActions(ctx, vu.webSocketCallbackSteps, "WebSocket callback", false, vu.makeLogger(cfg.Log), cfg.Log, cfg.ErrorHandling) {
			return fmt.Errorf("WebSocket callback failed")
		}
	}
	return nil
}

func (vu *VUser) beforeWebSocketAction(ctx context.Context, kind string) error {
	// Background HTTP conversation results are applied at the same action
	// boundaries as WebSocket callbacks.
	if err := vu.dispatchAsyncResults(ctx); err != nil {
		return err
	}
	if kind == "ws_on_message" || kind == "ws_connect" || kind == "ws_close" {
		return nil
	}
	return vu.dispatchWebSocketCallbacks(ctx, kind == "ws_dispatch")
}

func (vu *VUser) acceptWebSocketCallback(action dsl.Action, result *protocol.Result, err error) {
	if action.Type == "ws_on_message" && action.Control != nil && err == nil && result != nil && result.Success {
		vu.webSocketCallbackSteps = action.Control.Steps
	}
}

func callbackReceivedAt(at time.Time) time.Time {
	if at.IsZero() {
		return time.Now()
	}
	return at
}
