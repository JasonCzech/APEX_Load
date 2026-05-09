package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

type VUser struct {
	ID        int
	Group     string
	Proto     protocol.Protocol
	Script    *dsl.Script
	Metrics   *MetricsCollector
	Variables map[string]string
}

func (vu *VUser) Run(ctx context.Context) {
	logger := slog.With("vuser", vu.ID, "group", vu.Group)

	vu.Variables = make(map[string]string)

	// Init phase
	for _, action := range vu.Script.Init {
		if ctx.Err() != nil {
			return
		}
		resolved := vu.resolveAction(action)
		result, err := vu.Proto.Execute(ctx, resolved)
		if err != nil {
			logger.Error("init action failed", "action", action.Name, "err", err)
			return
		}
		result.Group = vu.Group
		vu.Metrics.Record(result)
		vu.mergeExtractions(result)
		if !result.Success {
			logger.Warn("init action unsuccessful", "action", action.Name, "error", result.ErrorMessage)
			return
		}
	}

	// Main loop
	for {
		for _, action := range vu.Script.Actions {
			if ctx.Err() != nil {
				goto teardown
			}

			resolved := vu.resolveAction(action)
			result, err := vu.Proto.Execute(ctx, resolved)
			if err != nil {
				logger.Error("action error", "action", action.Name, "err", err)
				continue
			}
			result.Group = vu.Group
			vu.Metrics.Record(result)
			vu.mergeExtractions(result)

			if action.ThinkTimeAfter {
				thinkCfg := action.ThinkTime
				if thinkCfg.Type == "" {
					thinkCfg = vu.Script.Config.ThinkTime
				}
				applyThinkTime(protocol.ThinkTimeConfig{
					Type:   thinkCfg.Type,
					MeanMs: thinkCfg.MeanMs,
					StdMs:  thinkCfg.StdMs,
					MinMs:  thinkCfg.MinMs,
					MaxMs:  thinkCfg.MaxMs,
				})
			}
		}
	}

teardown:
	for _, action := range vu.Script.Teardown {
		resolved := vu.resolveAction(action)
		result, _ := vu.Proto.Execute(context.Background(), resolved)
		if result != nil {
			vu.Metrics.Record(result)
		}
	}
}

func (vu *VUser) resolveAction(action dsl.Action) *protocol.Action {
	return &protocol.Action{
		Name:       action.Name,
		Type:       action.Type,
		Method:     action.Method,
		Target:     vu.interpolate(action.Target),
		Headers:    vu.interpolateMap(action.Headers),
		Body:       []byte(vu.interpolate(action.Body)),
		Params:     action.Params,
		Extractors: toProtocolExtractors(action.Extractors),
		Assertions: toProtocolAssertions(action.Assertions),
	}
}

func (vu *VUser) interpolate(s string) string {
	result := s
	for k, v := range vu.Variables {
		result = replaceAll(result, fmt.Sprintf("{{var.%s}}", k), v)
	}
	return result
}

func (vu *VUser) interpolateMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = vu.interpolate(v)
	}
	return out
}

func (vu *VUser) mergeExtractions(r *protocol.Result) {
	for k, v := range r.Extractions {
		vu.Variables[k] = v
	}
}

func replaceAll(s, old, new string) string {
	for {
		i := indexOf(s, old)
		if i < 0 {
			return s
		}
		s = s[:i] + new + s[i+len(old):]
	}
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func toProtocolExtractors(exts []dsl.Extractor) []protocol.Extractor {
	out := make([]protocol.Extractor, len(exts))
	for i, e := range exts {
		out[i] = protocol.Extractor{
			Name:       e.Name,
			Source:     e.Source,
			Type:       e.Type,
			Expression: e.Expression,
		}
	}
	return out
}

func toProtocolAssertions(asserts []dsl.Assertion) []protocol.Assertion {
	out := make([]protocol.Assertion, len(asserts))
	for i, a := range asserts {
		out[i] = protocol.Assertion{
			Type:     a.Type,
			Expected: a.Expected,
			Value:    a.Value,
			Max:      a.Max,
		}
	}
	return out
}
