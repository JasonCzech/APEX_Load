package kafka

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// schemaRegistryClient is a minimal Confluent Schema Registry client: it
// registers a schema under a subject (POST /subjects/{subject}/versions) and
// caches the assigned id so repeated produces don't re-register. It satisfies
// schemaIDResolver.
type schemaRegistryClient struct {
	baseURL    string
	schema     string
	schemaType string // JSON | AVRO | PROTOBUF
	httpClient *http.Client

	mu    sync.Mutex
	cache map[string]int32 // subject -> id
}

func newSchemaRegistryClient(baseURL, schema, schemaType string) *schemaRegistryClient {
	return &schemaRegistryClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		schema:     schema,
		schemaType: schemaType,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		cache:      make(map[string]int32),
	}
}

func (r *schemaRegistryClient) schemaID(ctx context.Context, subject string) (int32, error) {
	r.mu.Lock()
	if id, ok := r.cache[subject]; ok {
		r.mu.Unlock()
		return id, nil
	}
	r.mu.Unlock()

	payload := map[string]string{"schema": r.schema}
	if r.schemaType != "" && r.schemaType != "AVRO" {
		// The registry defaults schemaType to AVRO; only send it otherwise.
		payload["schemaType"] = r.schemaType
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	url := fmt.Sprintf("%s/subjects/%s/versions", r.baseURL, subject)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/vnd.schemaregistry.v1+json")
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("schema registry register %q: %w", subject, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("schema registry register %q: unexpected status %d", subject, resp.StatusCode)
	}
	var out struct {
		ID int32 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("schema registry register %q: decode response: %w", subject, err)
	}
	r.mu.Lock()
	r.cache[subject] = out.ID
	r.mu.Unlock()
	return out.ID, nil
}
