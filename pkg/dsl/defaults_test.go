package dsl

import "testing"

func TestDefaultScriptConfig(t *testing.T) {
	c := DefaultScriptConfig()
	if c.HTTP.ConnectTimeoutMs != 30000 || c.HTTP.RequestTimeoutMs != 60000 || c.HTTP.TLSTimeoutMs != 10000 || c.HTTP.IdleTimeoutMs != 90000 {
		t.Fatalf("http timeout defaults wrong: %+v", c.HTTP)
	}
	if c.HTTP.KeepAlive == nil || !*c.HTTP.KeepAlive {
		t.Fatal("keep_alive default should be true")
	}
	if c.HTTP.MaxConnsPerHost != 100 || c.HTTP.MaxIdleConns != 4 {
		t.Fatalf("http connection defaults wrong: %+v", c.HTTP)
	}
	if c.HTTP.MaxRedirects == nil || *c.HTTP.MaxRedirects != 10 {
		t.Fatal("max_redirects default should be 10")
	}
	if c.HTTP.TLS.MinVersion != "1.2" {
		t.Fatalf("tls min_version default: %q", c.HTTP.TLS.MinVersion)
	}
	if c.Browser.Engine != "chromium" || c.Browser.Headless == nil || !*c.Browser.Headless {
		t.Fatalf("browser defaults: %+v", c.Browser)
	}
	if c.Browser.Viewport.Width != 1440 || c.Browser.Viewport.Height != 900 {
		t.Fatalf("viewport default: %+v", c.Browser.Viewport)
	}
	if c.Pacing.Type != "none" {
		t.Fatalf("pacing default type: %q", c.Pacing.Type)
	}
	if c.RunLogic.WorkloadModel != "closed" || c.RunLogic.AutoTransactions != "none" {
		t.Fatalf("run_logic defaults: %+v", c.RunLogic)
	}
	if c.ErrorHandling.RetryDelayMs != 1000 || c.ErrorHandling.RetryBackoff != "fixed" {
		t.Fatalf("error_handling defaults: %+v", c.ErrorHandling)
	}
	if c.Log.Level != "standard" {
		t.Fatalf("log level default: %q", c.Log.Level)
	}
	if c.Log.RedactRunEvidence == nil || *c.Log.RedactRunEvidence {
		t.Fatalf("default redact_run_evidence = %v, want explicit false", c.Log.RedactRunEvidence)
	}
}
