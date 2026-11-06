package jmeter

import (
	"strings"
	"testing"
)

func authPortPlan(authURL, protocol, port string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <AuthManager testname="Auth" enabled="true">
          <collectionProp name="AuthManager.auth_list">
            <elementProp name="" elementType="Authorization">
              <stringProp name="Authorization.url">` + authURL + `</stringProp>
              <stringProp name="Authorization.username">fixture-user</stringProp>
              <stringProp name="Authorization.password">fixture-pass</stringProp>
              <stringProp name="Authorization.mechanism">BASIC</stringProp>
            </elementProp>
          </collectionProp>
        </AuthManager>
        <hashTree/>
        <HTTPSamplerProxy testname="Home" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">` + protocol + `</stringProp>
          <stringProp name="HTTPSampler.port">` + port + `</stringProp>
          <stringProp name="HTTPSampler.path">/home</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`
}

// JMeter's AuthManager matches an Authorization URL with or without the
// scheme's default port, so dropping :443/:80 from the sampler URL must not
// lose the credential.
func TestAuthorizationURLMatchesWithOrWithoutDefaultPort(t *testing.T) {
	for _, tc := range []struct {
		name, authURL, protocol, port string
		want                          bool
	}{
		{"https_auth_443_sampler_443", "https://api.example.com:443/", "https", "443", true},
		{"https_auth_443_sampler_default", "https://api.example.com:443", "https", "", true},
		{"https_auth_plain_sampler_443", "https://api.example.com/", "https", "443", true},
		{"http_auth_80_sampler_80", "http://api.example.com:80/home", "http", "80", true},
		{"other_port_does_not_match", "https://api.example.com:8443/", "https", "443", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ConvertJMX([]byte(authPortPlan(tc.authURL, tc.protocol, tc.port)), ConvertOptions{FileName: "auth.jmx"})
			if err != nil {
				t.Fatal(err)
			}
			action := result.Scripts[0].Actions[0]
			got := action.Auth != nil && action.Auth.Type == "basic" && action.Auth.Username == "fixture-user"
			if got != tc.want {
				t.Fatalf("auth matched = %v, want %v (action auth %#v, target %q)", got, tc.want, action.Auth, action.Target)
			}
		})
	}
}

func TestWithoutJMeterDefaultPort(t *testing.T) {
	for raw, want := range map[string]string{
		"https://h:443/p":      "https://h/p",
		"HTTPS://h:443":        "HTTPS://h",
		"http://h:80?q=1":      "http://h?q=1",
		"https://h:80/p":       "https://h:80/p",
		"https://h:4430/p":     "https://h:4430/p",
		"https://[::1]:443/p":  "https://[::1]/p",
		"https://[::1]/p":      "https://[::1]/p",
		"/relative/path":       "/relative/path",
		"https://h/p:443/deep": "https://h/p:443/deep",
	} {
		if got := withoutJMeterDefaultPort(raw); got != want {
			t.Errorf("withoutJMeterDefaultPort(%q) = %q, want %q", raw, got, want)
		}
	}
	if !strings.HasPrefix(withoutJMeterDefaultPort("https://h:443/a/b"), withoutJMeterDefaultPort("https://h/a")) {
		t.Fatal("normalized prefix must match")
	}
}

// Dropping an explicit default port must not let an Authorization URL match
// a longer host name, which JMeter's AuthManager never does.
func TestAuthorizationURLDefaultPortKeepsTheHostBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, authURL, domain, port string
		want                        bool
	}{
		{"longer_host_no_slash", "https://api.example.com:443", "api.example.com.other-tenant.net", "", false},
		{"longer_host_slash", "https://api.example.com:443/", "api.example.community", "443", false},
		{"other_port_on_same_host", "https://api.example.com:443", "api.example.com", "8443", false},
		{"same_host", "https://api.example.com:443", "api.example.com", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := strings.Replace(authPortPlan(tc.authURL, "https", tc.port), `<stringProp name="HTTPSampler.domain">api.example.com</stringProp>`, `<stringProp name="HTTPSampler.domain">`+tc.domain+`</stringProp>`, 1)
			result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "auth.jmx"})
			if err != nil {
				t.Fatal(err)
			}
			action := result.Scripts[0].Actions[0]
			got := action.Auth != nil && action.Auth.Username == "fixture-user"
			if got != tc.want {
				t.Fatalf("auth matched = %v, want %v (target %q)", got, tc.want, action.Target)
			}
		})
	}
}
