package recordingprofile

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
)

func TestValidate(t *testing.T) {
	valid := []Profile{{ID: "default", Name: "Default", ResourceMode: "in_step", HTTPScriptLevel: "html", Correlation: CorrelationSettings{
		UserRules: []UserRule{{Name: "OrderID", Type: "boundary", Left: "order=", Right: "&"}, {Name: "Ticket", Type: "regex", Regex: `ticket-(\d+)`}},
	}}}
	if err := Validate(valid); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		profiles []Profile
		want     string
	}{
		"bad id":           {[]Profile{{ID: "has space", Name: "x"}}, "id must be"},
		"duplicate id":     {[]Profile{{ID: "a", Name: "x"}, {ID: "A", Name: "y"}}, "duplicated"},
		"no name":          {[]Profile{{ID: "a"}}, "name must be"},
		"level":            {[]Profile{{ID: "a", Name: "x", HTTPScriptLevel: "page"}}, "http_script_level"},
		"resources":        {[]Profile{{ID: "a", Name: "x", ResourceMode: "inline"}}, "resource_mode"},
		"threshold":        {[]Profile{{ID: "a", Name: "x", ThinkTimeThresholdMs: -1}}, "think_time_threshold_ms"},
		"lengths":          {[]Profile{{ID: "a", Name: "x", Correlation: CorrelationSettings{MinValueLength: 10, MaxValueLength: 5}}}, "value lengths"},
		"rule name":        {[]Profile{{ID: "a", Name: "x", Correlation: CorrelationSettings{UserRules: []UserRule{{Name: "1bad", Type: "regex", Regex: "(x)"}}}}}, "parameter name"},
		"boundary":         {[]Profile{{ID: "a", Name: "x", Correlation: CorrelationSettings{UserRules: []UserRule{{Name: "a", Type: "boundary", Left: "x"}}}}}, "left and a right"},
		"regex group":      {[]Profile{{ID: "a", Name: "x", Correlation: CorrelationSettings{UserRules: []UserRule{{Name: "a", Type: "regex", Regex: "x+"}}}}}, "capture the value"},
		"rule type":        {[]Profile{{ID: "a", Name: "x", Correlation: CorrelationSettings{UserRules: []UserRule{{Name: "a", Type: "xpath"}}}}}, "boundary or regex"},
		"empty list entry": {[]Profile{{ID: "a", Name: "x", DropHeaders: []string{" "}}}, "entries must be 1-4096 characters"},
		"rule hosts":       {[]Profile{{ID: "a", Name: "x", Correlation: CorrelationSettings{UserRules: []UserRule{{Name: "a", Type: "regex", Regex: "(x)", Hosts: []string{""}}}}}}, "hosts entries must be"},
		"rule boundary":    {[]Profile{{ID: "a", Name: "x", Correlation: CorrelationSettings{UserRules: []UserRule{{Name: "a", Type: "boundary", Left: strings.Repeat("x", 5000), Right: "y"}}}}}, "at most 4096 characters"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Validate(tc.profiles); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestProfileDecisions(t *testing.T) {
	profile := Profile{
		RecordHeaders: []string{"User-Agent"}, DropHeaders: []string{"X-Debug"},
		IncludeHosts: []string{"*.shop.test"}, ExcludeHosts: []string{"cdn.shop.test"},
		ExcludeContentTypes: []string{"image/*", "font/woff2"},
	}
	if keep, decided := profile.HeaderDecision("user-agent"); !keep || !decided {
		t.Fatal("record list")
	}
	if keep, decided := profile.HeaderDecision("X-DEBUG"); keep || !decided {
		t.Fatal("drop list")
	}
	if _, decided := profile.HeaderDecision("Accept"); decided {
		t.Fatal("unlisted headers keep the recorder default")
	}
	for host, want := range map[string]bool{"www.shop.test": true, "shop.test:8443": true, "cdn.shop.test": false, "ads.test": false} {
		if got := profile.RecordsHost(host); got != want {
			t.Errorf("RecordsHost(%s) = %v", host, got)
		}
	}
	ipv6 := Profile{IncludeHosts: []string{"::1"}}
	for host, want := range map[string]bool{"[::1]:8080": true, "[::1]": true, "::2": false} {
		if got := ipv6.RecordsHost(host); got != want {
			t.Errorf("IPv6 RecordsHost(%s) = %v", host, got)
		}
	}
	for contentType, want := range map[string]bool{"image/png": false, "font/woff2": false, "text/html; charset=utf-8": true, "": true} {
		if got := profile.RecordsContentType(contentType); got != want {
			t.Errorf("RecordsContentType(%q) = %v", contentType, got)
		}
	}
	if (Profile{}).ThinkTimeThreshold() != 500 || (Profile{ThinkTimeThresholdMs: 2000}).ThinkTimeThreshold() != 2000 {
		t.Fatal("think-time threshold")
	}
}

func TestApplyCorrelation(t *testing.T) {
	observations := []correlate.Observation{
		{ID: 1, RequestURL: "https://shop.test/start", ResponseBody: `<a href="/order?order=A9F3K2&x=1">`, ResponseHeaders: map[string]string{"Content-Type": "text/html"}},
		{ID: 2, RequestURL: "https://shop.test/order?order=A9F3K2", ResponseBody: `ticket-77881`, ResponseHeaders: map[string]string{"Content-Type": "text/plain"}},
		{ID: 3, RequestURL: "https://shop.test/ticket/77881"},
	}
	detected := []correlate.Correlation{
		{Name: "csrf", Value: "longcsrfvalue", RuleID: correlate.RuleJSONCSRFToken, FoundInID: 1},
		{Name: "short", Value: "ab", RuleID: correlate.RuleJSONDynamicID, FoundInID: 1},
		{Name: "cookie", Value: "cookievalue1", RuleID: correlate.RuleSetCookieSession, FoundInID: 1},
		{Name: "same", Value: "A9F3K2", RuleID: correlate.RuleJSONDynamicID, FoundInID: 1},
		{Name: "excluded", Value: "static-value", RuleID: correlate.RuleJSONDynamicID, FoundInID: 1},
		{Name: "plain", Value: "plaintextvalue", RuleID: correlate.RuleJSONDynamicID, FoundInID: 2},
	}
	settings := CorrelationSettings{
		DisabledRules: []string{"cookie"}, ExcludedValues: []string{"static-value"}, ExcludedContentTypes: []string{"text/plain"},
		UserRules: []UserRule{
			{Name: "OrderID", Type: "boundary", Left: "order=", Right: "&"},
			{Name: "Ticket", Type: "regex", Regex: `ticket-(\d+)`, ContentTypes: []string{"text/plain"}},
			{Name: "Unused", Type: "regex", Regex: `nothing-(\d+)`},
			{Name: "OtherHost", Type: "regex", Regex: `ticket-(\d+)`, Hosts: []string{"other.test"}},
		},
	}
	got := settings.ApplyCorrelation(observations, detected)
	names := map[string]correlate.Correlation{}
	for _, correlation := range got {
		names[correlation.Name] = correlation
	}
	if len(got) != 3 || names["csrf"].Value == "" || names["OrderID"].Value != "A9F3K2" || names["Ticket"].Value != "77881" {
		t.Fatalf("correlations = %+v", got)
	}
	order := names["OrderID"]
	if order.ExtractType != "boundary" || order.Expression != "order=|||&" || order.FoundInID != 1 || len(order.UsedInIDs) != 1 || order.UsedInIDs[0] != 2 || order.RuleID != "user.OrderID" {
		t.Fatalf("user boundary rule = %+v", order)
	}
	if ticket := names["Ticket"]; ticket.ExtractType != "regex" || ticket.Expression != `ticket-(\d+)` {
		t.Fatalf("user regex rule = %+v", ticket)
	}
	regexSettings := CorrelationSettings{CaseInsensitive: true, PreferRegex: true, UserRules: []UserRule{{Name: "OrderID", Type: "boundary", Left: "ORDER=", Right: "&"}}}
	if got := regexSettings.ApplyCorrelation(observations, nil); len(got) != 1 || got[0].ExtractType != "regex" || got[0].Expression != `(?i)ORDER=(.+?)&` {
		t.Fatalf("prefer regex, case-insensitive = %+v", got)
	}
	if got := (CorrelationSettings{Disabled: true}).ApplyCorrelation(observations, detected); len(got) != 0 {
		t.Fatalf("a disabled scan keeps no automatic correlations: %+v", got)
	}
}

func TestBackgroundRequestPatterns(t *testing.T) {
	var none *Profile
	if got := none.BackgroundRequestPatterns(); strings.Join(got, ",") != strings.Join(DefaultBackgroundRequests, ",") {
		t.Fatalf("no profile = %v, want the defaults", got)
	}
	if got := (&Profile{}).BackgroundRequestPatterns(); len(got) != len(DefaultBackgroundRequests) {
		t.Fatalf("unset = %v, want the defaults", got)
	}
	empty := []string{}
	if got := (&Profile{BackgroundRequests: &empty}).BackgroundRequestPatterns(); len(got) != 0 {
		t.Fatalf("empty list = %v, want none", got)
	}
	own := []string{"telemetry/events"}
	if got := (&Profile{BackgroundRequests: &own}).BackgroundRequestPatterns(); len(got) != 1 || got[0] != "telemetry/events" {
		t.Fatalf("own list = %v", got)
	}
}

func TestMatchBackgroundRequest(t *testing.T) {
	for rawURL, want := range map[string]string{
		"https://acme.my.salesforce.com/services/data/v66.0/connect/proxy/app-analytics-logging": "connect/proxy/app-analytics-logging",
		"/services/apexrest/mobile/NebulaLogger?x=1":                                             "nebulaLogger",
		"{{base_url}}/services/apexrest/mobile/analyticsEvent":                                   "analyticsEvent",
		"/services/apexrest/mobile/analyticsEvents":                                              "",
		"/services/apexrest/mobile/login":                                                        "",
		"/proxy/app-analytics-logging":                                                           "",
		"/services/apexrest/mobile/search?log=nebulaLogger":                                      "",
	} {
		if got := MatchBackgroundRequest(DefaultBackgroundRequests, rawURL); got != want {
			t.Errorf("MatchBackgroundRequest(%q) = %q, want %q", rawURL, got, want)
		}
	}
	if got := MatchBackgroundRequest(nil, "/services/apexrest/mobile/nebulaLogger"); got != "" {
		t.Fatalf("no patterns matched %q", got)
	}
}

func TestValidateBackgroundRequests(t *testing.T) {
	for _, patterns := range [][]string{{""}, {"/"}, {"logs?x=1"}, {"https://host/log"}, {"a*b"}, {"two words"}, {"nebula\nLogger"}, {"tab\tbed"}} {
		list := patterns
		err := Validate([]Profile{{ID: "a", Name: "x", BackgroundRequests: &list}})
		if err == nil || !strings.Contains(err.Error(), "background_requests") {
			t.Fatalf("%q: error = %v", patterns, err)
		}
	}
	good := []string{"nebulaLogger", "/connect/proxy/app-analytics-logging/"}
	if err := Validate([]Profile{{ID: "a", Name: "x", BackgroundRequests: &good}}); err != nil {
		t.Fatal(err)
	}
}
