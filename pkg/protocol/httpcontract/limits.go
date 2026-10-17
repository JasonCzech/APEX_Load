package httpcontract

import "time"

const (
	DefaultMaxConnsPerHost       = 100
	DefaultMaxIdleConns          = 4
	DefaultExpectContinueTimeout = time.Second
	DefaultConnectTimeout        = 30 * time.Second
	DefaultTLSTimeout            = 10 * time.Second
	DefaultIdleTimeout           = 90 * time.Second
	DefaultRequestTimeout        = 60 * time.Second
	DefaultKeepAlive             = 30 * time.Second
	DefaultTLSMinVersion         = "1.2"
	DefaultMaxRedirects          = 10
	DefaultCookiesEnabled        = true
	DefaultKeepAliveEnabled      = true
	MaxConfiguredConnsPerHost    = 256
	MaxConfiguredIdleConns       = 32
	MaxConfiguredTimeout         = time.Hour
	MaxConfiguredRedirects       = 50
	MaxConfiguredNetworkKbps     = 100_000_000
	MaxConfiguredStreamKbps      = 100_000_000
	MaxConfiguredStreamBuffer    = time.Hour
	MaxActionAssertions          = 256
	MaxAssertionRegexBytes       = 4 << 10
	MaxPageResources             = 256
	MaxExtractorExpressionBytes  = 64 << 10
	MaxExtractorDFEs             = 8
	MaxCacheUnusedIterations     = 1_000_000
	HTMLStepParam                = "html_step"
)

var ExtractorDFETypes = map[string]bool{
	"base64": true, "gzip": true, "json_to_xml": true, "xml_to_json": true,
	"url_decode": true, "url_encode": true, "html_unescape": true, "html_escape": true,
}

var AuraRecordedFailurePolicies = []string{"", "require_success", "accept_recorded"}
