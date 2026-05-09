package protocol

import (
	"context"
	"time"
)

type Protocol interface {
	Name() string
	Init(ctx context.Context, config map[string]any) error
	Execute(ctx context.Context, action *Action) (*Result, error)
	Teardown(ctx context.Context) error
	HealthCheck(ctx context.Context) error
}

type Action struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Method     string            `json:"method,omitempty"`
	Target     string            `json:"target"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       []byte            `json:"body,omitempty"`
	Params     map[string]any    `json:"params,omitempty"`
	Extractors []Extractor       `json:"extractors,omitempty"`
	Assertions []Assertion       `json:"assertions,omitempty"`
	ThinkTime  ThinkTimeConfig   `json:"think_time,omitempty"`
}

type Extractor struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	Type       string `json:"type"`
	Expression string `json:"expression"`
}

type Assertion struct {
	Type     string `json:"type"`
	Expected any    `json:"expected,omitempty"`
	Value    string `json:"value,omitempty"`
	Max      int    `json:"max,omitempty"`
}

type ThinkTimeConfig struct {
	Type   string  `json:"type"`
	MeanMs float64 `json:"mean_ms"`
	StdMs  float64 `json:"std_ms,omitempty"`
	MinMs  float64 `json:"min_ms,omitempty"`
	MaxMs  float64 `json:"max_ms,omitempty"`
}

type Result struct {
	ActionName    string
	Group         string
	StartTime     time.Time
	Duration      time.Duration
	DNSTime       time.Duration
	ConnectTime   time.Duration
	TLSTime       time.Duration
	TTFB          time.Duration
	BytesSent     int64
	BytesReceived int64
	StatusCode    int
	Success       bool
	ErrorMessage  string
	Extractions   map[string]string
}
