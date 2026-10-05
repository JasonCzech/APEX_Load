package prometheus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func readinessQueries(count int) Selection {
	selection := Selection{ProjectID: "project"}
	for index := 0; index < count; index++ {
		id := fmt.Sprintf("query_%02d", index)
		selection.Queries = append(selection.Queries, Query{ID: id, Name: id, Expression: "vector(1)"})
	}
	return selection
}

func TestSelectedReadinessUsesInspectionBudgetForTwentyPlainQueries(t *testing.T) {
	calls := 0
	client := fixture(t, Config{RequestsPerMinute: 30, MaxPollSeconds: 1}, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Path != "/api/v1/query" {
			t.Fatalf("unselected capability consumed an inspection request: %s", request.URL.Path)
		}
		deadline, exists := request.Context().Deadline()
		if !exists || time.Until(deadline) < 5*time.Second || time.Until(deadline) > 10*time.Second {
			t.Fatalf("HTTP request lost its independent ten-second deadline: %v", deadline)
		}
		return success(map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{}, "value": []any{time.Now().Unix(), "1"}}}}), nil
	})
	inspection, cancel, err := client.selectedInspectionContext(context.Background(), readinessQueries(20))
	if err != nil {
		t.Fatal(err)
	}
	deadline, exists := inspection.Deadline()
	cancel()
	if !exists || time.Until(deadline) < 45*time.Second || time.Until(deadline) > time.Minute {
		t.Fatalf("selected inspection inherited runtime poll deadline: %v", deadline)
	}
	capabilities, err := client.TestSelection(context.Background(), readinessQueries(20))
	if err != nil || !capabilities.Metrics || calls != 20 || capabilities.Discovery || capabilities.Targets || capabilities.Alerts || capabilities.Status["discovery"] != "not_selected" || capabilities.Status["targets"] != "not_selected" || capabilities.Status["alerts"] != "not_selected" {
		t.Fatalf("twenty approved queries could not complete inspection: %+v, calls=%d, err=%v", capabilities, calls, err)
	}
}

func TestSelectedReadinessRejectsImpossibleBudgetsBeforeNetwork(t *testing.T) {
	for _, mode := range []string{"requests", "quota", "companions", "parent_deadline"} {
		t.Run(mode, func(t *testing.T) {
			config := Config{RequestsPerMinute: 30}
			selection := readinessQueries(20)
			ctx := context.Background()
			cancel := func() {}
			switch mode {
			case "requests":
				config.MaxRequestsPerPoll = 10
			case "quota":
				config.RequestsPerMinute = 5
			case "companions":
				for index := range selection.Queries {
					selection.Queries[index].Expression = "up"
					selection.Queries[index].FreshnessExpression = "timestamp(up)"
					selection.Queries[index].TerminalExpression = "sum_over_time(up[{{window.duration}}])"
				}
			case "parent_deadline":
				ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
			}
			defer cancel()
			calls := 0
			client := fixture(t, config, func(*http.Request) (*http.Response, error) {
				calls++
				return success(map[string]any{}), nil
			})
			capabilities, err := client.TestSelection(ctx, selection)
			if !errors.Is(err, ErrBudget) || capabilities.Metrics || capabilities.Status["metrics"] != "budget_exhausted" || calls != 0 {
				t.Fatalf("impossible selected inspection was not explicit: %+v, calls=%d, err=%v", capabilities, calls, err)
			}
		})
	}
}

func TestSelectedReadinessProbesOnlySelectedScopedDiagnostics(t *testing.T) {
	for _, mode := range []string{"success", "empty_scope", "target_error", "target_truncation"} {
		t.Run(mode, func(t *testing.T) {
			paths := []string{}
			client := fixture(t, Config{}, func(request *http.Request) (*http.Response, error) {
				paths = append(paths, request.URL.Path)
				switch request.URL.Path {
				case "/api/v1/query":
					return success(map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{}, "value": []any{time.Now().Unix(), "1"}}}}), nil
				case "/api/v1/targets":
					if mode == "target_error" {
						response := success(map[string]any{})
						response.StatusCode = http.StatusForbidden
						return response, nil
					}
					targets := []any{map[string]any{"labels": map[string]string{"job": "generator"}, "health": "up"}}
					if mode != "empty_scope" {
						count := 1
						if mode == "target_truncation" {
							count = 101
						}
						for index := 0; index < count; index++ {
							targets = append(targets, map[string]any{"labels": map[string]string{"job": "sut", "instance": fmt.Sprint(index)}, "health": "up"})
						}
					}
					return success(map[string]any{"activeTargets": targets}), nil
				case "/api/v1/alerts":
					alerts := []any{map[string]any{"labels": map[string]string{"job": "generator", "alertname": "Unrelated"}, "state": "firing"}}
					if mode != "empty_scope" {
						alerts = append(alerts, map[string]any{"labels": map[string]string{"job": "sut", "alertname": "Target"}, "state": "pending"})
					}
					return success(map[string]any{"alerts": alerts}), nil
				default:
					t.Fatalf("unselected capability: %s", request.URL.Path)
					return nil, errors.New("unexpected capability")
				}
			})
			selection := readinessQueries(1)
			selection.Targets, selection.Alerts = true, true
			selection.EvidenceSelector = `{job="sut"}`
			capabilities, err := client.TestSelection(context.Background(), selection)
			if mode == "target_error" {
				if err == nil || capabilities.Targets || capabilities.Status["targets"] != "permission_denied" || len(paths) != 2 {
					t.Fatalf("selected target permission failure ignored: %+v, paths=%v, err=%v", capabilities, paths, err)
				}
				return
			}
			if err != nil || !capabilities.Metrics || !capabilities.Alerts || len(paths) != 3 {
				t.Fatalf("selected diagnostics not probed: %+v, paths=%v, err=%v", capabilities, paths, err)
			}
			if mode == "target_truncation" {
				if capabilities.Targets || capabilities.Status["targets"] != "partial" {
					t.Fatalf("truncated selected target population passed readiness: %+v", capabilities)
				}
			} else if !capabilities.Targets {
				t.Fatalf("selected target capability unavailable: %+v", capabilities)
			}
			if mode == "empty_scope" && (capabilities.Status["targets"] != "no_data" || capabilities.Status["alerts"] != "no_data") {
				t.Fatalf("unrelated diagnostics satisfied selected scope: %+v", capabilities)
			}
		})
	}
}

func TestConnectionReadinessRetainsFullCapabilityChecks(t *testing.T) {
	paths := []string{}
	client := fixture(t, Config{}, func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		switch request.URL.Path {
		case "/api/v1/query":
			return success(map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{}, "value": []any{time.Now().Unix(), "1"}}}}), nil
		case "/api/v1/label/__name__/values":
			return success([]string{"up"}), nil
		case "/api/v1/status/buildinfo":
			return success(map[string]string{"version": "3.13.0"}), nil
		case "/api/v1/targets":
			return success(map[string]any{"activeTargets": []any{}}), nil
		case "/api/v1/alerts":
			return success(map[string]any{"alerts": []any{}}), nil
		default:
			t.Fatalf("unexpected connection probe: %s", request.URL.Path)
			return nil, errors.New("unexpected capability")
		}
	})
	capabilities, err := client.Test(context.Background())
	if err != nil || !capabilities.Metrics || !capabilities.Discovery || !capabilities.Targets || !capabilities.Alerts || capabilities.Version != "3.13.0" || len(paths) != 5 {
		t.Fatalf("connection verification lost independent capabilities: %+v, paths=%v, err=%v", capabilities, paths, err)
	}
}
