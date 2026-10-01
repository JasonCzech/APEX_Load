package collectoragent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/jasonczech/apexload/pkg/analysis"
)

// CloudQuery is operator-owned configuration, never supplied by a project job.
// The alias destination binds a job to these exact identities and metric names.
type CloudQuery struct {
	Region     string        `json:"region,omitempty"`
	AccountID  string        `json:"account_id,omitempty"`
	TenantID   string        `json:"tenant_id,omitempty"`
	ClientID   string        `json:"client_id,omitempty"`
	ResourceID string        `json:"resource_id,omitempty"`
	Identity   string        `json:"identity,omitempty"` // workload or managed (Azure)
	Namespace  string        `json:"namespace"`
	Metrics    []CloudMetric `json:"metrics"`
}
type CloudMetric struct {
	Name       string            `json:"name"`
	Unit       string            `json:"unit"`
	Statistic  string            `json:"statistic"` // Average, Minimum, Maximum, Sum
	Dimensions map[string]string `json:"dimensions,omitempty"`
}

var cloudRegion = regexp.MustCompile(`^[a-z]{2}-[a-z]+-[0-9]+$`)
var cloudAccount = regexp.MustCompile(`^[0-9]{12}$`)
var cloudUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validateCloudQuery(pack string, q *CloudQuery) error {
	if q == nil || len(q.Metrics) < 1 || len(q.Metrics) > 8 || strings.TrimSpace(q.Namespace) == "" || len(q.Namespace) > 128 {
		return errors.New("cloud query requires a namespace and 1-8 approved metrics")
	}
	if pack == "cloudwatch" {
		if !cloudRegion.MatchString(q.Region) || strings.HasPrefix(q.Region, "cn-") || !cloudAccount.MatchString(q.AccountID) {
			return errors.New("CloudWatch requires an approved commercial AWS region and account")
		}
	} else {
		parts := strings.Split(q.ResourceID, "/")
		if !cloudUUID.MatchString(q.TenantID) || !cloudUUID.MatchString(q.ClientID) || len(parts) < 9 || parts[1] != "subscriptions" || !cloudUUID.MatchString(parts[2]) || parts[3] != "resourceGroups" || parts[5] != "providers" || strings.ContainsAny(q.ResourceID, "?#%\\") || len(q.ResourceID) > 1024 || (q.Identity != "workload" && q.Identity != "managed") {
			return errors.New("Azure Monitor requires an approved resource and workload or managed identity")
		}
		for _, part := range parts[1:] {
			if part == "" || part == "." || part == ".." {
				return errors.New("invalid Azure resource path")
			}
		}
	}
	seen := map[string]bool{}
	for _, m := range q.Metrics {
		if strings.TrimSpace(m.Name) == "" || len(m.Name) > 128 || strings.ContainsAny(m.Name, ",\r\n") || seen[m.Name] || len(m.Dimensions) > 8 {
			return errors.New("invalid or duplicate cloud metric")
		}
		seen[m.Name] = true
		if m.Statistic != "Average" && m.Statistic != "Minimum" && m.Statistic != "Maximum" && m.Statistic != "Sum" {
			return errors.New("unsupported cloud metric statistic")
		}
		if pack == "cloudwatch" && (m.Unit == "CountPerSecond" || m.Unit == "BytesPerSecond") || pack == "azure-monitor" && (m.Unit == "Count/Second" || m.Unit == "Bytes/Second") {
			return errors.New("cloud unit spelling must match the provider")
		}
		if _, _, err := cloudUnit(m.Unit); err != nil {
			return err
		}
		for k, v := range m.Dimensions {
			if k == "" || v == "" || len(k) > 128 || len(v) > 128 {
				return errors.New("cloud dimension exceeds bounds")
			}
		}
		if pack == "azure-monitor" && len(m.Dimensions) > 0 {
			return errors.New("Azure Monitor dimensional queries are not supported; select a resource total")
		}
	}
	return nil
}
func cloudUnit(unit string) (string, float64, error) {
	switch unit {
	case "Percent":
		return "percent", 1, nil
	case "Count":
		return "count", 1, nil
	case "Bytes":
		return "bytes", 1, nil
	case "Seconds":
		return "ms", 1000, nil
	case "Milliseconds":
		return "ms", 1, nil
	case "Count/Second", "CountPerSecond":
		return "count/s", 1, nil
	case "Bytes/Second", "BytesPerSecond":
		return "bytes/s", 1, nil
	}
	return "", 0, errors.New("unsupported cloud metric unit")
}
func cloudPoint(m CloudMetric, at, start, end time.Time, value *float64) (analysis.MetricPoint, error) {
	unit, scale, err := cloudUnit(m.Unit)
	if err != nil {
		return analysis.MetricPoint{}, err
	}
	if at.Before(start) || !at.Before(end) {
		return analysis.MetricPoint{}, errors.New("cloud timestamp outside approved window")
	}
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, TimestampUTC: at.UTC(), MetricName: m.Name, Unit: unit, Kind: analysis.KindSummary, Quality: analysis.QualityValid, Dimensions: map[string]string{"statistic": m.Statistic, "period_seconds": "60"}}
	if value == nil {
		p.Quality = analysis.QualityMissing
	} else {
		v := *value * scale
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return p, errors.New("non-finite cloud metric")
		}
		p.Value = &v
	}
	return p, nil
}

// Bounded response/retries and no redirects protect process credentials. Provider
// error bodies can contain resource details and are never surfaced as diagnostics.
func cloudRequest(ctx context.Context, client *http.Client, build func() (*http.Request, error)) ([]byte, error) {
	for attempt := 0; attempt < 3; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		req = req.WithContext(ctx)
		resp, err := client.Do(req)
		if err != nil {
			return nil, errors.New("cloud metric request failed")
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		resp.Body.Close()
		if readErr != nil || len(body) > 1<<20 {
			return nil, errors.New("cloud response exceeds limit or could not be read")
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, nil
		}
		if resp.StatusCode != 429 && resp.StatusCode < 500 {
			return nil, fmt.Errorf("cloud metric request rejected (%d)", resp.StatusCode)
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, errors.New("cloud metric request exhausted bounded retries")
}
func collectCloud(ctx context.Context, target AdapterTarget, job RunJob) ([]analysis.MetricPoint, error) {
	if err := validateCloudQuery(target.Pack, target.Cloud); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	q := target.Cloud
	start, end := cloudCollectionWindow(job.StartedAt, time.Now())
	if !start.Before(end) {
		return nil, nil
	}
	if target.Pack == "cloudwatch" {
		return collectCloudWatch(ctx, client, q, start, end)
	}
	return collectAzureMonitor(ctx, client, q, start, end)
}
func cloudCollectionWindow(started, now time.Time) (time.Time, time.Time) {
	end := now.UTC().Truncate(time.Minute)
	start := end.Add(-5 * time.Minute)
	if started.After(start) {
		start = started.UTC().Truncate(time.Minute)
		if start.Before(started) {
			start = start.Add(time.Minute)
		}
	}
	return start, end
}

func collectCloudWatch(ctx context.Context, client *http.Client, q *CloudQuery, start, end time.Time) ([]analysis.MetricPoint, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(q.Region), awsconfig.WithHTTPClient(client))
	if err != nil {
		return nil, errors.New("AWS identity unavailable")
	}
	// Explicit service endpoints prevent environment/profile endpoint overrides.
	ident, err := sts.NewFromConfig(cfg, func(o *sts.Options) { o.BaseEndpoint = aws.String("https://sts." + q.Region + ".amazonaws.com") }).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil || aws.ToString(ident.Account) != q.AccountID {
		return nil, errors.New("AWS collector identity does not match approved account")
	}
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, errors.New("AWS credentials unavailable")
	}
	queries := []any{}
	for i, m := range q.Metrics {
		dims := []any{}
		for name, value := range m.Dimensions {
			dims = append(dims, map[string]string{"Name": name, "Value": value})
		}
		queries = append(queries, map[string]any{"Id": fmt.Sprintf("m%d", i), "MetricStat": map[string]any{"Metric": map[string]any{"Namespace": q.Namespace, "MetricName": m.Name, "Dimensions": dims}, "Period": 60, "Stat": m.Statistic, "Unit": m.Unit}, "ReturnData": true})
	}
	payload, _ := json.Marshal(map[string]any{"StartTime": start.Unix(), "EndTime": end.Unix(), "MetricDataQueries": queries, "MaxDatapoints": 512, "ScanBy": "TimestampAscending"})
	hash := sha256.Sum256(payload)
	body, err := cloudRequest(ctx, client, func() (*http.Request, error) {
		req, _ := http.NewRequest(http.MethodPost, "https://monitoring."+q.Region+".amazonaws.com/", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Encoding", "amz-1.0")
		req.Header.Set("X-Amz-Target", "GraniteServiceVersion20100801.GetMetricData")
		err := v4.NewSigner().SignHTTP(ctx, creds, req, hex.EncodeToString(hash[:]), "monitoring", q.Region, time.Now().UTC())
		return req, err
	})
	if err != nil {
		return nil, err
	}
	return decodeCloudWatch(body, q, start, end)
}
func decodeCloudWatch(body []byte, q *CloudQuery, start, end time.Time) ([]analysis.MetricPoint, error) {
	var data struct {
		NextToken         string
		Messages          []json.RawMessage
		MetricDataResults []struct {
			Id, StatusCode string
			Timestamps     []float64
			Values         []*float64
			Messages       []json.RawMessage
		}
	}
	if json.Unmarshal(body, &data) != nil || data.NextToken != "" || len(data.Messages) > 0 || len(data.MetricDataResults) != len(q.Metrics) {
		return nil, errors.New("incomplete CloudWatch response")
	}
	points := []analysis.MetricPoint{}
	seen := map[string]bool{}
	for _, result := range data.MetricDataResults {
		index := -1
		for i := range q.Metrics {
			if result.Id == fmt.Sprintf("m%d", i) {
				index = i
			}
		}
		if index < 0 || seen[result.Id] || result.StatusCode != "Complete" || len(result.Messages) > 0 || len(result.Timestamps) != len(result.Values) || len(result.Values) > 5 {
			return nil, errors.New("invalid CloudWatch metric response")
		}
		seen[result.Id] = true
		times := map[float64]bool{}
		for i, ts := range result.Timestamps {
			if times[ts] {
				return nil, errors.New("duplicate CloudWatch timestamp")
			}
			times[ts] = true
			if math.IsNaN(ts) || math.IsInf(ts, 0) || ts != math.Trunc(ts) {
				return nil, errors.New("invalid CloudWatch timestamp")
			}
			p, err := cloudPoint(q.Metrics[index], time.Unix(int64(ts), 0).UTC(), start, end, result.Values[i])
			if err != nil {
				return nil, err
			}
			points = append(points, p)
		}
	}
	return points, nil
}
func collectAzureMonitor(ctx context.Context, client *http.Client, q *CloudQuery, start, end time.Time) ([]analysis.MetricPoint, error) {
	var token string
	opts := policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}}
	if q.Identity == "workload" {
		cred, err := azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{TenantID: q.TenantID, ClientID: q.ClientID})
		if err != nil {
			return nil, errors.New("Azure workload identity unavailable")
		}
		t, err := cred.GetToken(ctx, opts)
		if err != nil {
			return nil, errors.New("Azure workload identity authentication failed")
		}
		token = t.Token
	} else {
		cred, err := azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{ID: azidentity.ClientID(q.ClientID)})
		if err != nil {
			return nil, errors.New("Azure managed identity unavailable")
		}
		t, err := cred.GetToken(ctx, opts)
		if err != nil {
			return nil, errors.New("Azure managed identity authentication failed")
		}
		token = t.Token
	}
	points := []analysis.MetricPoint{}
	for _, m := range q.Metrics {
		aggregation := m.Statistic
		if aggregation == "Sum" {
			aggregation = "Total"
		}
		query := url.Values{"api-version": {"2023-10-01"}, "metricnamespace": {q.Namespace}, "metricnames": {m.Name}, "timespan": {start.Format(time.RFC3339) + "/" + end.Format(time.RFC3339)}, "interval": {"PT1M"}, "aggregation": {aggregation}, "top": {"1"}, "AutoAdjustTimegrain": {"false"}}
		endpoint := "https://management.azure.com" + q.ResourceID + "/providers/Microsoft.Insights/metrics?" + query.Encode()
		body, err := cloudRequest(ctx, client, func() (*http.Request, error) {
			req, err := http.NewRequest(http.MethodGet, endpoint, nil)
			if err == nil {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			return req, err
		})
		if err != nil {
			return nil, err
		}
		batch, err := decodeAzureMonitor(body, m, start, end)
		if err != nil {
			return nil, err
		}
		points = append(points, batch...)
	}
	return points, nil
}
func decodeAzureMonitor(body []byte, m CloudMetric, start, end time.Time) ([]analysis.MetricPoint, error) {
	var data struct {
		Interval string `json:"interval"`
		Value    []struct {
			Name struct {
				Value string `json:"value"`
			} `json:"name"`
			Unit       string `json:"unit"`
			ErrorCode  string `json:"errorCode"`
			Timeseries []struct {
				Metadata []json.RawMessage `json:"metadatavalues"`
				Data     []struct {
					Timestamp                        time.Time `json:"timeStamp"`
					Average, Minimum, Maximum, Total *float64
				}
			}
		}
	}
	if json.Unmarshal(body, &data) != nil || data.Interval != "PT1M" || len(data.Value) != 1 {
		return nil, errors.New("invalid Azure metric response")
	}
	result := data.Value[0]
	if result.Name.Value != m.Name || result.Unit != m.Unit || (result.ErrorCode != "" && result.ErrorCode != "Success") || len(result.Timeseries) > 1 {
		return nil, errors.New("Azure metric identity, unit or completeness mismatch")
	}
	points := []analysis.MetricPoint{}
	for _, series := range result.Timeseries {
		if len(series.Metadata) > 0 || len(series.Data) > 6 {
			return nil, errors.New("Azure dimensional or oversized result rejected")
		}
		times := map[time.Time]bool{}
		for _, row := range series.Data {
			if times[row.Timestamp.UTC()] {
				return nil, errors.New("duplicate Azure timestamp")
			}
			times[row.Timestamp.UTC()] = true
			if row.Timestamp.Equal(end) {
				continue
			}
			var value *float64
			switch m.Statistic {
			case "Average":
				value = row.Average
			case "Minimum":
				value = row.Minimum
			case "Maximum":
				value = row.Maximum
			case "Sum":
				value = row.Total
			}
			p, err := cloudPoint(m, row.Timestamp, start, end, value)
			if err != nil {
				return nil, err
			}
			points = append(points, p)
		}
	}
	return points, nil
}
