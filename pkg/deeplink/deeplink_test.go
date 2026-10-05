package deeplink

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestValidateRejectsUnsafeTemplates(t *testing.T) {
	hosts := []string{"abc123.live.dynatrace.com", "kibana.example.com:5601"}
	for name, template := range map[string]string{
		"host mismatch":                 "https://evil.example.com/trace/{trace_id}",
		"non-https scheme":              "http://abc123.live.dynatrace.com/trace/{trace_id}",
		"javascript scheme":             "javascript:alert({trace_id})",
		"placeholder in the host":       "https://{trace_id}.live.dynatrace.com/",
		"placeholder ends the host":     "https://abc123.live.dynatrace.com{trace_id}",
		"user information":              "https://user@abc123.live.dynatrace.com/{trace_id}",
		"unknown placeholder":           "https://abc123.live.dynatrace.com/{token}",
		"unmatched brace":               "https://abc123.live.dynatrace.com/{trace_id",
		"stray close brace":             "https://abc123.live.dynatrace.com/}{trace_id}",
		"port must match":               "https://kibana.example.com/app/{trace_id}",
		"whitespace":                    "https://abc123.live.dynatrace.com/ {trace_id}",
		"relative link":                 "/ui/{trace_id}",
		"placeholder before the scheme": "{trace_id}https://abc123.live.dynatrace.com/",
	} {
		if Validate(template, hosts) == nil {
			t.Errorf("%s: %q accepted", name, template)
		}
	}
	for _, template := range []string{
		"https://abc123.live.dynatrace.com/ui/apps/dynatrace.distributedtracing/explorer?traceId={trace_id}",
		"https://kibana.example.com:5601/app/discover#/?_g=(time:(from:'{from_utc}',to:'{to_utc}'))&_a=(query:(language:kuery,query:'%22{trace_id}%22'))",
		"https://ABC123.live.dynatrace.com/#newservices/serviceOverview;id={entity_id}",
	} {
		if err := Validate(template, hosts); err != nil {
			t.Errorf("%q rejected: %v", template, err)
		}
	}
}

func TestExpandRejectsInjectedValues(t *testing.T) {
	hosts := []string{"abc123.live.dynatrace.com"}
	template := "https://abc123.live.dynatrace.com/ui/explorer/{entity_id}/view?traceId={trace_id}&from={from_utc}"
	from := time.Date(2026, 10, 5, 12, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	compiled, err := Compile(template, hosts)
	if err != nil {
		t.Fatal(err)
	}
	for _, injected := range []string{
		"x&tenant=other",
		"x#fragment",
		"../../admin",
		"..",
		".",
		"@evil.example.com",
		"x?redirect=https://evil.example.com",
		"<script>alert(1)</script>",
		"x'),index:'other",
		strings.Repeat("a", 300),
	} {
		if link, err := compiled.Expand(Values{TraceID: injected, EntityID: "SERVICE-1", From: from}); err == nil {
			t.Errorf("trace ID %q expanded to %s", injected, link)
		}
		if link, err := compiled.Expand(Values{TraceID: "abc", EntityID: injected, From: from}); err == nil {
			t.Errorf("entity ID %q expanded to %s", injected, link)
		}
	}
	link, err := compiled.Expand(Values{TraceID: "0af7651916cd43dd8448eb211c80319c", EntityID: "SERVICE-1A.b_c~", From: from})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil || u.Host != "abc123.live.dynatrace.com" || u.Path != "/ui/explorer/SERVICE-1A.b_c~/view" || u.Fragment != "" {
		t.Fatalf("identifiers changed the URL structure: %s", link)
	}
	if query := u.Query(); query.Get("traceId") != "0af7651916cd43dd8448eb211c80319c" || len(query) != 2 || query.Get("from") != "2026-10-05T10:00:00Z" || !strings.Contains(u.RawQuery, "from=2026-10-05T10%3A00%3A00Z") {
		t.Fatalf("values did not round-trip escaped: %s", link)
	}
	if _, err := compiled.Expand(Values{TraceID: "abc", EntityID: "SERVICE-1"}); err == nil || !strings.Contains(err.Error(), "from_utc") {
		t.Fatalf("a missing value expanded: %v", err)
	}
	long := "https://abc123.live.dynatrace.com/" + strings.Repeat("p", MaxTemplateBytes-60) + "/{trace_id}/{entity_id}"
	if _, err := Expand(long, Values{TraceID: strings.Repeat("a", 200), EntityID: "x"}, hosts); err == nil {
		t.Fatal("an oversized link expanded")
	}
}

func TestCheckTargetAndOriginHosts(t *testing.T) {
	hosts := OriginHosts("https://abc123.live.dynatrace.com", "http://plain.example.com:8080/base", "", "::bad")
	if len(hosts) != 2 || hosts[1] != "plain.example.com:8080" {
		t.Fatalf("origin hosts %v", hosts)
	}
	if CheckTarget("https://ABC123.live.dynatrace.com/#problems", hosts) != nil || CheckTarget("https://plain.example.com:8080/x", hosts) != nil {
		t.Fatal("a link on a connection host was rejected")
	}
	for _, link := range []string{"http://abc123.live.dynatrace.com/", "https://plain.example.com/x", "https://u@abc123.live.dynatrace.com/", "https:///x", "https://abc123.live.dynatrace.com/" + strings.Repeat("a", MaxTemplateBytes)} {
		if CheckTarget(link, hosts) == nil {
			t.Errorf("%.80s accepted", link)
		}
	}
}

func TestHostOf(t *testing.T) {
	if HostOf("https://kibana.example.com:5601/base") != "kibana.example.com:5601" || HostOf("http://plain.example.com") != "" || HostOf("https://u@x.example.com") != "" {
		t.Fatal("HostOf accepted a non-https or credentialed origin")
	}
}
