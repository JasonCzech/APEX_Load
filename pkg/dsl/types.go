package dsl

type Script struct {
	Name        string       `json:"name"`
	Version     string       `json:"version"`
	Protocol    string       `json:"protocol"`
	GeneratedBy string       `json:"generated_by,omitempty"`
	GeneratedFrom string    `json:"generated_from,omitempty"`
	Config      ScriptConfig `json:"config"`
	DataSources []DataSource `json:"data_sources,omitempty"`
	Init        []Action     `json:"init,omitempty"`
	Actions     []Action     `json:"actions"`
	Teardown    []Action     `json:"teardown,omitempty"`
}

type ScriptConfig struct {
	BaseURL   string         `json:"base_url"`
	ThinkTime ThinkTimeConfig `json:"think_time,omitempty"`
	Pacing    PacingConfig   `json:"pacing,omitempty"`
}

type ThinkTimeConfig struct {
	Type   string  `json:"type"`
	MeanMs float64 `json:"mean_ms"`
	StdMs  float64 `json:"std_ms,omitempty"`
	MinMs  float64 `json:"min_ms,omitempty"`
	MaxMs  float64 `json:"max_ms,omitempty"`
}

type PacingConfig struct {
	Type       string `json:"type"`
	IntervalMs int    `json:"interval_ms,omitempty"`
	TPS        float64 `json:"tps,omitempty"`
}

type DataSource struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Path     string `json:"path"`
	Strategy string `json:"strategy"`
}

type Action struct {
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Method         string            `json:"method,omitempty"`
	Target         string            `json:"target"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           string            `json:"body,omitempty"`
	Params         map[string]any    `json:"params,omitempty"`
	Extractors     []Extractor       `json:"extractors,omitempty"`
	Assertions     []Assertion       `json:"assertions,omitempty"`
	ThinkTime      ThinkTimeConfig   `json:"think_time,omitempty"`
	ThinkTimeAfter bool              `json:"think_time_after,omitempty"`
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
