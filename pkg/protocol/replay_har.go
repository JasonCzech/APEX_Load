package protocol

import (
	"fmt"
	"mime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const ReplayHARCaptureConfigKey = "apexload_internal_replay_har_capture"

// ReplayHAROptions applies only to a new interactive debug run. Empty bodies
// selects none; zero limits use 64 KiB per body and 4 MiB across the run.
type ReplayHAROptions struct {
	Bodies            string `json:"bodies"`
	Redirects         bool   `json:"redirects"`
	Timings           bool   `json:"timings"`
	MaxBodyBytes      int    `json:"max_body_bytes"`
	MaxTotalBodyBytes int    `json:"max_total_body_bytes"`
}

func (o ReplayHAROptions) Normalize() (ReplayHAROptions, error) {
	if o.Bodies == "" {
		o.Bodies = "none"
	}
	switch o.Bodies {
	case "none", "request", "response", "both":
	default:
		return o, fmt.Errorf("HAR bodies must be none, request, response, or both")
	}
	if o.MaxBodyBytes == 0 {
		o.MaxBodyBytes = 64 << 10
	}
	if o.MaxTotalBodyBytes == 0 {
		o.MaxTotalBodyBytes = 4 << 20
	}
	if o.MaxBodyBytes < 1 || o.MaxBodyBytes > 1<<20 {
		return o, fmt.Errorf("HAR per-body limit must be between 1 and 1048576 bytes")
	}
	if o.MaxTotalBodyBytes < 1 || o.MaxTotalBodyBytes > 16<<20 {
		return o, fmt.Errorf("HAR total body limit must be between 1 and 16777216 bytes")
	}
	return o, nil
}
func (o ReplayHAROptions) KeepBody(request bool) bool {
	return o.Bodies == "both" || request && o.Bodies == "request" || !request && o.Bodies == "response"
}
func (o *ReplayHAROptions) Clone() *ReplayHAROptions {
	if o == nil {
		return nil
	}
	c := *o
	return &c
}

type ReplayHARBody struct {
	SizeBytes *int64 `json:"size_bytes,omitempty"`
	Text      string `json:"text,omitempty"`
	State     string `json:"state"` // retained, size_limit, total_limit, binary, unavailable, redacted
	MimeType  string `json:"mime_type,omitempty"`
}
type ReplayHARExchange struct {
	ID           string                 `json:"id,omitempty"`
	StartedAt    time.Time              `json:"started_at"`
	Phases       []BrowserResourcePhase `json:"phases,omitempty"`
	RequestBody  *ReplayHARBody         `json:"request_body,omitempty"`
	ResponseBody *ReplayHARBody         `json:"response_body,omitempty"`
}

func (e *ReplayHARExchange) Clone() *ReplayHARExchange {
	if e == nil {
		return nil
	}
	c := *e
	if len(c.ID) > 128 {
		c.ID = ""
	}
	c.Phases = append([]BrowserResourcePhase(nil), e.Phases[:min(len(e.Phases), 6)]...)
	for i := range c.Phases {
		if c.Phases[i].StartMS != nil {
			v := *c.Phases[i].StartMS
			c.Phases[i].StartMS = &v
		}
		if c.Phases[i].EndMS != nil {
			v := *c.Phases[i].EndMS
			c.Phases[i].EndMS = &v
		}
	}
	if e.RequestBody != nil {
		v := *e.RequestBody
		c.RequestBody = boundReplayHARBody(v)
	}
	if e.ResponseBody != nil {
		v := *e.ResponseBody
		c.ResponseBody = boundReplayHARBody(v)
	}
	return &c
}
func (e *ReplayHARExchange) BodyBytes() int {
	if e == nil {
		return 0
	}
	n := 0
	if e.RequestBody != nil {
		n += len(e.RequestBody.Text)
	}
	if e.ResponseBody != nil {
		n += len(e.ResponseBody.Text)
	}
	return n
}

// ReplayHARCapture shares a body budget across HTTP policy clones and browser
// contexts, so navigation or a version change cannot reset a run's allowance.
type ReplayHARCapture struct {
	Options ReplayHAROptions
	mu      sync.Mutex
	used    int
}

var replayHARSequence atomic.Uint64

func NewReplayHARCapture(options ReplayHAROptions) (*ReplayHARCapture, error) {
	o, err := options.Normalize()
	if err != nil {
		return nil, err
	}
	return &ReplayHARCapture{Options: o}, nil
}
func ReplayHARCaptureFromConfig(config map[string]any) *ReplayHARCapture {
	c, _ := config[ReplayHARCaptureConfigKey].(*ReplayHARCapture)
	return c
}
func NewReplayHARExchange() *ReplayHARExchange {
	return &ReplayHARExchange{ID: fmt.Sprint(replayHARSequence.Add(1)), StartedAt: time.Now().UTC()}
}
func HARTextMIME(value string) bool {
	t, _, _ := mime.ParseMediaType(value)
	t = strings.ToLower(t)
	return strings.HasPrefix(t, "text/") || strings.Contains(t, "json") || strings.Contains(t, "xml") || t == "application/x-www-form-urlencoded" || t == "application/javascript"
}
func (c *ReplayHARCapture) Body(data []byte, mimeType string, complete bool) *ReplayHARBody {
	cleanMIME, _, _ := mime.ParseMediaType(mimeType)
	if len(cleanMIME) > 128 {
		cleanMIME = ""
	}
	b := &ReplayHARBody{MimeType: cleanMIME, State: "retained"}
	switch {
	case !complete:
		b.State = "unavailable"
	case len(data) > c.Options.MaxBodyBytes:
		b.State = "size_limit"
	case !utf8.Valid(data) || len(data) > 0 && !HARTextMIME(mimeType):
		b.State = "binary"
	default:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.used+len(data) > c.Options.MaxTotalBodyBytes {
			b.State = "total_limit"
		} else {
			c.used += len(data)
			b.Text = string(data)
			size := int64(len(data))
			b.SizeBytes = &size
		}
	}
	return b
}

func boundReplayHARBody(b ReplayHARBody) *ReplayHARBody {
	if b.SizeBytes != nil {
		size := *b.SizeBytes
		if size < 0 || size > 1<<20 {
			b.SizeBytes = nil
		} else {
			b.SizeBytes = &size
		}
	}
	if len(b.MimeType) > 128 {
		b.MimeType = ""
	}
	if b.State != "retained" {
		b.Text = ""
	}
	if len(b.Text) > 1<<20 {
		b.Text = ""
		b.State = "size_limit"
	}
	return &b
}
