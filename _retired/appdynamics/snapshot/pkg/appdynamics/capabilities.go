package appdynamics

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"
)

func (c *Client) Test(ctx context.Context) (Capabilities, error) {
	return c.testSelection(ctx, nil, nil)
}

// TestApplications probes the full connection capability set using one locally
// approved application. Analytics remains restricted to its verified name.
// Source readiness uses TestSelection to verify its explicitly selected scope.
func (c *Client) TestApplications(ctx context.Context, applications []string) (Capabilities, error) {
	if err := (Selection{ProjectID: "connection-probe", Applications: applications}).Validate(); err != nil {
		return Capabilities{}, err
	}
	return c.testSelection(ctx, nil, applications[:1])
}

// TestSelection probes only selected capabilities, using the approved metric
// paths and Analytics definitions rather than a generic unrelated application.
func (c *Client) TestSelection(ctx context.Context, selection Selection) (Capabilities, error) {
	if err := selection.Validate(); err != nil {
		return Capabilities{}, err
	}
	return c.testSelection(ctx, &selection, nil)
}

func (c *Client) testSelection(ctx context.Context, selection *Selection, approvedApplications []string) (Capabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.cfg.MaxPollSeconds)*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, budgetKey{}, &pollBudget{remaining: c.cfg.MaxRequestsPerPoll})
	ctx = withAnalyticsApplicationCache(ctx)
	caps := Capabilities{Status: map[string]string{}}
	application := ""
	if selection != nil {
		application = selection.Applications[0]
	} else if len(approvedApplications) > 0 {
		application = approvedApplications[0]
	}
	apps, discoveryErr := c.Discover(ctx, application, "applications", "")
	caps.Status["controller"] = capabilityStatus(discoveryErr)
	caps.Status["discovery"] = capabilityStatus(discoveryErr)
	var resultErr error
	applications := append([]string(nil), approvedApplications...)
	if selection != nil {
		applications = append(applications, selection.Applications...)
	}
	if discoveryErr != nil {
		resultErr = discoveryErr
		caps.Status["applications"] = capabilityStatus(discoveryErr)
	} else {
		if selection == nil && len(applications) == 0 && len(apps) > 0 {
			applications = []string{apps[0].ID}
		}
		if len(apps) == 0 {
			caps.Status["applications"] = "empty"
			resultErr = errors.New("AppDynamics returned no visible applications; permissions and scope cannot be verified")
		} else {
			caps.Status["applications"] = "available"
		}
	}
	// A metric permission denial does not prevent reporting independently
	// working events, snapshots or separately authenticated Analytics.
	metricSelections := []MetricSelection{}
	if selection != nil {
		// Metric read permissions are scoped by application. Verify one
		// approved selection per application; every path remains independently
		// bounded and its actual evidence coverage is recorded during collection.
		seenApplications := map[string]bool{}
		for _, metric := range selection.Metrics {
			if !seenApplications[metric.ApplicationID] {
				metricSelections = append(metricSelections, metric)
				seenApplications[metric.ApplicationID] = true
			}
		}
	} else if len(applications) > 0 {
		metricSelections = []MetricSelection{{ApplicationID: applications[0], Path: "Overall Application Performance|Calls per Minute"}}
	}
	caps.Status["metrics"] = "not_selected"
	if len(metricSelections) > 0 {
		caps.Metrics = true
		caps.Status["metrics"] = "available_no_data"
		for _, metric := range metricSelections {
			_, catalogErr := c.Discover(ctx, metric.ApplicationID, "metrics", "")
			if catalogErr != nil {
				caps.Status["discovery"] = capabilityStatus(catalogErr)
			}
			rows, _, err := c.metrics(ctx, metric, time.Now().Add(-5*time.Minute), time.Now().Add(-2*time.Minute), 0)
			if err != nil {
				caps.Metrics = false
				caps.Status["metrics"] = capabilityStatus(err)
				if selection != nil && resultErr == nil {
					resultErr = err
				}
			} else if caps.Metrics && len(rows) > 0 {
				caps.Status["metrics"] = "available"
			}
		}
		c.capMu.Lock()
		caps.MetricAPI = c.capabilities.MetricAPI
		c.capMu.Unlock()
	}
	for _, kind := range []string{"events", "violations", "snapshots"} {
		enabled := selection == nil || kind == "events" && selection.Events || kind == "violations" && selection.Violations || kind == "snapshots" && selection.Snapshots
		caps.Status[kind] = "not_selected"
		if !enabled {
			continue
		}
		available := len(applications) > 0
		caps.Status[kind] = "unavailable"
		for _, app := range applications {
			approved := Selection{}
			if selection != nil {
				approved = *selection
			}
			rows, _, err := c.diagnostics(ctx, app, kind, time.Now().Add(-time.Minute), time.Now(), approved, 0)
			if err != nil {
				available = false
				caps.Status[kind] = capabilityStatus(err)
				if selection != nil && resultErr == nil {
					resultErr = err
				}
			} else if available {
				caps.Status[kind] = "available_no_data"
				if len(rows) > 0 {
					caps.Status[kind] = "available"
				}
			}
		}
		switch kind {
		case "events":
			caps.Events = available
		case "violations":
			caps.Violations = available
		case "snapshots":
			caps.Snapshots = available
		}
	}
	// Events Service credentials and licensing are checked independently.
	caps.Status["analytics"] = "not_configured"
	if selection != nil && len(selection.Analytics) == 0 {
		caps.Status["analytics"] = "not_selected"
	} else if c.cfg.EventsEndpoint != "" && c.cfg.EventsAccountName != "" && c.credentials.EventsAPIKey != "" {
		queries := []AnalyticsQuery{{Query: "SELECT COUNT(*) AS value FROM transactions"}}
		if selection != nil {
			queries = selection.Analytics
		} else if len(approvedApplications) > 0 {
			queries[0].ApplicationID = approvedApplications[0]
			for _, app := range apps {
				if app.ID == approvedApplications[0] {
					queries[0].ApplicationName = app.Name
					break
				}
			}
		}
		caps.Analytics = true
		for _, q := range queries {
			var response []analyticsResponse
			params := url.Values{"start": {strconv.FormatInt(time.Now().Add(-time.Minute).UnixMilli(), 10)}, "end": {strconv.FormatInt(time.Now().UnixMilli(), 10)}, "limit": {"1"}}
			query := q.Query
			var err error
			if selection != nil {
				if err = c.validateAnalyticsApplication(ctx, q); err == nil {
					query, err = CompileAnalyticsQuery(q, "apex-preparation-probe", selection.RunHeader)
				}
			} else if len(approvedApplications) > 0 {
				if q.ApplicationName == "" || discoveryErr != nil {
					err = errors.New("approved Analytics application scope could not be verified")
				} else {
					query, err = CompileAnalyticsQuery(q, "", false)
				}
			}
			if err == nil {
				err = c.query(ctx, "POST", "/events/query", params, []byte(query), true, &response)
			}
			if err != nil {
				caps.Analytics = false
				caps.Status["analytics"] = capabilityStatus(err)
				if selection != nil && resultErr == nil {
					resultErr = err
				}
			} else if caps.Analytics {
				caps.Status["analytics"] = "available_no_data"
				for _, rows := range response {
					if len(rows.Results) > 0 {
						caps.Status["analytics"] = "available"
					}
				}
			}
		}
	} else if selection != nil && len(selection.Analytics) > 0 && resultErr == nil {
		resultErr = errors.New("Analytics credentials and endpoint are required")
	}
	c.capMu.Lock()
	c.capabilities = cloneCapabilities(caps)
	c.capMu.Unlock()
	return caps, resultErr
}
