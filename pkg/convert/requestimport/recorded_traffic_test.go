package requestimport

import "testing"

func TestIntegratedAuthScheme(t *testing.T) {
	for value, want := range map[string]string{
		"NTLM TlRMTVNT": "ntlm", "ntlm abc": "ntlm", "Negotiate YIIG": "negotiate", "Basic abc": "", "Bearer x": "", "": "",
	} {
		if got := IntegratedAuthScheme(value); got != want {
			t.Errorf("IntegratedAuthScheme(%q) = %q, want %q", value, got, want)
		}
	}
	if !IsIntegratedAuthChallenge(401, []string{"Basic realm=x\nNTLM"}) {
		t.Fatal("a newline-joined challenge list containing NTLM is an integrated challenge")
	}
	if IsIntegratedAuthChallenge(401, []string{"Basic realm=x"}) || IsIntegratedAuthChallenge(200, []string{"NTLM"}) {
		t.Fatal("only 401 NTLM/Negotiate challenges qualify")
	}
}

func TestCookieOriginsLeaveJarCookiesToTheJar(t *testing.T) {
	origins := NewCookieOrigins()
	origins.Observe("shop.test:443", []string{"SESSIONID=a; Path=/", "lang=en; Domain=.shop.test"})
	origins.Observe("other.test", []string{"tracking=1"})

	remaining, removed := origins.WithoutJarCookies("shop.test", "consent=yes; SESSIONID=a; lang=en; tracking=1")
	if remaining != "consent=yes; tracking=1" || removed != 2 {
		t.Fatalf("remaining = %q removed = %d", remaining, removed)
	}
	remaining, removed = origins.WithoutJarCookies("api.shop.test", "lang=en; SESSIONID=a")
	if remaining != "SESSIONID=a" || removed != 1 {
		t.Fatalf("a Domain= cookie covers subdomains, a host-only cookie does not: remaining = %q removed = %d", remaining, removed)
	}
	remaining, removed = origins.WithoutJarCookies("shop.test", "SESSIONID=a")
	if remaining != "" || removed != 1 {
		t.Fatalf("remaining = %q removed = %d", remaining, removed)
	}
}

func TestRecordedTrafficFindingsAreExecutionGates(t *testing.T) {
	for _, finding := range []struct {
		code string
		gate string
	}{
		{IntegratedAuthFinding("har", "entry-1", "ntlm").Code, string(IntegratedAuthFinding("har", "entry-1", "ntlm").Gate)},
		{RecordedBodyTruncatedFinding("recording", "request-2", 10).Code, string(RecordedBodyTruncatedFinding("recording", "request-2", 10).Gate)},
	} {
		if finding.gate != "execution" {
			t.Fatalf("%s gate = %q, want execution", finding.code, finding.gate)
		}
	}
}
