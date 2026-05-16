package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/stormrunner/pkg/api"
	"github.com/jasonczech/stormrunner/pkg/engine"
)

type ApiClient struct {
	BaseURL string
	Client  *http.Client
}

func NewApiClient(baseURL string) *ApiClient {
	return &ApiClient{
		BaseURL: baseURL,
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Result mimics engine.TestResult for formatting
type ApiTestResult struct {
	Duration          time.Duration
	TotalTransactions int64
	TotalErrors       int64
	FinalSnapshot     engine.MetricsSnapshot
	SLASummary        *SLASummary
}

func (c *ApiClient) RunTest(scriptPath string, vusers int, duration string, rampUp string, format string, goalConfig *api.GoalConfig) (*ApiTestResult, error) {
	file, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("read script: %w", err)
	}
	return c.RunPreparedTest(scriptPath, file, vusers, duration, rampUp, format, goalConfig)
}

func (c *ApiClient) RunPreparedTest(scriptName string, scriptBody []byte, vusers int, duration string, rampUp string, format string, goalConfig *api.GoalConfig) (*ApiTestResult, error) {
	// 1. Upload script
	scriptID, err := c.uploadScript(scriptName, scriptBody)
	if err != nil {
		return nil, fmt.Errorf("upload script: %w", err)
	}

	// 2. Create Test
	testID, err := c.createTest(scriptID, vusers, duration, rampUp, goalConfig)
	if err != nil {
		return nil, fmt.Errorf("create test: %w", err)
	}

	if format != "json" {
		fmt.Printf("Created test %s from script %s. Starting...\n", testID, scriptID)
	}

	// 3. Start Test
	err = c.startTest(testID)
	if err != nil {
		return nil, fmt.Errorf("start test: %w", err)
	}

	// 4. Poll Test Status
	return c.pollTest(testID, format, goalConfig != nil)
}

func (c *ApiClient) uploadScript(scriptName string, scriptBody []byte) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(scriptName))
	if err != nil {
		return "", err
	}
	if _, err := part.Write(scriptBody); err != nil {
		return "", err
	}
	writer.Close()

	req, _ := http.NewRequest("POST", c.BaseURL+"/api/v1/scripts", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status: %s", resp.Status)
	}

	var m map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&m)
	return m["id"].(string), nil
}

func (c *ApiClient) createTest(scriptID string, vusers int, duration, rampUp string, goalConfig *api.GoalConfig) (string, error) {
	reqBody := api.CreateTestRequest{
		Name:     "CLI Run " + time.Now().Format("2006-01-02 15:04:05"),
		Duration: duration,
		ScriptID: scriptID,
		RampUp:   rampUp,
		VUsers:   vusers,
	}
	if goalConfig != nil {
		reqBody.ScenarioMode = "goal_oriented"
		reqBody.GoalConfig = goalConfig
	}

	b, _ := json.Marshal(reqBody)
	resp, err := c.Client.Post(c.BaseURL+"/api/v1/tests", "application/json", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("bad status: %s, %s", resp.Status, string(b))
	}

	var test api.ManagedTest
	json.NewDecoder(resp.Body).Decode(&test)
	return test.ID, nil
}

func (c *ApiClient) startTest(testID string) error {
	resp, err := c.Client.Post(c.BaseURL+"/api/v1/tests/"+testID+"/start", "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}
	return nil
}

func (c *ApiClient) pollTest(testID string, format string, expectSLA bool) (*ApiTestResult, error) {
	start := time.Now()
	for {
		time.Sleep(2 * time.Second)

		resp, err := c.Client.Get(c.BaseURL + "/api/v1/tests/" + testID)
		if err != nil {
			return nil, err
		}

		var test api.ManagedTest
		json.NewDecoder(resp.Body).Decode(&test)
		resp.Body.Close()

		if format != "json" {
			fmt.Printf("... Status: %s\n", test.Status)
		}

		if test.Status == "COMPLETE" || test.Status == "FAILED" || test.Status == "ABORTED" {
			res := &ApiTestResult{
				Duration: time.Since(start),
			}
			if test.LiveMetrics != nil {
				res.TotalTransactions = test.LiveMetrics.TotalTx
				res.TotalErrors = test.LiveMetrics.TotalErrors
				res.FinalSnapshot = engine.MetricsSnapshot{
					Transactions: test.LiveMetrics.TotalTx,
					TPS:          test.LiveMetrics.TPS,
				}
			}
			if expectSLA {
				sla, err := c.GetSLAStatus(testID)
				if err != nil {
					return res, fmt.Errorf("fetch SLA status: %w", err)
				}
				if sla != nil {
					res.SLASummary = &SLASummary{
						Evaluated: true,
						Breached:  sla.SLABreached,
						Details:   append([]string(nil), sla.Details...),
					}
					if sla.SLABreached {
						return res, fmt.Errorf("SLA breached: %s", strings.Join(sla.Details, "; "))
					}
				}
			}
			if test.Status == "FAILED" || test.Status == "ABORTED" {
				return res, fmt.Errorf("test completed with status %s", strings.ToLower(test.Status))
			}
			return res, nil
		}
	}
}

func (c *ApiClient) GetSLAStatus(testID string) (*api.SLABreachReport, error) {
	resp, err := c.Client.Get(c.BaseURL + "/api/v1/tests/" + testID + "/sla-status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var report api.SLABreachReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		return nil, err
	}
	return &report, nil
}
